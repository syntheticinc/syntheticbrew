package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm/registry"
)

type mockModelRegistry struct {
	models    []registry.ModelInfo
	providers []registry.ProviderInfo
}

func (m *mockModelRegistry) ListProviders() []registry.ProviderInfo {
	return m.providers
}

func (m *mockModelRegistry) ListModels(filters registry.ModelFilters) []registry.ModelInfo {
	var result []registry.ModelInfo
	for _, model := range m.models {
		if filters.Provider != "" && model.Provider != filters.Provider {
			continue
		}
		if filters.Tier != nil && model.Tier != *filters.Tier {
			continue
		}
		if filters.SupportsTools != nil && model.SupportsTools != *filters.SupportsTools {
			continue
		}
		result = append(result, model)
	}
	return result
}

func (m *mockModelRegistry) GetModel(id string) *registry.ModelInfo {
	for _, model := range m.models {
		if model.ID == id {
			return &model
		}
	}
	return nil
}

func newTestRegistry() *mockModelRegistry {
	return &mockModelRegistry{
		models: []registry.ModelInfo{
			{
				ID:            "model-a",
				DisplayName:   "Model A",
				Provider:      "anthropic",
				Tier:          registry.Tier1,
				ContextWindow: 100_000,
				SupportsTools: true,
			},
			{
				ID:            "model-b",
				DisplayName:   "Model B",
				Provider:      "openai",
				Tier:          registry.Tier2,
				ContextWindow: 50_000,
				SupportsTools: true,
			},
			{
				ID:            "model-c",
				DisplayName:   "Model C",
				Provider:      "openai",
				Tier:          registry.Tier3,
				ContextWindow: 10_000,
				SupportsTools: false,
			},
		},
		providers: []registry.ProviderInfo{
			{ID: "anthropic", DisplayName: "Anthropic", AuthType: "api_key"},
			{ID: "openai", DisplayName: "OpenAI", AuthType: "api_key"},
		},
	}
}

func TestRegistryHandler_List(t *testing.T) {
	handler := NewModelRegistryHandler(newTestRegistry())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/models/registry", nil)
	rec := httptest.NewRecorder()
	handler.List(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var models []registry.ModelInfo
	err := json.NewDecoder(rec.Body).Decode(&models)
	require.NoError(t, err)
	assert.Len(t, models, 3)
}

func TestRegistryHandler_FilterByProvider(t *testing.T) {
	handler := NewModelRegistryHandler(newTestRegistry())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/models/registry?provider=openai", nil)
	rec := httptest.NewRecorder()
	handler.List(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var models []registry.ModelInfo
	err := json.NewDecoder(rec.Body).Decode(&models)
	require.NoError(t, err)
	assert.Len(t, models, 2)
	for _, m := range models {
		assert.Equal(t, "openai", m.Provider)
	}
}

func TestRegistryHandler_FilterByTier(t *testing.T) {
	handler := NewModelRegistryHandler(newTestRegistry())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/models/registry?tier=1", nil)
	rec := httptest.NewRecorder()
	handler.List(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var models []registry.ModelInfo
	err := json.NewDecoder(rec.Body).Decode(&models)
	require.NoError(t, err)
	assert.Len(t, models, 1)
	assert.Equal(t, "model-a", models[0].ID)
}

func TestRegistryHandler_FilterBySupportsTools(t *testing.T) {
	handler := NewModelRegistryHandler(newTestRegistry())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/models/registry?supports_tools=false", nil)
	rec := httptest.NewRecorder()
	handler.List(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var models []registry.ModelInfo
	err := json.NewDecoder(rec.Body).Decode(&models)
	require.NoError(t, err)
	assert.Len(t, models, 1)
	assert.Equal(t, "model-c", models[0].ID)
}

func TestRegistryHandler_InvalidTier(t *testing.T) {
	handler := NewModelRegistryHandler(newTestRegistry())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/models/registry?tier=abc", nil)
	rec := httptest.NewRecorder()
	handler.List(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var errResp map[string]string
	err := json.NewDecoder(rec.Body).Decode(&errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp["error"], "tier must be an integer")
}

func TestRegistryHandler_InvalidSupportsTools(t *testing.T) {
	handler := NewModelRegistryHandler(newTestRegistry())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/models/registry?supports_tools=maybe", nil)
	rec := httptest.NewRecorder()
	handler.List(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var errResp map[string]string
	err := json.NewDecoder(rec.Body).Decode(&errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp["error"], "supports_tools must be a boolean")
}

func TestRegistryHandler_ListProviders(t *testing.T) {
	handler := NewModelRegistryHandler(newTestRegistry())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/models/registry/providers", nil)
	rec := httptest.NewRecorder()
	handler.ListProviders(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var providers []registry.ProviderInfo
	err := json.NewDecoder(rec.Body).Decode(&providers)
	require.NoError(t, err)
	assert.Len(t, providers, 2)
}

func TestRegistryHandler_CombinedFilters(t *testing.T) {
	handler := NewModelRegistryHandler(newTestRegistry())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/models/registry?provider=openai&tier=2", nil)
	rec := httptest.NewRecorder()
	handler.List(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var models []registry.ModelInfo
	err := json.NewDecoder(rec.Body).Decode(&models)
	require.NoError(t, err)
	assert.Len(t, models, 1)
	assert.Equal(t, "model-b", models[0].ID)
}
