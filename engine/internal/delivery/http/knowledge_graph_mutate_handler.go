package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// maxBulkImportBodyBytes caps /import request bodies to protect the engine
// against gigabyte-payload DoS. Per-entity (100 KB) and per-bundle (10 MB)
// caps still apply downstream in the usecase; this is the outermost guard.
const maxBulkImportBodyBytes = 16 * 1024 * 1024

// Entity request bodies are raw flat objects matching bulk-import items[],
// e.g. {"code":"PM","name":"Property Management"}. The response wraps each
// entity with bundle/type/id/timestamps + a `data` field that carries the
// original payload — request and response shapes are intentionally asymmetric:
// the request *is* the entity, the response *describes* it.

// UpsertSchemaRequest is the body for PUT /knowledge-graphs/{bundle}/schemas/{entity_type}.
type UpsertSchemaRequest struct {
	Schema          json.RawMessage `json:"schema"`
	ExposeTools     []string        `json:"expose_tools,omitempty"`
	ToolDescription string          `json:"tool_description,omitempty"`
}

// BulkImportRequest is the body for POST /knowledge-graphs/{bundle}/import.
// Mirrors the structure embedded inside /config/import YAML.
type BulkImportRequest struct {
	Version  string                  `json:"version,omitempty"`
	Schemas  []BulkImportSchemaItem  `json:"schemas"`
	Entities []BulkImportEntityGroup `json:"entities"`
}

// BulkImportSchemaItem is one schema in the bulk import payload.
type BulkImportSchemaItem struct {
	EntityType      string          `json:"entity_type"`
	Schema          json.RawMessage `json:"schema"`
	ExposeTools     []string        `json:"expose_tools,omitempty"`
	ToolDescription string          `json:"tool_description,omitempty"`
}

// BulkImportEntityGroup groups entities of one type for bulk import.
type BulkImportEntityGroup struct {
	EntityType string           `json:"entity_type"`
	Items      []map[string]any `json:"items"`
}

// KGMutateService is the consumer-side interface the mutate handler depends
// on. Implemented by a thin adapter wrapping the kgapply + kgmutate usecases.
type KGMutateService interface {
	BulkImport(r *http.Request, bundleName string, req BulkImportRequest) error
	CreateEntity(r *http.Request, bundleName, entityType string, data map[string]any) (*KGEntityInfo, error)
	UpdateEntity(r *http.Request, bundleName, entityType, entityID string, data map[string]any) (*KGEntityInfo, error)
	DeleteEntity(r *http.Request, bundleName, entityType, entityID string) error
	UpsertSchema(r *http.Request, bundleName, entityType string, req UpsertSchemaRequest) (*KGSchemaInfo, error)
	DeleteBundle(r *http.Request, bundleName string) error
}

// KGMutateHandler exposes mutation endpoints for Knowledge Graphs.
type KGMutateHandler struct {
	svc KGMutateService
}

// NewKGMutateHandler returns a handler bound to the given service.
func NewKGMutateHandler(svc KGMutateService) *KGMutateHandler {
	return &KGMutateHandler{svc: svc}
}

// BulkImport handles POST /api/v1/knowledge-graphs/{bundle}/import.
func (h *KGMutateHandler) BulkImport(w http.ResponseWriter, r *http.Request) {
	bundleName := chi.URLParam(r, "bundle")

	r.Body = http.MaxBytesReader(w, r.Body, maxBulkImportBodyBytes)
	var req BulkImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
				"error": fmt.Sprintf("[PAYLOAD_TOO_LARGE] /import body exceeds %d-byte limit", maxBulkImportBodyBytes),
			})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "parse body: " + err.Error()})
		return
	}
	if err := h.svc.BulkImport(r, bundleName, req); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"applied": true, "bundle_name": bundleName})
}

// CreateEntity handles POST /api/v1/knowledge-graphs/{bundle}/entities/{entity_type}.
// Body is a flat JSON object matching the schema (the entity itself).
func (h *KGMutateHandler) CreateEntity(w http.ResponseWriter, r *http.Request) {
	bundleName := chi.URLParam(r, "bundle")
	entityType := chi.URLParam(r, "entity_type")

	data, err := decodeEntityBody(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	entity, err := h.svc.CreateEntity(r, bundleName, entityType, data)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, entity)
}

// UpdateEntity handles PUT /api/v1/knowledge-graphs/{bundle}/entities/{entity_type}/{id}.
// Body is a flat JSON object matching the schema (the entity itself).
func (h *KGMutateHandler) UpdateEntity(w http.ResponseWriter, r *http.Request) {
	bundleName := chi.URLParam(r, "bundle")
	entityType := chi.URLParam(r, "entity_type")
	entityID := chi.URLParam(r, "id")

	data, err := decodeEntityBody(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	entity, err := h.svc.UpdateEntity(r, bundleName, entityType, entityID, data)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entity)
}

// decodeEntityBody reads the request body as a flat JSON object representing
// the entity. Rejects null/empty bodies and non-object payloads.
func decodeEntityBody(r *http.Request) (map[string]any, error) {
	var data map[string]any
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("[INVALID_INPUT] parse body: %s", err.Error())
	}
	if data == nil {
		return nil, fmt.Errorf("[INVALID_INPUT] entity body must be a non-empty JSON object")
	}
	return data, nil
}

// DeleteEntity handles DELETE /api/v1/knowledge-graphs/{bundle}/entities/{entity_type}/{id}.
func (h *KGMutateHandler) DeleteEntity(w http.ResponseWriter, r *http.Request) {
	bundleName := chi.URLParam(r, "bundle")
	entityType := chi.URLParam(r, "entity_type")
	entityID := chi.URLParam(r, "id")

	if err := h.svc.DeleteEntity(r, bundleName, entityType, entityID); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UpsertSchema handles PUT /api/v1/knowledge-graphs/{bundle}/schemas/{entity_type}.
func (h *KGMutateHandler) UpsertSchema(w http.ResponseWriter, r *http.Request) {
	bundleName := chi.URLParam(r, "bundle")
	entityType := chi.URLParam(r, "entity_type")

	var req UpsertSchemaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "parse body: " + err.Error()})
		return
	}
	schema, err := h.svc.UpsertSchema(r, bundleName, entityType, req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, schema)
}

// DeleteBundle handles DELETE /api/v1/knowledge-graphs/{bundle}.
func (h *KGMutateHandler) DeleteBundle(w http.ResponseWriter, r *http.Request) {
	bundleName := chi.URLParam(r, "bundle")

	if err := h.svc.DeleteBundle(r, bundleName); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
