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

// CapabilityInfo is a capability returned in API responses.
type CapabilityInfo struct {
	ID      string                 `json:"id"`
	Type    string                 `json:"type"`
	Config  map[string]interface{} `json:"config,omitempty"`
	Enabled bool                   `json:"enabled"`
}

// CreateCapabilityRequest is the body for POST /api/v1/agents/{name}/capabilities.
type CreateCapabilityRequest struct {
	Type    string                 `json:"type"`
	Config  map[string]interface{} `json:"config,omitempty"`
	Enabled *bool                  `json:"enabled,omitempty"` // pointer to distinguish absent from false
}

// UpdateCapabilityRequest is the body for PUT /api/v1/agents/{name}/capabilities/{id}.
type UpdateCapabilityRequest struct {
	Type    string                 `json:"type,omitempty"`
	Config  map[string]interface{} `json:"config,omitempty"`
	Enabled *bool                  `json:"enabled,omitempty"`
}

// CapabilityService provides capability CRUD for an agent.
type CapabilityService interface {
	ListCapabilities(ctx context.Context, agentName string) ([]CapabilityInfo, error)
	AddCapability(ctx context.Context, agentName string, req CreateCapabilityRequest) (*CapabilityInfo, error)
	UpdateCapability(ctx context.Context, id string, req UpdateCapabilityRequest) error
	RemoveCapability(ctx context.Context, id string) error
}

// CapabilityHandler serves /api/v1/agents/{name}/capabilities endpoints.
type CapabilityHandler struct {
	service CapabilityService
}

// NewCapabilityHandler creates a CapabilityHandler.
func NewCapabilityHandler(service CapabilityService) *CapabilityHandler {
	return &CapabilityHandler{service: service}
}

// List handles GET /api/v1/agents/{name}/capabilities.
func (h *CapabilityHandler) List(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "agent name is required")
		return
	}

	caps, err := h.service.ListCapabilities(r.Context(), name)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, caps)
}

// Add handles POST /api/v1/agents/{name}/capabilities.
func (h *CapabilityHandler) Add(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "agent name is required")
		return
	}

	var req CreateCapabilityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}
	if req.Type == "" {
		writeJSONError(w, http.StatusBadRequest, "type is required")
		return
	}
	// Validate capability type against the canonical allowlist. The list
	// lives in domain.AllCapabilityTypes so adding a new capability is a
	// one-file change — handler just mirrors that list.
	if !domain.CapabilityType(req.Type).IsValid() {
		allowed := make([]string, 0, len(domain.AllCapabilityTypes()))
		for _, t := range domain.AllCapabilityTypes() {
			allowed = append(allowed, string(t))
		}
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid capability type %q: must be one of %s",
			req.Type, strings.Join(allowed, ", ")))
		return
	}

	// BUG-013: Reject duplicate capability type for the same agent.
	existing, err := h.service.ListCapabilities(r.Context(), name)
	if err == nil {
		for _, c := range existing {
			if c.Type == req.Type {
				writeJSONError(w, http.StatusConflict, fmt.Sprintf("capability type %q already exists for this agent", req.Type))
				return
			}
		}
	}

	cap, err := h.service.AddCapability(r.Context(), name, req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, cap)
}

// Update handles PUT /api/v1/agents/{name}/capabilities/{id}.
func (h *CapabilityHandler) Update(w http.ResponseWriter, r *http.Request) {
	capID, err := parseStringParam(r, "capId")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req UpdateCapabilityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}

	if err := h.service.UpdateCapability(r.Context(), capID, req); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Remove handles DELETE /api/v1/agents/{name}/capabilities/{id}.
func (h *CapabilityHandler) Remove(w http.ResponseWriter, r *http.Request) {
	capID, err := parseStringParam(r, "capId")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.service.RemoveCapability(r.Context(), capID); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
