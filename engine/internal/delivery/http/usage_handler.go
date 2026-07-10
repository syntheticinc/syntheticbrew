package http

import (
	"log/slog"
	"net/http"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	pluginpkg "github.com/syntheticinc/syntheticbrew/pkg/plugin"
	"gorm.io/gorm"
)

// UsageHandler serves GET /api/v1/usage with usage counters.
//
// The engine returns a fixed "Community Edition" plan with raw counts and -1
// limits (unlimited). A plugin may merge additional fields into the response
// via the UsageExtras extension point without the engine knowing about them.
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
	// CE single-tenant mode carries an empty tenant claim; resolve it to the
	// sentinel so self-hosted counts match the rows written under CETenantID.
	// A multi-tenant deployment always carries a real tenant UUID (empty is
	// rejected upstream by the required tenant middleware). Mirrors configrepo's
	// tenantIDFromCtx.
	tenantID := domain.TenantIDFromContext(ctx)
	if tenantID == "" {
		tenantID = domain.CETenantID
	}

	var agentCount, schemaCount int64
	if err := h.db.WithContext(ctx).Raw("SELECT COUNT(*) FROM agents WHERE tenant_id = ?", tenantID).Scan(&agentCount).Error; err != nil {
		slog.ErrorContext(ctx, "usage: failed to count agents", "error", err)
	}
	if err := h.db.WithContext(ctx).Raw("SELECT COUNT(*) FROM schemas WHERE tenant_id = ?", tenantID).Scan(&schemaCount).Error; err != nil {
		slog.ErrorContext(ctx, "usage: failed to count schemas", "error", err)
	}

	var sessionCount int64
	if err := h.db.WithContext(ctx).Raw("SELECT COUNT(DISTINCT session_id) FROM messages WHERE tenant_id = ?", tenantID).Scan(&sessionCount).Error; err != nil {
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

	for k, v := range h.plugin.UsageExtras(ctx, tenantID) {
		resp[k] = v
	}

	writeJSON(w, http.StatusOK, resp)
}
