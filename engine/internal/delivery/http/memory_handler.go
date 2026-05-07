package http

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/syntheticinc/bytebrew/engine/internal/domain"
)

// MemoryLister lists memories for a schema (AC-MEM-03).
type MemoryLister interface {
	Execute(ctx context.Context, schemaID string) ([]*domain.Memory, error)
}

// MemoryClearer clears memories for a schema (AC-MEM-03).
type MemoryClearer interface {
	ClearAll(ctx context.Context, schemaID string) (int64, error)
	DeleteOne(ctx context.Context, id string) error
}

// MemoryHandler handles memory-related HTTP endpoints.
//
// Engine 1.1.0 made the URL `{name}` segment a stable operator-facing handle
// (was UUID in 1.0.x). The handler resolves the schema name to a tenant-scoped
// UUID via SchemaNameResolver before invoking the underlying memory service.
type MemoryHandler struct {
	lister   MemoryLister
	clearer  MemoryClearer
	resolver SchemaNameResolver
}

// NewMemoryHandler creates a new MemoryHandler.
//
// resolver is the tenant-scoped name → UUID resolver — required to translate
// the URL `{name}` segment into the canonical schema UUID consumed by the
// memory service.
func NewMemoryHandler(lister MemoryLister, clearer MemoryClearer, resolver SchemaNameResolver) *MemoryHandler {
	return &MemoryHandler{
		lister:   lister,
		clearer:  clearer,
		resolver: resolver,
	}
}

// resolveSchemaName translates the `{name}` URL param into a tenant-scoped
// schema UUID. On any error it writes the appropriate HTTP response and
// returns ("", false); callers must not write further output when ok == false.
func (h *MemoryHandler) resolveSchemaName(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := chi.URLParam(r, "name")
	id, err := resolveSchemaNameToUUID(r.Context(), h.resolver, name)
	if err != nil {
		writeNameLookupError(r.Context(), w, "schema", name, err)
		return "", false
	}
	return id, true
}

// memoryResponse represents a single memory entry in the API response.
type memoryResponse struct {
	ID        string            `json:"id"`
	SchemaID  string            `json:"schema_id"`
	UserSub   string            `json:"user_sub"`
	Content   string            `json:"content"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// ListMemories handles GET /api/v1/schemas/{name}/memory
func (h *MemoryHandler) ListMemories(w http.ResponseWriter, r *http.Request) {
	schemaID, ok := h.resolveSchemaName(w, r)
	if !ok {
		return
	}

	memories, err := h.lister.Execute(r.Context(), schemaID)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	resp := make([]memoryResponse, 0, len(memories))
	for _, m := range memories {
		resp = append(resp, memoryResponse{
			ID:        m.ID,
			SchemaID:  m.SchemaID,
			UserSub:   m.UserSub,
			Content:   m.Content,
			Metadata:  m.Metadata,
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

// ClearMemories handles DELETE /api/v1/schemas/{name}/memory
func (h *MemoryHandler) ClearMemories(w http.ResponseWriter, r *http.Request) {
	schemaID, ok := h.resolveSchemaName(w, r)
	if !ok {
		return
	}

	if _, err := h.clearer.ClearAll(r.Context(), schemaID); err != nil {
		writeDomainError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// DeleteMemory handles DELETE /api/v1/schemas/{name}/memory/{entry_id}.
// Schema {name} is resolved + validated even though we only delete by entry_id —
// this keeps the audit trail tied to the schema and prevents probing for
// memories without a valid schema handle.
func (h *MemoryHandler) DeleteMemory(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.resolveSchemaName(w, r); !ok {
		return
	}
	entryID := chi.URLParam(r, "entry_id")
	if entryID == "" {
		writeJSONError(w, http.StatusBadRequest, "memory entry id required")
		return
	}

	if err := h.clearer.DeleteOne(r.Context(), entryID); err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
