package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

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

// KGReadService is the consumer-side interface the read handler depends on.
// Implemented by a thin adapter wrapping the kgread usecase.
type KGReadService interface {
	ListBundles(r *http.Request) ([]KGBundleInfo, error)
	GetBundle(r *http.Request, bundleName string) (*KGBundleInfo, error)
	ListSchemas(r *http.Request, bundleName string) ([]KGSchemaInfo, error)
	GetSchema(r *http.Request, bundleName, entityType string) (*KGSchemaInfo, error)
	ListEntities(r *http.Request, bundleName, entityType string, filters map[string]any, limit, offset int) (*KGEntitiesListResponse, error)
	GetEntity(r *http.Request, bundleName, entityType, entityID string) (*KGEntityInfo, error)
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
	limit, err := parseLimitParam(r.URL.Query().Get("limit"), 50, 500)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	offset := parsePositiveInt(r.URL.Query().Get("offset"), 0, 1<<31-1)

	resp, err := h.svc.ListEntities(r, bundleName, entityType, filters, limit, offset)
	if err != nil {
		writeDomainError(w, err)
		return
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

// parseFilterQuery extracts `filter[field]=value` style params into a map
// suitable for JSONB containment filtering. Only string values are produced
// at this layer; numeric / boolean coercion is the service layer's concern
// based on the schema's property types.
func parseFilterQuery(q map[string][]string) map[string]any {
	out := make(map[string]any)
	for key, vals := range q {
		if len(vals) == 0 {
			continue
		}
		// Match keys of the form "filter[<field>]".
		const prefix = "filter["
		const suffix = "]"
		if len(key) <= len(prefix)+len(suffix) {
			continue
		}
		if key[:len(prefix)] != prefix || key[len(key)-1:] != suffix {
			continue
		}
		field := key[len(prefix) : len(key)-1]
		if field == "" {
			continue
		}
		out[field] = vals[0]
	}
	return out
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
