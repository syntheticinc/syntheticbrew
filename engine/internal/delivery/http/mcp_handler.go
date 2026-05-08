package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/syntheticinc/bytebrew/engine/internal/service/mcp"
)

// allowedMCPTransports matches target-schema.dbml mcp_servers.type CHECK:
//
//	stdio | http | sse | streamable-http
var allowedMCPTransports = map[string]struct{}{
	"stdio":           {},
	"http":            {},
	"sse":             {},
	"streamable-http": {},
}

// isAllowedMCPTransport reports whether the requested transport value is one
// of the four DBML-permitted values. Anything else (including legacy "docker")
// must be rejected before it reaches the DB.
func isAllowedMCPTransport(t string) bool {
	_, ok := allowedMCPTransports[t]
	return ok
}

// MCPServerResponse is the API representation of an MCP server.
//
// V2 Commit Group C (§5.5, §5.6): `is_well_known` is gone (the catalog lives
// in its own `mcp_catalog` table and installs are independent copies).
// Connection status is omitted from this response — callers that want live
// status query the MCP client registry endpoint instead of reading a
// persisted field.
type MCPServerResponse struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	Command        string            `json:"command,omitempty"`
	Args           []string          `json:"args,omitempty"`
	URL            string            `json:"url,omitempty"`
	EnvVars        map[string]string `json:"env_vars,omitempty"`
	ForwardHeaders []string          `json:"forward_headers,omitempty"`
	AuthType       string            `json:"auth_type,omitempty"`
	AuthKeyEnv     string            `json:"auth_key_env,omitempty"`
	AuthTokenEnv   string            `json:"auth_token_env,omitempty"`
	AuthClientID   string            `json:"auth_client_id,omitempty"`
	Agents         []string          `json:"agents"`
}

// CreateMCPServerRequest is the body for POST /api/v1/mcp-servers.
type CreateMCPServerRequest struct {
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	Command        string            `json:"command,omitempty"`
	Args           []string          `json:"args,omitempty"`
	URL            string            `json:"url,omitempty"`
	EnvVars        map[string]string `json:"env_vars,omitempty"`
	ForwardHeaders []string          `json:"forward_headers,omitempty"`
	AuthType       string            `json:"auth_type,omitempty"`
	AuthKeyEnv     string            `json:"auth_key_env,omitempty"`
	AuthTokenEnv   string            `json:"auth_token_env,omitempty"`
	AuthClientID   string            `json:"auth_client_id,omitempty"`
}

// UpdateMCPServerRequest is the body for PATCH /api/v1/mcp-servers/{name}.
// All fields are pointers: nil means "preserve existing value".
type UpdateMCPServerRequest struct {
	Name           *string            `json:"name,omitempty"`
	Type           *string            `json:"type,omitempty"`
	Command        *string            `json:"command,omitempty"`
	Args           *[]string          `json:"args,omitempty"`
	URL            *string            `json:"url,omitempty"`
	EnvVars        *map[string]string `json:"env_vars,omitempty"`
	ForwardHeaders *[]string          `json:"forward_headers,omitempty"`
	AuthType       *string            `json:"auth_type,omitempty"`
	AuthKeyEnv     *string            `json:"auth_key_env,omitempty"`
	AuthTokenEnv   *string            `json:"auth_token_env,omitempty"`
	AuthClientID   *string            `json:"auth_client_id,omitempty"`
}

// MCPService provides MCP server CRUD operations.
type MCPService interface {
	ListMCPServers(ctx context.Context) ([]MCPServerResponse, error)
	CreateMCPServer(ctx context.Context, req CreateMCPServerRequest) (*MCPServerResponse, error)
	UpdateMCPServer(ctx context.Context, name string, req CreateMCPServerRequest) (*MCPServerResponse, error)
	PatchMCPServer(ctx context.Context, name string, req UpdateMCPServerRequest) (*MCPServerResponse, error)
	DeleteMCPServer(ctx context.Context, name string) error
}

// MCPHandler serves /api/v1/mcp-servers endpoints.
type MCPHandler struct {
	service  MCPService
	policy   mcp.TransportPolicy
}

// NewMCPHandler creates an MCPHandler.
// policy enforces deployment-specific transport restrictions (e.g. blocking
// stdio in Cloud mode). Pass mcp.PermissiveTransportPolicy{} for CE.
func NewMCPHandler(service MCPService, policy mcp.TransportPolicy) *MCPHandler {
	return &MCPHandler{service: service, policy: policy}
}

// Routes returns a chi router with MCP server endpoints mounted.
func (h *MCPHandler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Post("/", h.Create)
	r.Put("/{name}", h.Update)
	r.Patch("/{name}", h.Patch)
	r.Delete("/{name}", h.Delete)
	return r
}

// List handles GET /api/v1/mcp-servers.
func (h *MCPHandler) List(w http.ResponseWriter, r *http.Request) {
	servers, err := h.service.ListMCPServers(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, servers)
}

// Create handles POST /api/v1/mcp-servers.
func (h *MCPHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateMCPServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}
	if req.Name == "" {
		writeJSONError(w, http.StatusBadRequest, "name is required")
		return
	}
	// MCP servers are name-keyed in URLs. Enforce the same DNS-label format
	// the rest of the surface uses so PATCH/DELETE on `{name}` round-trips
	// without depending on `%2F` / `%20` decoding.
	if err := ValidateResourceName(req.Name); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid mcp server name: "+err.Error())
		return
	}
	if req.Type == "" {
		writeJSONError(w, http.StatusBadRequest, "type is required")
		return
	}
	if !isAllowedMCPTransport(req.Type) {
		writeJSONError(w, http.StatusBadRequest, "invalid transport type: must be one of stdio, http, sse, streamable-http")
		return
	}
	if err := h.policy.IsAllowed(req.Type); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	server, err := h.service.CreateMCPServer(r.Context(), req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, server)
}

// Update handles PUT /api/v1/mcp-servers/{name}.
// PUT is a full-replace: type is required; missing required fields return 400.
// Use PATCH for partial updates.
func (h *MCPHandler) Update(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "mcp server name is required")
		return
	}

	var req CreateMCPServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}

	// PUT full-replace: type is required.
	if req.Type == "" {
		writeJSONError(w, http.StatusBadRequest, "type is required for PUT (full replace); use PATCH for partial updates")
		return
	}
	if !isAllowedMCPTransport(req.Type) {
		writeJSONError(w, http.StatusBadRequest, "invalid transport type: must be one of stdio, http, sse, streamable-http")
		return
	}
	if err := h.policy.IsAllowed(req.Type); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := h.service.UpdateMCPServer(r.Context(), name, req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// Patch handles PATCH /api/v1/mcp-servers/{name}.
// Only non-nil fields are applied; all others preserve their current value.
func (h *MCPHandler) Patch(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "mcp server name is required")
		return
	}

	var req UpdateMCPServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}

	// Validate transport type if provided.
	if req.Type != nil {
		if !isAllowedMCPTransport(*req.Type) {
			writeJSONError(w, http.StatusBadRequest, "invalid transport type: must be one of stdio, http, sse, streamable-http")
			return
		}
		if err := h.policy.IsAllowed(*req.Type); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	result, err := h.service.PatchMCPServer(r.Context(), name, req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// Delete handles DELETE /api/v1/mcp-servers/{name}.
func (h *MCPHandler) Delete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "mcp server name is required")
		return
	}

	if err := h.service.DeleteMCPServer(r.Context(), name); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
