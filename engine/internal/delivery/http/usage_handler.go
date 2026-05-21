package http

import (
	"log/slog"
	"net/http"

	pluginpkg "github.com/syntheticinc/syntheticbrew/pkg/plugin"
	"gorm.io/gorm"
)

// UsageHandler serves GET /api/v1/usage with usage counters.
//
// CE returns a fixed "Community Edition" plan with raw counts and -1 limits
// (unlimited). Cloud/EE plugins merge billing/quota fields via UsageExtras
// without CE knowing about them.
type UsageHandler struct {
	db     *gorm.DB
	plugin pluginpkg.Plugin
}

func NewUsageHandler(db *gorm.DB, plug pluginpkg.Plugin) *UsageHandler {
	if plug == nil {
		plug = pluginpkg.Noop{}
	}
	return &UsageHandler{db: db, plugin: plug}
}

// GetUsage handles GET /api/v1/usage.
func (h *UsageHandler) GetUsage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var agentCount, schemaCount int64
	if err := h.db.WithContext(ctx).Raw("SELECT COUNT(*) FROM agents").Scan(&agentCount).Error; err != nil {
		slog.ErrorContext(ctx, "usage: failed to count agents", "error", err)
	}
	if err := h.db.WithContext(ctx).Raw("SELECT COUNT(*) FROM schemas").Scan(&schemaCount).Error; err != nil {
		slog.ErrorContext(ctx, "usage: failed to count schemas", "error", err)
	}

	var sessionCount int64
	if err := h.db.WithContext(ctx).Raw("SELECT COUNT(DISTINCT session_id) FROM messages").Scan(&sessionCount).Error; err != nil {
		slog.ErrorContext(ctx, "usage: failed to count sessions", "error", err)
	}

	resp := map[string]any{
		"plan": "Community Edition",
		"metrics": []map[string]any{
			{"name": "agents", "label": "Agents", "used": agentCount, "limit": -1, "unit": ""},
			{"name": "schemas", "label": "Schemas", "used": schemaCount, "limit": -1, "unit": ""},
			{"name": "sessions", "label": "Sessions", "used": sessionCount, "limit": -1, "unit": ""},
		},
	}

	for k, v := range h.plugin.UsageExtras(ctx, "") {
		resp[k] = v
	}

	writeJSON(w, http.StatusOK, resp)
}
