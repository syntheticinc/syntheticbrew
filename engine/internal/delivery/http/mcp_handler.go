package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/syntheticinc/bytebrew/engine/internal/service/mcp"
)

// MCP catalog refresh interval bounds — mirrors the chk_mcp_refresh_range
// CHECK on mcp_servers (migration 007). Defence-in-depth: API rejects out-of-
// range values with 400 before the DB CHECK fires with a 500-equivalent.
const (
	mcpRefreshIntervalMinSeconds = 30
	mcpRefreshIntervalMaxSeconds = 86400
)

// validateRefreshInterval returns an error message when interval is non-nil
// and outside the allowed [30, 86400] range. Returns "" when valid (including
// nil = disabled).
func validateRefreshInterval(interval *int) string {
	if interval == nil {
		return ""
	}
	if *interval < mcpRefreshIntervalMinSeconds || *interval > mcpRefreshIntervalMaxSeconds {
		return "catalog_refresh_interval_seconds must be between 30 and 86400, or null"
	}
	return ""
}

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
	// CatalogRefreshIntervalSeconds is the optional periodic tools/list refresh
	// interval in seconds. NULL (omitted) disables refresh; range 30..86400
	// validated dual-side at API + DB CHECK.
	CatalogRefreshIntervalSeconds *int     `json:"catalog_refresh_interval_seconds,omitempty"`
	Agents                        []string `json:"agents"`
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
	// CatalogRefreshIntervalSeconds enables periodic tools/list refresh.
	// nil = disabled; non-nil values must be in [30, 86400].
	CatalogRefreshIntervalSeconds *int `json:"catalog_refresh_interval_seconds,omitempty"`
}

// UpdateMCPServerRequest is the body for PATCH /api/v1/mcp-servers/{name}.
// All fields are pointers: nil means "preserve existing value".
//
// CatalogRefreshIntervalSeconds is doubly-pointered semantics: nil = preserve.
// Use a non-nil pointer to a non-nil int to set a value; PATCH with NULL
// clear-out is currently expressed by sending the field omitted (preserve)
// — explicit NULL clearing requires a follow-up. Validation: when non-nil
// the inner int must be in [30, 86400].
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
	// CatalogRefreshIntervalSeconds: nil = preserve current value; non-nil
	// pointer to int in [30, 86400] sets the new interval.
	CatalogRefreshIntervalSeconds *int `json:"catalog_refresh_interval_seconds,omitempty"`
}

// MCPService provides MCP server CRUD operations.
type MCPService interface {
	ListMCPServers(ctx context.Context) ([]MCPServerResponse, error)
	CreateMCPServer(ctx context.Context, req CreateMCPServerRequest) (*MCPServerResponse, error)
	UpdateMCPServer(ctx context.Context, name string, req CreateMCPServerRequest) (*MCPServerResponse, error)
	PatchMCPServer(ctx context.Context, name string, req UpdateMCPServerRequest) (*MCPServerResponse, error)
	DeleteMCPServer(ctx context.Context, name string) error
	// RefreshMCPServer re-queries tools/list for the named server without
	// reconnecting the transport. Returns the post-refresh tools_count.
	// Returns a NotFound DomainError when the server is not registered in
	// the runtime registry (caller's POV: trigger PATCH/PUT to redial).
	RefreshMCPServer(ctx context.Context, name string) (int, error)
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

// Refresh handles POST /api/v1/mcp-servers/{name}/refresh.
//
// Lightweight on-demand re-fetch of the server's tools/list catalog without
// reconnecting the transport. Used by the Admin SPA "Refresh now" surface so
// operators can pick up downstream rename/add/remove of tools without waiting
// for the optional TTL refresher (or after PATCHing
// catalog_refresh_interval_seconds = NULL).
//
// 200 with {name, tools_count} on success. 404 when the server is not
// registered in the runtime registry — caller should trigger PATCH/PUT or
// /config/reload to redial.
func (h *MCPHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "mcp server name is required")
		return
	}
	count, err := h.service.RefreshMCPServer(r.Context(), name)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":        name,
		"tools_count": count,
	})
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
	if msg := validateRefreshInterval(req.CatalogRefreshIntervalSeconds); msg != "" {
		writeJSONError(w, http.StatusBadRequest, msg)
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
	if msg := validateRefreshInterval(req.CatalogRefreshIntervalSeconds); msg != "" {
		writeJSONError(w, http.StatusBadRequest, msg)
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
	// PATCH: nil pointer = preserve, do not validate. Non-nil must be in range.
	if msg := validateRefreshInterval(req.CatalogRefreshIntervalSeconds); msg != "" {
		writeJSONError(w, http.StatusBadRequest, msg)
		return
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
