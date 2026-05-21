package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

type mockCatalogProvider struct {
	entries []domain.MCPCatalogEntry
}

func (m *mockCatalogProvider) List() []domain.MCPCatalogEntry {
	return m.entries
}

func (m *mockCatalogProvider) ListByCategory(cat domain.MCPCatalogCategory) []domain.MCPCatalogEntry {
	var result []domain.MCPCatalogEntry
	for _, e := range m.entries {
		if e.Category == cat {
			result = append(result, e)
		}
	}
	return result
}

func (m *mockCatalogProvider) Search(query string) []domain.MCPCatalogEntry {
	return m.entries // simplified for test
}

func (m *mockCatalogProvider) Version() string { return "1.0" }

func setupCatalogRouter(provider CatalogProvider) *chi.Mux {
	handler := NewCatalogHandler(provider)
	r := chi.NewRouter()
	r.Get("/api/v1/mcp/catalog", handler.ListCatalog)
	return r
}

func TestCatalogHandler_ListAll(t *testing.T) {
	provider := &mockCatalogProvider{
		entries: []domain.MCPCatalogEntry{
			{Name: "tavily", Display: "Tavily", Category: "search", Verified: true},
			{Name: "github", Display: "GitHub", Category: "dev-tools", Verified: true},
		},
	}
	r := setupCatalogRouter(provider)

	req := httptest.NewRequest("GET", "/api/v1/mcp/catalog", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp catalogListResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "1.0", resp.Version)
	assert.Len(t, resp.Servers, 2)
}

func TestCatalogHandler_FilterByCategory(t *testing.T) {
	provider := &mockCatalogProvider{
		entries: []domain.MCPCatalogEntry{
			{Name: "tavily", Category: "search"},
			{Name: "github", Category: "dev-tools"},
		},
	}
	r := setupCatalogRouter(provider)

	req := httptest.NewRequest("GET", "/api/v1/mcp/catalog?category=search", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp catalogListResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Len(t, resp.Servers, 1)
	assert.Equal(t, "tavily", resp.Servers[0].Name)
}

func TestCatalogHandler_Search(t *testing.T) {
	provider := &mockCatalogProvider{
		entries: []domain.MCPCatalogEntry{
			{Name: "tavily", Display: "Tavily"},
		},
	}
	r := setupCatalogRouter(provider)

	req := httptest.NewRequest("GET", "/api/v1/mcp/catalog?q=tavily", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp catalogListResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Len(t, resp.Servers, 1)
}

func TestCatalogHandler_EmptyCatalog(t *testing.T) {
	provider := &mockCatalogProvider{entries: nil}
	r := setupCatalogRouter(provider)

	req := httptest.NewRequest("GET", "/api/v1/mcp/catalog", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp catalogListResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Len(t, resp.Servers, 0)
}
