package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// --- Schema DTOs ---

// SchemaInfo is a summary of a schema returned in list responses.
type SchemaInfo struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Description     string     `json:"description,omitempty"`
	Agents          []string   `json:"agents,omitempty"`
	IsSystem        bool       `json:"is_system,omitempty"`
	EntryAgentName  string     `json:"entry_agent_name,omitempty"`
	AgentsCount     int        `json:"agents_count"`
	ChatEnabled     bool       `json:"chat_enabled"`
	ChatLastFiredAt *time.Time `json:"chat_last_fired_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

// CreateSchemaRequest is the body for POST /api/v1/schemas.
type CreateSchemaRequest struct {
	Name         string  `json:"name"`
	Description  string  `json:"description,omitempty"`
	EntryAgentID *string `json:"entry_agent_id,omitempty"`
	ChatEnabled  *bool   `json:"chat_enabled,omitempty"`
}

// UpdateSchemaRequest is the body for PUT /api/v1/schemas/{id}.
// All fields are pointers so callers can send partial updates — nil fields
// preserve their current value instead of being overwritten with a zero value.
type UpdateSchemaRequest struct {
	Name         *string `json:"name,omitempty"`
	Description  *string `json:"description,omitempty"`
	EntryAgentID *string `json:"entry_agent_id,omitempty"`
	ChatEnabled  *bool   `json:"chat_enabled,omitempty"`
}

// --- AgentRelation DTOs ---

// AgentRelationInfo is an agent_relation returned in API responses.
//
// V2 has a single implicit DELEGATION relationship type (see
// docs/architecture/agent-first-runtime.md §3.1). Optional Config carries
// non-typing routing hints.
// AgentRelationInfo is an agent_relation returned in API responses.
// Q.5: source/target are now agent UUIDs internally but the JSON keys
// remain "source"/"target" for API backward compatibility.
type AgentRelationInfo struct {
	ID            string                 `json:"id"`
	SchemaID      string                 `json:"schema_id"`
	SourceAgentID string                 `json:"source"`
	TargetAgentID string                 `json:"target"`
	Config        map[string]interface{} `json:"config,omitempty"`
}

// CreateAgentRelationRequest is the body for POST /api/v1/schemas/{id}/agent-relations.
type CreateAgentRelationRequest struct {
	Source string                 `json:"source"`
	Target string                 `json:"target"`
	Config map[string]interface{} `json:"config,omitempty"`
}

// --- Service interfaces (consumer-side) ---

// SchemaService provides schema CRUD operations.
//
// V2: schema membership is derived from `agent_relations` (see
// docs/architecture/agent-first-runtime.md §2.1) — there is no separate
// AddSchemaAgent / RemoveSchemaAgent surface. Adding an agent to a schema
// is done by creating a delegation relation through AgentRelationService.
type SchemaService interface {
	ListSchemas(ctx context.Context) ([]SchemaInfo, error)
	GetSchema(ctx context.Context, id string) (*SchemaInfo, error)
	CreateSchema(ctx context.Context, req CreateSchemaRequest) (*SchemaInfo, error)
	UpdateSchema(ctx context.Context, id string, req UpdateSchemaRequest) error
	PatchSchema(ctx context.Context, id string, req UpdateSchemaRequest) error
	DeleteSchema(ctx context.Context, id string) error
	ListSchemaAgents(ctx context.Context, schemaID string) ([]string, error)
}

// AgentRelationService provides agent-relation CRUD operations.
type AgentRelationService interface {
	ListAgentRelations(ctx context.Context, schemaID string) ([]AgentRelationInfo, error)
	GetAgentRelation(ctx context.Context, id string) (*AgentRelationInfo, error)
	CreateAgentRelation(ctx context.Context, schemaID string, req CreateAgentRelationRequest) (*AgentRelationInfo, error)
	UpdateAgentRelation(ctx context.Context, id string, req CreateAgentRelationRequest) error
	DeleteAgentRelation(ctx context.Context, id string) error
}

// AgentSchemaLister provides the ability to list schemas that reference an agent.
type AgentSchemaLister interface {
	ListSchemasForAgent(ctx context.Context, agentName string) ([]string, error)
}

// --- Handler ---

// SchemaHandler serves /api/v1/schemas endpoints.
//
// Engine 1.1.0 made the URL `{name}` segment a stable operator-facing handle
// (was UUID in 1.0.x). The handler resolves the name to a tenant-scoped UUID
// via SchemaNameResolver before invoking the underlying service. Internal
// IDs (relationId, FK references, audit resource_id) remain UUID.
type SchemaHandler struct {
	schemas        SchemaService
	agentRelations AgentRelationService
	resolver       SchemaNameResolver
}

// NewSchemaHandler creates a SchemaHandler.
//
// resolver is the tenant-scoped name → UUID resolver — required to translate
// the URL `{name}` segment into the canonical schema UUID consumed by the
// underlying SchemaService and AgentRelationService.
func NewSchemaHandler(schemas SchemaService, agentRelations AgentRelationService, resolver SchemaNameResolver) *SchemaHandler {
	return &SchemaHandler{schemas: schemas, agentRelations: agentRelations, resolver: resolver}
}

// resolveSchemaName translates the `{name}` URL param into a tenant-scoped
// UUID. On any error it writes the appropriate HTTP response and returns
// ("", false); callers must not write further output when ok == false.
func (h *SchemaHandler) resolveSchemaName(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := chi.URLParam(r, "name")
	id, err := resolveSchemaNameToUUID(r.Context(), h.resolver, name)
	if err != nil {
		writeNameLookupError(r.Context(), w, "schema", name, err)
		return "", false
	}
	return id, true
}

// --- Schema endpoints ---

func (h *SchemaHandler) ListSchemas(w http.ResponseWriter, r *http.Request) {
	schemas, err := h.schemas.ListSchemas(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, schemas)
}

func (h *SchemaHandler) GetSchema(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSchemaName(w, r)
	if !ok {
		return
	}

	schema, err := h.schemas.GetSchema(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, schema)
}

func (h *SchemaHandler) CreateSchema(w http.ResponseWriter, r *http.Request) {
	var req CreateSchemaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}
	if req.Name == "" {
		writeJSONError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := ValidateResourceName(req.Name); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid schema name: "+err.Error())
		return
	}

	schema, err := h.schemas.CreateSchema(r.Context(), req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, schema)
}

// UpdateSchema handles PUT /api/v1/schemas/{name}.
// PUT is a full-replace: name is required; missing required fields return 400.
// Renaming is forbidden — supplying a name that differs from the URL handle
// returns 409 Conflict (immutability gate). Use PATCH for partial updates.
func (h *SchemaHandler) UpdateSchema(w http.ResponseWriter, r *http.Request) {
	currentName := chi.URLParam(r, "name")
	id, ok := h.resolveSchemaName(w, r)
	if !ok {
		return
	}

	var req UpdateSchemaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}

	// PUT full-replace: name is required.
	if req.Name == nil || *req.Name == "" {
		writeJSONError(w, http.StatusBadRequest, "name is required for PUT (full replace); use PATCH for partial updates")
		return
	}

	// Names are immutable post-create — rejecting the rename here keeps audit
	// resource_id (UUID) stable and prevents GitOps consumers' stable-handle
	// references from silently breaking. Operators recreate + migrate.
	if *req.Name != currentName {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "name is immutable; recreate with new name and migrate consumers",
		})
		return
	}

	if err := h.schemas.UpdateSchema(r.Context(), id, req); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PatchSchema handles PATCH /api/v1/schemas/{name}.
// Only non-nil fields are applied; all others preserve their current value.
// Supplying `name` is allowed only when it equals the current URL handle —
// any rename returns 409 Conflict (immutability gate).
func (h *SchemaHandler) PatchSchema(w http.ResponseWriter, r *http.Request) {
	currentName := chi.URLParam(r, "name")
	id, ok := h.resolveSchemaName(w, r)
	if !ok {
		return
	}

	var req UpdateSchemaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}

	if req.Name != nil && *req.Name != currentName {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "name is immutable; recreate with new name and migrate consumers",
		})
		return
	}

	if err := h.schemas.PatchSchema(r.Context(), id, req); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *SchemaHandler) DeleteSchema(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSchemaName(w, r)
	if !ok {
		return
	}

	if err := h.schemas.DeleteSchema(r.Context(), id); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Schema-Agent ref endpoints ---

func (h *SchemaHandler) ListSchemaAgents(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSchemaName(w, r)
	if !ok {
		return
	}

	agents, err := h.schemas.ListSchemaAgents(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agents)
}

// --- AgentRelation endpoints ---

func (h *SchemaHandler) ListAgentRelations(w http.ResponseWriter, r *http.Request) {
	schemaID, ok := h.resolveSchemaName(w, r)
	if !ok {
		return
	}

	rels, err := h.agentRelations.ListAgentRelations(r.Context(), schemaID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rels)
}

func (h *SchemaHandler) GetAgentRelation(w http.ResponseWriter, r *http.Request) {
	relationID, err := parseStringParam(r, "relationId")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	rel, err := h.agentRelations.GetAgentRelation(r.Context(), relationID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rel)
}

func (h *SchemaHandler) CreateAgentRelation(w http.ResponseWriter, r *http.Request) {
	schemaID, ok := h.resolveSchemaName(w, r)
	if !ok {
		return
	}

	var req CreateAgentRelationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}
	if req.Source == "" || req.Target == "" {
		writeJSONError(w, http.StatusBadRequest, "source and target are required")
		return
	}

	rel, err := h.agentRelations.CreateAgentRelation(r.Context(), schemaID, req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rel)
}

func (h *SchemaHandler) UpdateAgentRelation(w http.ResponseWriter, r *http.Request) {
	relationID, err := parseStringParam(r, "relationId")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req CreateAgentRelationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}

	if err := h.agentRelations.UpdateAgentRelation(r.Context(), relationID, req); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *SchemaHandler) DeleteAgentRelation(w http.ResponseWriter, r *http.Request) {
	relationID, err := parseStringParam(r, "relationId")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.agentRelations.DeleteAgentRelation(r.Context(), relationID); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Helpers ---
// parseStringParam and parseStringIDParam are defined in task_handler.go
