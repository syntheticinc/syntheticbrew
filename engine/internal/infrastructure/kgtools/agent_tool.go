package kgtools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// EntityReader is the read-side dependency the dynamic KG tools need to
// produce list/get/batch-get results at chat time. It mirrors the kgread
// package's EntityReader interface to avoid an import cycle (kgtools →
// kgread → ...).
//
// 1.4.0 expanded the interface:
//   - ListEntities now accepts a sort spec ([]SortHint) alongside filters
//   - GetEntities (batch) replaces single-ID lookup in agent-facing tools;
//     GetEntity is retained for REST single-id endpoint use
type EntityReader interface {
	ListEntities(ctx context.Context, tenantID, bundleName, entityType string, filters map[string]any, sort []SortHint, limit, offset int) (items []*domain.KGEntity, total int, err error)
	GetEntity(ctx context.Context, tenantID, bundleName, entityType, entityID string) (*domain.KGEntity, error)
	GetEntities(ctx context.Context, tenantID, bundleName, entityType string, ids []string) (found []*domain.KGEntity, notFound []string, err error)
}

// SortHint carries one sort directive (field + order) from tool args to the
// reader. Mirrors kgread.SortSpec without the engine-populated EnumValues —
// the kgread layer enriches that downstream of this struct.
type SortHint struct {
	Field string `json:"field"`
	Order string `json:"order"`
}

// SchemaByTypeReader resolves the schema for one (bundle, entity_type) so the
// list tool can validate filter keys against the x-index whitelist before
// hitting the DB. Implemented by configrepo.GORMKGSchemaRepository.GetSchema.
type SchemaByTypeReader interface {
	GetSchema(ctx context.Context, tenantID, bundleName, entityType string) (*domain.KGEntitySchema, error)
}

// AgentToolFactory builds runtime tool.InvokableTool instances for the
// auto-generated KG tool names (list_<entity_type>, get_<entity_type>,
// list_<entity_type>_ids).
//
// One factory is shared across all chat turns; the per-turn tenant scope
// comes from ctx. The factory needs the tenant's bundle/entity_type schemas
// to map a tool name (e.g. "list_category") to the underlying KG bundle the
// agent is bound to. Bundles are looked up via the Provider (already wired
// for the capability resolver).
type AgentToolFactory struct {
	provider *Provider
	entities EntityReader
	schemas  SchemaByTypeReader
}

// NewAgentToolFactory returns a factory bound to the provider + reader pair.
func NewAgentToolFactory(p *Provider, entities EntityReader, schemas SchemaByTypeReader) *AgentToolFactory {
	return &AgentToolFactory{provider: p, entities: entities, schemas: schemas}
}

// BuildTool returns an InvokableTool implementation for a name like
// `list_category`, `get_category`, or `list_category_ids`, scoping the lookup
// to the agent's bound bundles in the current tenant. Returns (nil, false)
// when the name is not a KG tool name in any bound bundle — caller falls
// back to its next resolver.
//
// Names that don't match a KG tool pattern, or whose entity_type does not
// resolve to a schema in any of the agent's bound bundles, return (nil, false).
func (f *AgentToolFactory) BuildTool(ctx context.Context, name string, bundles []string) (tool.InvokableTool, bool) {
	if f == nil || f.provider == nil || len(bundles) == 0 {
		return nil, false
	}

	verb, entityType := splitVerbType(name)
	if verb == "" {
		return nil, false
	}

	tenantID := domain.TenantIDFromContext(ctx)
	if tenantID == "" {
		tenantID = domain.CETenantID
	}

	// Find which of the agent's bundles owns this entity_type. The first
	// match wins — collision detection (apply-time) prevents two bundles in
	// the same tenant from publishing the same entity_type, so deterministic
	// "first match" is safe.
	var owningBundle string
	for _, b := range bundles {
		s, err := f.schemas.GetSchema(ctx, tenantID, b, entityType)
		if err == nil && s != nil {
			owningBundle = b
			break
		}
	}
	if owningBundle == "" {
		return nil, false
	}

	switch verb {
	case "list":
		return &kgListTool{
			tenantID:   tenantID,
			bundleName: owningBundle,
			entityType: entityType,
			entities:   f.entities,
			schemas:    f.schemas,
		}, true
	case "get":
		return &kgGetTool{
			tenantID:   tenantID,
			bundleName: owningBundle,
			entityType: entityType,
			entities:   f.entities,
		}, true
	case "list_ids":
		return &kgListIDsTool{
			tenantID:   tenantID,
			bundleName: owningBundle,
			entityType: entityType,
			entities:   f.entities,
			schemas:    f.schemas,
		}, true
	default:
		return nil, false
	}
}

// splitVerbType parses a tool name like "list_category", "get_brand", or
// "list_category_ids" into a (verb, entity_type) pair. The `_ids` suffix is
// recognised on list-prefixed names; "list_use_case_ids" → ("list_ids",
// "use_case"). Returns ("", "") on names that don't match the KG tool pattern.
func splitVerbType(name string) (verb, entityType string) {
	switch {
	case strings.HasPrefix(name, "list_") && strings.HasSuffix(name, "_ids"):
		// list_<X>_ids → ("list_ids", "<X>")
		trimmed := strings.TrimPrefix(name, "list_")
		trimmed = strings.TrimSuffix(trimmed, "_ids")
		if trimmed == "" {
			return "", ""
		}
		return "list_ids", trimmed
	case strings.HasPrefix(name, "list_"):
		return "list", strings.TrimPrefix(name, "list_")
	case strings.HasPrefix(name, "get_"):
		return "get", strings.TrimPrefix(name, "get_")
	default:
		return "", ""
	}
}

// kgToolDescriptionDefault returns the default description used when a
// schema author did not set x-tool-description. Centralised so the three
// auto-tools render consistent baseline text and 1.4.0 caveats (enum sort,
// NULLS LAST, batch get partial success) live in one place.
func kgToolDescriptionDefault(toolName, entityType, bundleName, kind string) string {
	switch kind {
	case "list":
		return fmt.Sprintf(
			"List entities of type %q from the %q knowledge graph bundle. "+
				"Returns full entity payloads. Supports filters (equality, gte/gt/lte/lt range on numeric and date fields, in for multi-value), "+
				"sort (asc/desc by indexed fields; enum fields sort by declaration order — NOT alphabetical; missing values appear last). "+
				"For agent-facing browsing prefer %s first to see compact previews, then %s with the chosen ids for full payloads.",
			entityType, bundleName,
			"list_"+entityType+"_ids", "get_"+entityType,
		)
	case "get":
		return fmt.Sprintf(
			"Batch fetch entities of type %q from the %q knowledge graph bundle. "+
				"Accepts an array of ids (max 500). Returns matched entities in input order plus a not_found array for missing ids — "+
				"partial success is normal, the tool never fails when some ids miss.",
			entityType, bundleName,
		)
	case "list_ids":
		return fmt.Sprintf(
			"List entity ids of type %q from the %q knowledge graph bundle. "+
				"Cheap preview pass: response includes ids plus any summary fields the schema author marked with x-summary-fields. "+
				"Use this for discovery, then call %s with a chosen subset of ids to retrieve full payloads. "+
				"Same filter and sort operators as %s.",
			entityType, bundleName,
			"get_"+entityType, "list_"+entityType,
		)
	}
	return toolName
}

// kgListTool is the runtime tool the agent invokes as `list_<entity_type>`.
type kgListTool struct {
	tenantID   string
	bundleName string
	entityType string
	entities   EntityReader
	schemas    SchemaByTypeReader
}

// kgListArgs is the LLM-facing argument shape for `list_<entity_type>`.
//
// Filters supports both 1.3.0 equality (`{"field": "value"}`) and 1.4.0
// operator-bag (`{"field": {"gte": 70}}`) shapes — the value-polymorphism is
// resolved by the downstream `plainFiltersToKgread` adapter in internal/app.
type kgListArgs struct {
	Filters map[string]any `json:"filters,omitempty"`
	Sort    []SortHint     `json:"sort,omitempty"`
	Limit   int            `json:"limit,omitempty"`
	Offset  int            `json:"offset,omitempty"`
}

func (t *kgListTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	desc := kgToolDescriptionDefault("list_"+t.entityType, t.entityType, t.bundleName, "list")
	if s, err := t.schemas.GetSchema(ctx, t.tenantID, t.bundleName, t.entityType); err == nil && s != nil {
		if s.ToolDescription != "" {
			desc = s.ToolDescription
		}
	}
	return &schema.ToolInfo{
		Name: "list_" + t.entityType,
		Desc: desc,
		ParamsOneOf: schema.NewParamsOneOfByParams(
			map[string]*schema.ParameterInfo{
				"filters": {
					Type: schema.Object,
					Desc: "Optional filter map. Bare value = equality. Nested operator bag = {gte, gt, lte, lt} for range (numeric/date only) or {in: [v1,v2,...]} for multi-value. Only fields marked x-index in the schema are filterable.",
				},
				"sort": {
					Type: schema.Array,
					Desc: "Optional ORDER BY array: [{field, order}]. order is \"asc\" or \"desc\". Enum fields sort by declaration order, not alphabetical. Missing values appear last (NULLS LAST).",
				},
				"limit": {
					Type: schema.Integer,
					Desc: "Max number of entities to return (default 50, max 500).",
				},
				"offset": {
					Type: schema.Integer,
					Desc: "Number of entities to skip for pagination (default 0).",
				},
			},
		),
	}, nil
}

func (t *kgListTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var args kgListArgs
	if argumentsInJSON != "" {
		if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
			return fmt.Sprintf("[ERROR] parse args: %s", err.Error()), nil
		}
	}
	if args.Limit == 0 {
		args.Limit = 50
	}

	items, total, err := t.entities.ListEntities(ctx, t.tenantID, t.bundleName, t.entityType, args.Filters, args.Sort, args.Limit, args.Offset)
	if err != nil {
		return fmt.Sprintf("[ERROR] list entities: %s", err.Error()), nil
	}

	type itemDTO struct {
		ID   string          `json:"id"`
		Data json.RawMessage `json:"data"`
	}
	out := struct {
		Items  []itemDTO `json:"items"`
		Total  int       `json:"total"`
		Limit  int       `json:"limit"`
		Offset int       `json:"offset"`
	}{
		Items:  make([]itemDTO, 0, len(items)),
		Total:  total,
		Limit:  args.Limit,
		Offset: args.Offset,
	}
	for _, e := range items {
		out.Items = append(out.Items, itemDTO{ID: e.EntityID, Data: e.Data})
	}
	buf, err := json.Marshal(out)
	if err != nil {
		return fmt.Sprintf("[ERROR] marshal response: %s", err.Error()), nil
	}
	return string(buf), nil
}

// kgGetTool is the runtime tool the agent invokes as `get_<entity_type>`.
// 1.4.0 BREAKING: signature changed from single-id to ids[] array; response
// shape now {entities, not_found} with partial-success semantics.
type kgGetTool struct {
	tenantID   string
	bundleName string
	entityType string
	entities   EntityReader
}

type kgGetArgs struct {
	IDs []string `json:"ids"`
}

func (t *kgGetTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "get_" + t.entityType,
		Desc: kgToolDescriptionDefault("get_"+t.entityType, t.entityType, t.bundleName, "get"),
		ParamsOneOf: schema.NewParamsOneOfByParams(
			map[string]*schema.ParameterInfo{
				"ids": {
					Type:     schema.Array,
					Desc:     "Array of entity ids (max 500). Results returned in input order; missing ids surface in not_found rather than failing the call.",
					Required: true,
				},
			},
		),
	}, nil
}

func (t *kgGetTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var args kgGetArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] parse args: %s", err.Error()), nil
	}
	if len(args.IDs) == 0 {
		return "[INVALID_INPUT] ids must contain at least one element", nil
	}
	found, notFound, err := t.entities.GetEntities(ctx, t.tenantID, t.bundleName, t.entityType, args.IDs)
	if err != nil {
		return fmt.Sprintf("[ERROR] batch get entities: %s", err.Error()), nil
	}

	type entityDTO struct {
		ID   string          `json:"id"`
		Data json.RawMessage `json:"data"`
	}
	out := struct {
		Entities []entityDTO `json:"entities"`
		NotFound []string    `json:"not_found"`
	}{
		Entities: make([]entityDTO, 0, len(found)),
		NotFound: notFound,
	}
	if out.NotFound == nil {
		out.NotFound = []string{}
	}
	for _, e := range found {
		out.Entities = append(out.Entities, entityDTO{ID: e.EntityID, Data: e.Data})
	}
	buf, err := json.Marshal(out)
	if err != nil {
		return fmt.Sprintf("[ERROR] marshal: %s", err.Error()), nil
	}
	return string(buf), nil
}

// kgListIDsTool is the runtime tool the agent invokes as `list_<entity_type>_ids`.
// 1.4.0 NEW: domain layer already declared this expose option in 1.3.0 but
// the factory switch was missing — list_X_ids names were never built. This
// closes that gap and adds the x-summary-fields projection on top.
//
// When the schema declares x-summary-fields the response shape switches from
// {ids, total} to {items: [{id_field, ...summary_fields}], total}. Default
// (no annotation) preserves the bare-ids shape for backward compat with any
// 1.3.x bundle that might already use list_ids tool name.
type kgListIDsTool struct {
	tenantID   string
	bundleName string
	entityType string
	entities   EntityReader
	schemas    SchemaByTypeReader
}

func (t *kgListIDsTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	desc := kgToolDescriptionDefault("list_"+t.entityType+"_ids", t.entityType, t.bundleName, "list_ids")
	if s, err := t.schemas.GetSchema(ctx, t.tenantID, t.bundleName, t.entityType); err == nil && s != nil {
		if s.ToolDescription != "" {
			desc = s.ToolDescription
		}
	}
	return &schema.ToolInfo{
		Name: "list_" + t.entityType + "_ids",
		Desc: desc,
		ParamsOneOf: schema.NewParamsOneOfByParams(
			map[string]*schema.ParameterInfo{
				"filters": {
					Type: schema.Object,
					Desc: "Optional filter map. Same shape as list_<entity>: bare value = equality, nested {gte/gt/lte/lt} = range, {in: [...]} = multi-value. Only x-index fields are filterable.",
				},
				"sort": {
					Type: schema.Array,
					Desc: "Optional ORDER BY array: [{field, order}]. Same semantics as list_<entity> sort — enum by declaration order, NULLS LAST.",
				},
				"limit": {
					Type: schema.Integer,
					Desc: "Max number of items to return (default 50, max 500).",
				},
				"offset": {
					Type: schema.Integer,
					Desc: "Pagination offset (default 0).",
				},
			},
		),
	}, nil
}

func (t *kgListIDsTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var args kgListArgs
	if argumentsInJSON != "" {
		if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
			return fmt.Sprintf("[ERROR] parse args: %s", err.Error()), nil
		}
	}
	if args.Limit == 0 {
		args.Limit = 50
	}

	items, total, err := t.entities.ListEntities(ctx, t.tenantID, t.bundleName, t.entityType, args.Filters, args.Sort, args.Limit, args.Offset)
	if err != nil {
		return fmt.Sprintf("[ERROR] list entities: %s", err.Error()), nil
	}

	// Decide response shape based on schema's x-summary-fields annotation:
	// non-empty → projection mode {items, total}; empty/absent → bare ids
	// mode {ids, total} (1.3.x backward compat).
	summaryFields := t.resolveSummaryFields(ctx)
	if len(summaryFields) == 0 {
		return marshalBareIDsResponse(items, total)
	}
	return marshalSummaryResponse(items, total, summaryFields)
}

// resolveSummaryFields looks up the schema and parses x-summary-fields. On
// any error it falls back to no projection (bare-ids mode) — the response
// shape is the customer-facing contract, so a transient parse error should
// not break the tool entirely. Logged downstream by the schema reader.
func (t *kgListIDsTool) resolveSummaryFields(ctx context.Context) []string {
	s, err := t.schemas.GetSchema(ctx, t.tenantID, t.bundleName, t.entityType)
	if err != nil || s == nil {
		return nil
	}
	return summaryFieldsFromSchemaJSON(s.SchemaJSON)
}

// marshalBareIDsResponse keeps the 1.3.x backward-compatible {ids, total}
// shape used when x-summary-fields is absent.
func marshalBareIDsResponse(items []*domain.KGEntity, total int) (string, error) {
	ids := make([]string, 0, len(items))
	for _, e := range items {
		ids = append(ids, e.EntityID)
	}
	out := struct {
		IDs   []string `json:"ids"`
		Total int      `json:"total"`
	}{IDs: ids, Total: total}
	buf, err := json.Marshal(out)
	if err != nil {
		return fmt.Sprintf("[ERROR] marshal: %s", err.Error()), nil
	}
	return string(buf), nil
}

// marshalSummaryResponse produces the 1.4.0 projection shape
// {items: [{id, ...selected fields}], total} when x-summary-fields is set.
// Selected fields are pulled from the entity's data JSONB; missing fields
// are silently skipped (Chirp-contract: ID auto-included).
func marshalSummaryResponse(items []*domain.KGEntity, total int, summaryFields []string) (string, error) {
	out := struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}{
		Items: make([]map[string]any, 0, len(items)),
		Total: total,
	}
	for _, e := range items {
		var data map[string]any
		if err := json.Unmarshal(e.Data, &data); err != nil {
			// Skip malformed rows rather than failing the whole call.
			continue
		}
		row := map[string]any{"id": e.EntityID}
		for _, f := range summaryFields {
			if v, ok := data[f]; ok {
				row[f] = v
			}
		}
		out.Items = append(out.Items, row)
	}
	buf, err := json.Marshal(out)
	if err != nil {
		return fmt.Sprintf("[ERROR] marshal: %s", err.Error()), nil
	}
	return string(buf), nil
}
