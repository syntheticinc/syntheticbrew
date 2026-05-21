package http

import (
	"net/http"
	"strconv"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm/registry"
)

// ModelRegistry provides read-only access to the built-in model catalog.
type ModelRegistry interface {
	ListProviders() []registry.ProviderInfo
	ListModels(filters registry.ModelFilters) []registry.ModelInfo
	GetModel(id string) *registry.ModelInfo
}

// ModelRegistryHandler serves the model registry endpoints.
type ModelRegistryHandler struct {
	registry ModelRegistry
}

// NewModelRegistryHandler creates a ModelRegistryHandler.
func NewModelRegistryHandler(reg ModelRegistry) *ModelRegistryHandler {
	return &ModelRegistryHandler{registry: reg}
}

// List handles GET /api/v1/models/registry.
// Query params: provider (string), tier (int), supports_tools (bool).
func (h *ModelRegistryHandler) List(w http.ResponseWriter, r *http.Request) {
	filters := registry.ModelFilters{}

	if provider := r.URL.Query().Get("provider"); provider != "" {
		filters.Provider = provider
	}

	if tierStr := r.URL.Query().Get("tier"); tierStr != "" {
		tierInt, err := strconv.Atoi(tierStr)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "tier must be an integer (1, 2, or 3)")
			return
		}
		tier := registry.ModelTier(tierInt)
		filters.Tier = &tier
	}

	if toolsStr := r.URL.Query().Get("supports_tools"); toolsStr != "" {
		tools, err := strconv.ParseBool(toolsStr)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "supports_tools must be a boolean")
			return
		}
		filters.SupportsTools = &tools
	}

	models := h.registry.ListModels(filters)
	writeJSON(w, http.StatusOK, models)
}

// ListProviders handles GET /api/v1/models/registry/providers.
func (h *ModelRegistryHandler) ListProviders(w http.ResponseWriter, r *http.Request) {
	providers := h.registry.ListProviders()
	writeJSON(w, http.StatusOK, providers)
}
