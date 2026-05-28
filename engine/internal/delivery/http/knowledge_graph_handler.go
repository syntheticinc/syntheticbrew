package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// KGBundleInfo is the API DTO for a Knowledge Graph bundle.
type KGBundleInfo struct {
	BundleName string         `json:"bundle_name"`
	Version    string         `json:"version"`
	Manifest   map[string]any `json:"manifest"`
	CreatedAt  string         `json:"created_at"`
	UpdatedAt  string         `json:"updated_at"`
}

// KGSchemaInfo is the API DTO for a single entity schema.
type KGSchemaInfo struct {
	BundleName      string          `json:"bundle_name"`
	EntityType      string          `json:"entity_type"`
	SchemaJSON      json.RawMessage `json:"schema_json"`
	SchemaHash      string          `json:"schema_hash"`
	IDField         string          `json:"id_field"`
	ExposeTools     []string        `json:"expose_tools"`
	ToolDescription string          `json:"tool_description,omitempty"`
}

// KGEntityInfo is the API DTO for a single entity instance.
type KGEntityInfo struct {
	BundleName string          `json:"bundle_name"`
	EntityType string          `json:"entity_type"`
	EntityID   string          `json:"entity_id"`
	Data       json.RawMessage `json:"data"`
	SchemaHash string          `json:"schema_hash"`
	CreatedAt  string          `json:"created_at"`
	UpdatedAt  string          `json:"updated_at"`
}

// KGEntitiesListResponse is the paginated list response.
type KGEntitiesListResponse struct {
	Items  []KGEntityInfo `json:"items"`
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}

// KGSortParam carries one entry of the `sort=field:order,field:order` REST
// query parameter. The handler parses the raw string into this list; the
// service layer converts it to the typed kgread.SortSpec downstream.
type KGSortParam struct {
	Field string
	Order string
}

// KGBatchGetResponse is the body returned by POST /entities/{type}/batch-get.
// Entities preserve input-id order; missing ids appear in NotFound (also in
// input order). The shape mirrors the auto-MCP tool response so REST and
// tool consumers see the same model.
type KGBatchGetResponse struct {
	Entities []KGEntityInfo `json:"entities"`
	NotFound []string       `json:"not_found"`
}

// KGReadService is the consumer-side interface the read handler depends on.
// Implemented by a thin adapter wrapping the kgread usecase.
//
// 1.4.0: ListEntities gained a sort parameter; filters now carry operator
// bags (nested maps recognised by the downstream FilterSpec normalisation).
// BatchGetEntities is added for REST/tool API symmetry — same shape as the
// auto-MCP `get_<entity>(ids[])` tool response.
type KGReadService interface {
	ListBundles(r *http.Request) ([]KGBundleInfo, error)
	GetBundle(r *http.Request, bundleName string) (*KGBundleInfo, error)
	ListSchemas(r *http.Request, bundleName string) ([]KGSchemaInfo, error)
	GetSchema(r *http.Request, bundleName, entityType string) (*KGSchemaInfo, error)
	ListEntities(r *http.Request, bundleName, entityType string, filters map[string]any, sort []KGSortParam, limit, offset int) (*KGEntitiesListResponse, error)
	GetEntity(r *http.Request, bundleName, entityType, entityID string) (*KGEntityInfo, error)
	BatchGetEntities(r *http.Request, bundleName, entityType string, ids []string) (*KGBatchGetResponse, error)
}

// KGReadHandler exposes the read endpoints for Knowledge Graphs.
// Mutations live in KGMutateHandler (see knowledge_graph_mutate_handler.go).
type KGReadHandler struct {
	svc KGReadService
}

// NewKGReadHandler returns a handler bound to the given service.
func NewKGReadHandler(svc KGReadService) *KGReadHandler {
	return &KGReadHandler{svc: svc}
}

// ListBundles handles GET /api/v1/knowledge-graphs.
func (h *KGReadHandler) ListBundles(w http.ResponseWriter, r *http.Request) {
	bundles, err := h.svc.ListBundles(r)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if bundles == nil {
		bundles = []KGBundleInfo{}
	}
	writeJSON(w, http.StatusOK, bundles)
}

// GetBundle handles GET /api/v1/knowledge-graphs/{bundle}.
func (h *KGReadHandler) GetBundle(w http.ResponseWriter, r *http.Request) {
	bundleName := chi.URLParam(r, "bundle")
	b, err := h.svc.GetBundle(r, bundleName)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if b == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "bundle not found"})
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// ListSchemas handles GET /api/v1/knowledge-graphs/{bundle}/schemas.
func (h *KGReadHandler) ListSchemas(w http.ResponseWriter, r *http.Request) {
	bundleName := chi.URLParam(r, "bundle")
	schemas, err := h.svc.ListSchemas(r, bundleName)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if schemas == nil {
		schemas = []KGSchemaInfo{}
	}
	writeJSON(w, http.StatusOK, schemas)
}

// GetSchema handles GET /api/v1/knowledge-graphs/{bundle}/schemas/{entity_type}.
func (h *KGReadHandler) GetSchema(w http.ResponseWriter, r *http.Request) {
	bundleName := chi.URLParam(r, "bundle")
	entityType := chi.URLParam(r, "entity_type")
	s, err := h.svc.GetSchema(r, bundleName, entityType)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if s == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "schema not found"})
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// ListEntities handles GET /api/v1/knowledge-graphs/{bundle}/entities/{entity_type}.
// Filters are parsed from query params with the prefix `filter[<field>]=<value>`
// — only x-index fields are exposed by the schema, so the whitelist is
// enforced at the service layer.
func (h *KGReadHandler) ListEntities(w http.ResponseWriter, r *http.Request) {
	bundleName := chi.URLParam(r, "bundle")
	entityType := chi.URLParam(r, "entity_type")

	filters := parseFilterQuery(r.URL.Query())
	sort, err := parseSortQuery(r.URL.Query().Get("sort"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	limit, err := parseLimitParam(r.URL.Query().Get("limit"), 50, 500)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	offset := parsePositiveInt(r.URL.Query().Get("offset"), 0, 1<<31-1)

	resp, err := h.svc.ListEntities(r, bundleName, entityType, filters, sort, limit, offset)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// BatchGetEntities handles POST /api/v1/knowledge-graphs/{bundle}/entities/{entity_type}/batch-get.
// Body shape: {"ids": ["A","B","C"]}. Response preserves input order; missing
// ids appear in `not_found`. Partial success — 200 even when some ids miss.
// Edge cases (empty / >500 / dedup) surface as 400 from the usecase.
func (h *KGReadHandler) BatchGetEntities(w http.ResponseWriter, r *http.Request) {
	bundleName := chi.URLParam(r, "bundle")
	entityType := chi.URLParam(r, "entity_type")

	var body struct {
		IDs []string `json:"ids"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "[INVALID_INPUT] body must be {\"ids\": [...]}",
		})
		return
	}

	resp, err := h.svc.BatchGetEntities(r, bundleName, entityType, body.IDs)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if resp.NotFound == nil {
		resp.NotFound = []string{}
	}
	if resp.Entities == nil {
		resp.Entities = []KGEntityInfo{}
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetEntity handles GET /api/v1/knowledge-graphs/{bundle}/entities/{entity_type}/{id}.
func (h *KGReadHandler) GetEntity(w http.ResponseWriter, r *http.Request) {
	bundleName := chi.URLParam(r, "bundle")
	entityType := chi.URLParam(r, "entity_type")
	entityID := chi.URLParam(r, "id")

	e, err := h.svc.GetEntity(r, bundleName, entityType, entityID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if e == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "entity not found"})
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// parseFilterQuery extracts the filter family of query params:
//   - `filter[<field>]=<value>`                  → equality (1.3.0 syntax)
//   - `filter[<field>][in]=v1,v2,v3`             → IN (1.4.0)
//   - `filter[<field>][gte|gt|lte|lt]=<value>`   → range  (1.4.0)
//
// Range and IN params collapse into a nested map under the same field key
// (`{"score": {"gte": "70", "lte": "95"}}`) — the downstream FilterSpec
// adapter recognises this shape and emits typed operators. Equality params
// produce the bare value the 1.3.x adapter expected, preserving backward
// compat on query strings.
//
// Only string values are produced here; numeric / date coercion happens
// inside the SQL builder which knows the schema property types.
func parseFilterQuery(q map[string][]string) map[string]any {
	out := make(map[string]any)
	const prefix = "filter["

	for key, vals := range q {
		if len(vals) == 0 || !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, "]") {
			continue
		}
		inner := strings.TrimSuffix(strings.TrimPrefix(key, prefix), "]")
		// inner is either "field" (bare) or "field][operator".
		field, op := splitFilterFieldAndOp(inner)
		if field == "" {
			continue
		}

		switch op {
		case "":
			// Bare filter[field] — equality. Last writer wins if both bare and
			// operator forms appear (callers shouldn't mix them).
			out[field] = vals[0]
		case "in":
			ensureOpMap(out, field)["in"] = splitCommaList(vals[0])
		case "gte", "gt", "lte", "lt":
			ensureOpMap(out, field)[op] = vals[0]
		default:
			// Unknown operator — ignore silently. Sending a clear error here
			// would surface to LLMs as a tool failure even for typos that the
			// service layer already rejects with a friendlier message.
		}
	}
	return out
}

// splitFilterFieldAndOp parses the inner part of a filter[…] key:
//   - "score"               → ("score", "")
//   - "score][gte"          → ("score", "gte")
//   - "industry][in"        → ("industry", "in")
//
// Returns ("", "") on shapes we don't recognise.
func splitFilterFieldAndOp(inner string) (field, op string) {
	if !strings.Contains(inner, "][") {
		return inner, ""
	}
	idx := strings.Index(inner, "][")
	return inner[:idx], inner[idx+2:]
}

// ensureOpMap returns the operator-bag nested map for field, creating it if
// the slot currently holds a non-map value. A bare equality previously
// recorded is overwritten — callers should not mix forms on the same field.
func ensureOpMap(out map[string]any, field string) map[string]any {
	if existing, ok := out[field].(map[string]any); ok {
		return existing
	}
	bag := make(map[string]any)
	out[field] = bag
	return bag
}

// splitCommaList parses `a,b,c` into `["a","b","c"]`. Empty segments are
// preserved as empty strings — the validation layer rejects empty IN entries
// with a clear error, so we don't silently filter here.
func splitCommaList(raw string) []any {
	parts := strings.Split(raw, ",")
	out := make([]any, 0, len(parts))
	for _, p := range parts {
		out = append(out, p)
	}
	return out
}

// parseSortQuery turns `field1:order,field2:order` into a typed slice. Each
// entry must be `field:asc` or `field:desc`. Empty input returns nil. Invalid
// shape produces a 400-eligible error envelope.
//
// Per CLAUDE.md "no client names in tracked artefacts" + KG-SEC-08 (query
// string size DoS): the handler rejects strings longer than maxSortQuerySize
// before parsing, so a 1MB sort= burst never enters the splitter.
func parseSortQuery(raw string) ([]KGSortParam, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	const maxSortQuerySize = 2048
	if len(raw) > maxSortQuerySize {
		return nil, fmt.Errorf("[INVALID_INPUT] sort query too long (max %d bytes)", maxSortQuerySize)
	}
	parts := strings.Split(raw, ",")
	out := make([]KGSortParam, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, fmt.Errorf("[INVALID_INPUT] sort entry must be field:order")
		}
		colon := strings.Index(p, ":")
		if colon <= 0 || colon == len(p)-1 {
			return nil, fmt.Errorf("[INVALID_INPUT] sort entry %q must be field:order", p)
		}
		field := p[:colon]
		order := strings.ToLower(p[colon+1:])
		if order != "asc" && order != "desc" {
			return nil, fmt.Errorf("[INVALID_INPUT] sort order must be asc or desc, got %q", order)
		}
		out = append(out, KGSortParam{Field: field, Order: order})
	}
	return out, nil
}

// parsePositiveInt returns the parsed int clamped between 0 and max.
// Returns def for empty input. Negative or non-numeric inputs return def.
func parsePositiveInt(s string, def, max int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// parseLimitParam parses a pagination limit and rejects values outside [1..max]
// rather than silently clamping. Empty input returns def. Plan TestKG05.
func parseLimitParam(s string, def, max int) (int, error) {
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("[INVALID_INPUT] limit must be an integer")
	}
	if n < 1 || n > max {
		return 0, fmt.Errorf("[INVALID_INPUT] limit must be between 1 and %d", max)
	}
	return n, nil
}
