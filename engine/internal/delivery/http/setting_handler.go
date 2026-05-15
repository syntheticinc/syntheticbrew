package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// SettingResponse is the API representation of a setting.
type SettingResponse struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedAt string `json:"updated_at"`
}

// UpdateSettingRequest is the body for PUT /api/v1/settings/{key}.
type UpdateSettingRequest struct {
	Value string `json:"value"`
}

// SettingService provides settings CRUD operations.
type SettingService interface {
	ListSettings(ctx context.Context) ([]SettingResponse, error)
	UpdateSetting(ctx context.Context, key, value string) (*SettingResponse, error)
}

// SettingHandler serves /api/v1/settings endpoints.
type SettingHandler struct {
	service SettingService
}

// NewSettingHandler creates a SettingHandler.
func NewSettingHandler(service SettingService) *SettingHandler {
	return &SettingHandler{service: service}
}

// List handles GET /api/v1/settings.
func (h *SettingHandler) List(w http.ResponseWriter, r *http.Request) {
	settings, err := h.service.ListSettings(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// Update handles PUT /api/v1/settings/{key}.
func (h *SettingHandler) Update(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if key == "" {
		writeJSONError(w, http.StatusBadRequest, "setting key is required")
		return
	}

	var req UpdateSettingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}

	setting, err := h.service.UpdateSetting(r.Context(), key, req.Value)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, setting)
}
