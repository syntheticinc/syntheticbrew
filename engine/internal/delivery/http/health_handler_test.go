package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockAgentCounter struct {
	count int
}

func (m *mockAgentCounter) Count() int { return m.count }

type mockUpdateChecker struct {
	version string
}

func (m *mockUpdateChecker) UpdateAvailable() string { return m.version }

type mockPlatformDefaultChecker struct {
	has bool
}

func (m *mockPlatformDefaultChecker) HasPlatformDefault() bool { return m.has }

func TestHealthHandler_ServeHTTP(t *testing.T) {
	tests := []struct {
		name        string
		version     string
		agentsCount int
	}{
		{"basic response", "1.0.0", 3},
		{"zero agents", "2.0.0-beta", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewHealthHandler(tt.version, &mockAgentCounter{count: tt.agentsCount})

			req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

			var resp HealthResponse
			err := json.NewDecoder(rec.Body).Decode(&resp)
			require.NoError(t, err)
			assert.Equal(t, "ok", resp.Status)
			assert.Equal(t, tt.version, resp.Version)
			assert.Equal(t, tt.agentsCount, resp.AgentsCount)
			assert.NotEmpty(t, resp.Uptime)
			assert.Empty(t, resp.UpdateAvailable)
		})
	}
}

func TestHealthHandler_UpdateAvailable(t *testing.T) {
	handler := NewHealthHandler("1.0.0", &mockAgentCounter{count: 2})
	handler.SetUpdateChecker(&mockUpdateChecker{version: "1.0.1"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp HealthResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "1.0.1", resp.UpdateAvailable)
}

func TestHealthHandler_NoUpdateAvailable(t *testing.T) {
	handler := NewHealthHandler("1.0.0", &mockAgentCounter{count: 2})
	handler.SetUpdateChecker(&mockUpdateChecker{version: ""})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp HealthResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Empty(t, resp.UpdateAvailable)
}

func TestHealthHandler_NilUpdateChecker(t *testing.T) {
	handler := NewHealthHandler("1.0.0", &mockAgentCounter{count: 0})
	// Do not set update checker — should not panic

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp HealthResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Empty(t, resp.UpdateAvailable)
}

func TestHealthHandler_PlatformDefaultModel(t *testing.T) {
	tests := []struct {
		name    string
		checker PlatformDefaultChecker
		want    bool
	}{
		{"default installed", &mockPlatformDefaultChecker{has: true}, true},
		{"default absent", &mockPlatformDefaultChecker{has: false}, false},
		{"no checker wired", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewHealthHandler("1.0.0", &mockAgentCounter{count: 1})
			if tt.checker != nil {
				handler.SetPlatformDefaultChecker(tt.checker)
			}

			req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code)

			var resp HealthResponse
			err := json.NewDecoder(rec.Body).Decode(&resp)
			require.NoError(t, err)
			assert.Equal(t, tt.want, resp.PlatformDefaultModel)
		})
	}
}
