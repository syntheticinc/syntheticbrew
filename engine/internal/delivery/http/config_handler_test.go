package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- mocks ---

type mockConfigReloader struct {
	reloadErr   error
	agentsCount int
	reloadCalls int
}

func (m *mockConfigReloader) Reload(_ context.Context) error {
	m.reloadCalls++
	return m.reloadErr
}

func (m *mockConfigReloader) AgentsCount() int {
	return m.agentsCount
}

type mockConfigImportExporter struct {
	importErr    error
	exportData   []byte
	exportErr    error
	importedData []byte
}

func (m *mockConfigImportExporter) ImportYAML(_ context.Context, data []byte) error {
	m.importedData = data
	return m.importErr
}

func (m *mockConfigImportExporter) ExportYAML(_ context.Context) ([]byte, error) {
	return m.exportData, m.exportErr
}

// --- tests ---

func TestConfigHandler_Reload(t *testing.T) {
	tests := []struct {
		name           string
		reloadErr      error
		agentsCount    int
		wantStatus     int
		wantContains   string
	}{
		{
			name:         "success",
			agentsCount:  3,
			wantStatus:   http.StatusOK,
			wantContains: `"reloaded":true`,
		},
		{
			name:         "reload error",
			reloadErr:    errors.New("db connection lost"),
			wantStatus:   http.StatusInternalServerError,
			wantContains: "db connection lost",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reloader := &mockConfigReloader{
				reloadErr:   tt.reloadErr,
				agentsCount: tt.agentsCount,
			}
			h := NewConfigHandler(reloader, &mockConfigImportExporter{})

			req := httptest.NewRequest(http.MethodPost, "/api/v1/config/reload", nil)
			w := httptest.NewRecorder()

			h.Reload(w, req)

			assert.Equal(t, tt.wantStatus, w.Code)
			assert.Contains(t, w.Body.String(), tt.wantContains)
		})
	}
}

func TestConfigHandler_Reload_AgentsCount(t *testing.T) {
	reloader := &mockConfigReloader{agentsCount: 5}
	h := NewConfigHandler(reloader, &mockConfigImportExporter{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/config/reload", nil)
	w := httptest.NewRecorder()

	h.Reload(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"agents_count":5`)
}

func TestConfigHandler_Import(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		importErr    error
		reloadErr    error
		agentsCount  int
		wantStatus   int
		wantContains string
	}{
		{
			name:         "success",
			body:         "agents:\n  - name: test\n",
			agentsCount:  1,
			wantStatus:   http.StatusOK,
			wantContains: `"imported":true`,
		},
		{
			name:         "empty body",
			body:         "",
			wantStatus:   http.StatusBadRequest,
			wantContains: "empty body",
		},
		{
			name:         "import error",
			body:         "invalid: yaml: [",
			importErr:    errors.New("invalid yaml"),
			wantStatus:   http.StatusBadRequest,
			wantContains: "invalid yaml",
		},
		{
			name:         "reload after import fails",
			body:         "agents:\n  - name: test\n",
			reloadErr:    errors.New("reload failed"),
			wantStatus:   http.StatusInternalServerError,
			wantContains: "imported but reload failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reloader := &mockConfigReloader{
				reloadErr:   tt.reloadErr,
				agentsCount: tt.agentsCount,
			}
			ie := &mockConfigImportExporter{importErr: tt.importErr}
			h := NewConfigHandler(reloader, ie)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/config/import", strings.NewReader(tt.body))
			w := httptest.NewRecorder()

			h.Import(w, req)

			assert.Equal(t, tt.wantStatus, w.Code)
			assert.Contains(t, w.Body.String(), tt.wantContains)
		})
	}
}

func TestConfigHandler_Import_PassesBodyToImporter(t *testing.T) {
	ie := &mockConfigImportExporter{}
	h := NewConfigHandler(&mockConfigReloader{}, ie)

	body := "agents:\n  - name: supervisor\n"
	req := httptest.NewRequest(http.MethodPost, "/api/v1/config/import", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.Import(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, body, string(ie.importedData))
}

func TestConfigHandler_Import_ReloadsAfterImport(t *testing.T) {
	reloader := &mockConfigReloader{agentsCount: 2}
	h := NewConfigHandler(reloader, &mockConfigImportExporter{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/config/import", strings.NewReader("data"))
	w := httptest.NewRecorder()

	h.Import(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, reloader.reloadCalls)
}

func TestConfigHandler_Export(t *testing.T) {
	tests := []struct {
		name         string
		exportData   []byte
		exportErr    error
		wantStatus   int
		wantContains string
		wantYAML     bool
	}{
		{
			name:       "success",
			exportData: []byte("agents:\n  - name: test\n"),
			wantStatus: http.StatusOK,
			wantYAML:   true,
		},
		{
			name:         "export error",
			exportErr:    errors.New("db error"),
			wantStatus:   http.StatusInternalServerError,
			wantContains: "db error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ie := &mockConfigImportExporter{
				exportData: tt.exportData,
				exportErr:  tt.exportErr,
			}
			h := NewConfigHandler(&mockConfigReloader{}, ie)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/config/export", nil)
			w := httptest.NewRecorder()

			h.Export(w, req)

			assert.Equal(t, tt.wantStatus, w.Code)
			if tt.wantYAML {
				assert.Equal(t, "application/x-yaml", w.Header().Get("Content-Type"))
				assert.Contains(t, w.Header().Get("Content-Disposition"), "syntheticbrew-config.yaml")
				assert.Equal(t, string(tt.exportData), w.Body.String())
			}
			if tt.wantContains != "" {
				assert.Contains(t, w.Body.String(), tt.wantContains)
			}
		})
	}
}
