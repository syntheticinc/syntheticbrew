package http

import (
	"context"
	"io"
	"log/slog"
	"net/http"
)

// ConfigReloader reloads agent configuration from the database.
type ConfigReloader interface {
	Reload(ctx context.Context) error
	AgentsCount() int
}

// ConfigImportExporter handles YAML config import and export.
type ConfigImportExporter interface {
	ImportYAML(ctx context.Context, yamlData []byte) error
	ExportYAML(ctx context.Context) ([]byte, error)
}

// ConfigHandler handles configuration management endpoints.
type ConfigHandler struct {
	reloader       ConfigReloader
	importExporter ConfigImportExporter
}

// NewConfigHandler creates a new ConfigHandler.
func NewConfigHandler(reloader ConfigReloader, importExporter ConfigImportExporter) *ConfigHandler {
	return &ConfigHandler{
		reloader:       reloader,
		importExporter: importExporter,
	}
}

// Reload handles POST /api/v1/config/reload.
func (h *ConfigHandler) Reload(w http.ResponseWriter, r *http.Request) {
	if err := h.reloader.Reload(r.Context()); err != nil {
		slog.ErrorContext(r.Context(), "config reload failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"reloaded":     true,
		"agents_count": h.reloader.AgentsCount(),
	})
}

// Import handles POST /api/v1/config/import.
func (h *ConfigHandler) Import(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB limit
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body: " + err.Error()})
		return
	}
	defer r.Body.Close()

	if len(body) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty body"})
		return
	}

	if err := h.importExporter.ImportYAML(r.Context(), body); err != nil {
		slog.ErrorContext(r.Context(), "config import failed", "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if err := h.reloader.Reload(r.Context()); err != nil {
		slog.ErrorContext(r.Context(), "config reload after import failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "imported but reload failed: " + err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"imported":     true,
		"agents_count": h.reloader.AgentsCount(),
	})
}

// Export handles GET /api/v1/config/export.
func (h *ConfigHandler) Export(w http.ResponseWriter, r *http.Request) {
	data, err := h.importExporter.ExportYAML(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "config export failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Content-Disposition", "attachment; filename=syntheticbrew-config.yaml")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		slog.ErrorContext(r.Context(), "write export response failed", "error", err)
	}
}
