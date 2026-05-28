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
// produce list/get results at chat time. It mirrors the kgread package's
// EntityReader interface to avoid an import cycle (kgtools → kgread → ...).
type EntityReader interface {
	ListEntities(ctx context.Context, tenantID, bundleName, entityType string, filters map[string]any, limit, offset int) (items []*domain.KGEntity, total int, err error)
	GetEntity(ctx context.Context, tenantID, bundleName, entityType, entityID string) (*domain.KGEntity, error)
}

// SchemaByTypeReader resolves the schema for one (bundle, entity_type) so the
// list tool can validate filter keys against the x-index whitelist before
// hitting the DB. Implemented by configrepo.GORMKGSchemaRepository.GetSchema.
type SchemaByTypeReader interface {
	GetSchema(ctx context.Context, tenantID, bundleName, entityType string) (*domain.KGEntitySchema, error)
}

// AgentToolFactory builds runtime tool.InvokableTool instances for the
// auto-generated KG tool names (list_<entity_type>, get_<entity_type>).
//
// One factory is shared across all chat turns; the per-turn tenant scope
// comes from ctx. The factory needs the tenant's bundle/entity_type schemas
// to map a tool name (e.g. "list_industry") to the underlying KG bundle the
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
// `list_industry` or `get_industry`, scoping the lookup to the agent's bound
// bundles in the current tenant. Returns (nil, false) when the name is not a
// KG tool name in any bound bundle — caller falls back to its next resolver.
//
// Examples of names handled:
//   - list_industry → builds a list tool that queries kg_entity for entity_type=industry
//   - get_industry  → builds a get tool that fetches one entity by id
//
// Names that don't start with "list_" or "get_", or whose entity_type does not
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
	default:
		return nil, false
	}
}

// splitVerbType parses a tool name like "list_industry" or "get_use_case"
// into ("list", "industry") or ("get", "use_case"). Returns ("", "") on
// names that don't match the KG tool pattern.
func splitVerbType(name string) (verb, entityType string) {
	switch {
	case strings.HasPrefix(name, "list_"):
		return "list", strings.TrimPrefix(name, "list_")
	case strings.HasPrefix(name, "get_"):
		return "get", strings.TrimPrefix(name, "get_")
	default:
		return "", ""
	}
}

// kgListTool is the runtime tool the agent invokes as `list_<entity_type>`.
type kgListTool struct {
	tenantID   string
	bundleName string
	entityType string
	entities   EntityReader
	schemas    SchemaByTypeReader
}

type kgListArgs struct {
	Filters map[string]any `json:"filters,omitempty"`
	Limit   int            `json:"limit,omitempty"`
	Offset  int            `json:"offset,omitempty"`
}

func (t *kgListTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	desc := fmt.Sprintf("List entities of type %q from the %q knowledge graph bundle. "+
		"Use this to enumerate the catalog before fetching details.",
		t.entityType, t.bundleName)
	// Best-effort: include the entity-schema description / x-tool-description
	// if available.
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
					Desc: "Optional filter map: field → exact-match value. Only fields marked x-index in the entity schema are filterable.",
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

	items, total, err := t.entities.ListEntities(ctx, t.tenantID, t.bundleName, t.entityType, args.Filters, args.Limit, args.Offset)
	if err != nil {
		return fmt.Sprintf("[ERROR] list entities: %s", err.Error()), nil
	}

	// Emit a compact JSON response — items use the raw entity data so the
	// LLM sees the same shape the bundle author authored.
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
type kgGetTool struct {
	tenantID   string
	bundleName string
	entityType string
	entities   EntityReader
}

type kgGetArgs struct {
	ID string `json:"id"`
}

func (t *kgGetTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "get_" + t.entityType,
		Desc: fmt.Sprintf("Get one entity of type %q by its id from the %q knowledge graph bundle. Returns the full entity payload or an error if not found.", t.entityType, t.bundleName),
		ParamsOneOf: schema.NewParamsOneOfByParams(
			map[string]*schema.ParameterInfo{
				"id": {
					Type:     schema.String,
					Desc:     "The entity id (value of the schema's x-id-field).",
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
	if args.ID == "" {
		return "[ERROR] id is required", nil
	}
	e, err := t.entities.GetEntity(ctx, t.tenantID, t.bundleName, t.entityType, args.ID)
	if err != nil {
		return fmt.Sprintf("[ERROR] get entity: %s", err.Error()), nil
	}
	if e == nil {
		return fmt.Sprintf("[NOT_FOUND] entity %q of type %q not in bundle %q", args.ID, t.entityType, t.bundleName), nil
	}
	out := struct {
		ID   string          `json:"id"`
		Data json.RawMessage `json:"data"`
	}{ID: e.EntityID, Data: e.Data}
	buf, err := json.Marshal(out)
	if err != nil {
		return fmt.Sprintf("[ERROR] marshal: %s", err.Error()), nil
	}
	return string(buf), nil
}
