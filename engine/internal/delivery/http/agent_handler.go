package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

const agentNameMaxLen = 255

// AgentInfo is a summary of an agent returned in list responses.
type AgentInfo struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	ToolsCount     int      `json:"tools_count"`
	HasKnowledge   bool     `json:"has_knowledge"`
	IsSystem       bool     `json:"is_system,omitempty"`
	UsedInSchemas  []string `json:"used_in_schemas,omitempty"`
}

// AgentDetail is the full agent information returned by the detail endpoint.
type AgentDetail struct {
	AgentInfo
	ModelID        *string          `json:"model_id,omitempty"`
	SystemPrompt   string           `json:"system_prompt"`
	Tools          []string         `json:"tools"`
	CanSpawn       []string         `json:"can_spawn,omitempty"`
	Lifecycle      string           `json:"lifecycle"`
	ToolExecution  string           `json:"tool_execution"`
	MaxSteps        int              `json:"max_steps"`
	MaxContextSize  int              `json:"max_context_size"`
	MaxTurnDuration int              `json:"max_turn_duration"`
	Temperature     *float64         `json:"temperature,omitempty"`
	TopP            *float64         `json:"top_p,omitempty"`
	MaxTokens       *int             `json:"max_tokens,omitempty"`
	StopSequences   []string         `json:"stop_sequences,omitempty"`
	ConfirmBefore []string `json:"confirm_before,omitempty"`
	MCPServers    []string `json:"mcp_servers,omitempty"`
}

// CreateAgentRequest is the body for POST /api/v1/agents.
// Accepts both "system_prompt" and "system" for the system prompt field.
type CreateAgentRequest struct {
	Name           string           `json:"name"`
	Model          string           `json:"model,omitempty"`
	ModelID        *string          `json:"model_id,omitempty"`
	SystemPrompt   string           `json:"system_prompt"`
	Lifecycle      string           `json:"lifecycle,omitempty"`
	ToolExecution  string           `json:"tool_execution,omitempty"`
	MaxSteps        int              `json:"max_steps,omitempty"`
	MaxContextSize  int              `json:"max_context_size,omitempty"`
	MaxTurnDuration int              `json:"max_turn_duration,omitempty"`
	Temperature     *float64         `json:"temperature,omitempty"`
	TopP            *float64         `json:"top_p,omitempty"`
	MaxTokens       *int             `json:"max_tokens,omitempty"`
	StopSequences   []string         `json:"stop_sequences,omitempty"`
	ConfirmBefore   []string         `json:"confirm_before,omitempty"`
	Tools      []string `json:"tools,omitempty"`
	CanSpawn   []string `json:"can_spawn,omitempty"`
	MCPServers []string `json:"mcp_servers,omitempty"`
	// KnowledgeBaseIDs is the set of KB UUIDs linked to this agent via the
	// knowledge_base_agents M2M. A nil slice means "do not change" on
	// update; an empty slice means "unlink all". Bug 7: the field was
	// silently accepted and ignored — now the upsert is wired through
	// the agent manager adapter.
	KnowledgeBaseIDs []string `json:"knowledge_base_ids,omitempty"`
}

// UnmarshalJSON supports "system" as an alias for "system_prompt" in the JSON body.
func (r *CreateAgentRequest) UnmarshalJSON(data []byte) error {
	// Use a shadow type to prevent infinite recursion.
	type createAgentAlias CreateAgentRequest
	var alias createAgentAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}

	// Check for "system" alias if system_prompt is empty.
	if alias.SystemPrompt == "" {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err == nil {
			if val, ok := raw["system"]; ok {
				var s string
				if json.Unmarshal(val, &s) == nil {
					alias.SystemPrompt = s
				}
			}
		}
	}

	*r = CreateAgentRequest(alias)
	return nil
}

// UpdateAgentRequest is the body for PATCH /api/v1/agents/{name}.
// All fields are pointers: nil means "preserve existing value"; non-nil means "apply this value".
// This prevents the PUT-wipe bug (BUG-MT-03) where unspecified fields were zeroed.
type UpdateAgentRequest struct {
	SystemPrompt    *string   `json:"system_prompt,omitempty"`
	ModelID         *string   `json:"model_id,omitempty"` // accepts UUID or name
	Lifecycle       *string   `json:"lifecycle,omitempty"`
	ToolExecution   *string   `json:"tool_execution,omitempty"`
	MaxSteps        *int      `json:"max_steps,omitempty"`
	MaxContextSize  *int      `json:"max_context_size,omitempty"`
	MaxTurnDuration *int      `json:"max_turn_duration,omitempty"`
	Temperature     *float64  `json:"temperature,omitempty"`
	TopP            *float64  `json:"top_p,omitempty"`
	MaxTokens       *int      `json:"max_tokens,omitempty"`
	StopSequences   *[]string `json:"stop_sequences,omitempty"`
	ConfirmBefore   *[]string `json:"confirm_before,omitempty"`
	Tools           *[]string `json:"tools,omitempty"`
	CanSpawn        *[]string `json:"can_spawn,omitempty"`
	MCPServers      *[]string `json:"mcp_servers,omitempty"`
	// KnowledgeBaseIDs, when non-nil, replaces the KB membership for the
	// agent (empty slice = unlink all). Bug 7: silent-accept before.
	KnowledgeBaseIDs *[]string `json:"knowledge_base_ids,omitempty"`
}

// AgentLister provides agent listing and detail retrieval.
type AgentLister interface {
	ListAgents(ctx context.Context) ([]AgentInfo, error)
	GetAgent(ctx context.Context, name string) (*AgentDetail, error)
}

// AgentManager extends AgentLister with create, update, and delete operations.
type AgentManager interface {
	AgentLister
	CreateAgent(ctx context.Context, req CreateAgentRequest) (*AgentDetail, error)
	UpdateAgent(ctx context.Context, name string, req CreateAgentRequest) (*AgentDetail, error)
	PatchAgent(ctx context.Context, name string, req UpdateAgentRequest) (*AgentDetail, error)
	DeleteAgent(ctx context.Context, name string) error
}

// AgentHandler serves /api/v1/agents endpoints.
type AgentHandler struct {
	lister  AgentLister
	manager AgentManager // may be nil if only read-only mode
}

// NewAgentHandler creates an AgentHandler (read-only).
func NewAgentHandler(lister AgentLister) *AgentHandler {
	return &AgentHandler{lister: lister}
}

// NewAgentHandlerWithManager creates an AgentHandler with full CRUD support.
func NewAgentHandlerWithManager(manager AgentManager) *AgentHandler {
	return &AgentHandler{lister: manager, manager: manager}
}

// List handles GET /api/v1/agents.
func (h *AgentHandler) List(w http.ResponseWriter, r *http.Request) {
	agents, err := h.lister.ListAgents(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, agents)
}

// Get handles GET /api/v1/agents/{name}.
func (h *AgentHandler) Get(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "agent name is required")
		return
	}

	agent, err := h.lister.GetAgent(r.Context(), name)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if agent == nil {
		writeJSONError(w, http.StatusNotFound, "agent not found: "+name)
		return
	}

	writeJSON(w, http.StatusOK, agent)
}

// Create handles POST /api/v1/agents.
func (h *AgentHandler) Create(w http.ResponseWriter, r *http.Request) {
	if h.manager == nil {
		writeJSONError(w, http.StatusNotImplemented, "agent creation not supported")
		return
	}

	var req CreateAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}
	if req.Name == "" {
		writeJSONError(w, http.StatusBadRequest, "name is required")
		return
	}
	// Agents are name-keyed in URLs (`/api/v1/agents/{name}/...`). Enforce
	// the same DNS-label format the schemas/KBs/models layer uses so PATCH/
	// DELETE round-trip the name without depending on `%2F` / `%20` decoding.
	if err := ValidateResourceName(req.Name); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid agent name: "+err.Error())
		return
	}
	if err := validateAgentName(req.Name); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Reject admin_* tools — reserved for system agents only.
	for _, toolName := range req.Tools {
		if strings.HasPrefix(toolName, "admin_") {
			writeJSONError(w, http.StatusBadRequest, "admin tools are reserved for system agents")
			return
		}
	}

	agent, err := h.manager.CreateAgent(r.Context(), req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, agent)
}

// Update handles PUT /api/v1/agents/{name}.
// PUT is a full-replace: the request body MUST include all required fields
// (system_prompt is required; missing required fields return 400).
// Use PATCH for partial updates.
func (h *AgentHandler) Update(w http.ResponseWriter, r *http.Request) {
	if h.manager == nil {
		writeJSONError(w, http.StatusNotImplemented, "agent update not supported")
		return
	}

	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "agent name is required")
		return
	}

	var req CreateAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}

	// PUT full-replace: system_prompt is required.
	if req.SystemPrompt == "" {
		writeJSONError(w, http.StatusBadRequest, "system_prompt is required for PUT (full replace); use PATCH for partial updates")
		return
	}

	// Ensure name from URL is used (body may omit it).
	if req.Name == "" {
		req.Name = name
	}

	// Reject admin_* tools — reserved for system agents only.
	for _, toolName := range req.Tools {
		if strings.HasPrefix(toolName, "admin_") {
			writeJSONError(w, http.StatusBadRequest, "admin tools are reserved for system agents")
			return
		}
	}

	agent, err := h.manager.UpdateAgent(r.Context(), name, req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

// Patch handles PATCH /api/v1/agents/{name}.
// Only non-nil fields in the request body are applied; all others preserve their current value.
// This fixes BUG-MT-03 where PUT with a partial body wiped unspecified fields.
func (h *AgentHandler) Patch(w http.ResponseWriter, r *http.Request) {
	if h.manager == nil {
		writeJSONError(w, http.StatusNotImplemented, "agent update not supported")
		return
	}

	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "agent name is required")
		return
	}

	var req UpdateAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}

	// Reject admin_* tools — reserved for system agents only.
	if req.Tools != nil {
		for _, toolName := range *req.Tools {
			if strings.HasPrefix(toolName, "admin_") {
				writeJSONError(w, http.StatusBadRequest, "admin tools are reserved for system agents")
				return
			}
		}
	}

	agent, err := h.manager.PatchAgent(r.Context(), name, req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

// Delete handles DELETE /api/v1/agents/{name}.
func (h *AgentHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if h.manager == nil {
		writeJSONError(w, http.StatusNotImplemented, "agent deletion not supported")
		return
	}

	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "agent name is required")
		return
	}

	if err := h.manager.DeleteAgent(r.Context(), name); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// validateAgentName checks that the agent name matches the allowed pattern.
func validateAgentName(name string) error {
	if len(name) > agentNameMaxLen {
		return fmt.Errorf("agent name must be at most %d characters", agentNameMaxLen)
	}
	return domain.ValidateAgentName(name)
}
