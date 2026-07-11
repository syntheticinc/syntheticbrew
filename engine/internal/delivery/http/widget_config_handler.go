package http

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// WidgetConfigPolicyReader reads a single protected per-tenant policy value.
// Defined consumer-side; configrepo.GORMTenantPolicyRepository satisfies it.
type WidgetConfigPolicyReader interface {
	Get(ctx context.Context, key string) (*domain.TenantPolicy, error)
}

// WidgetConfigResponse is the public widget bootstrap config. It carries no
// tenant-identifying data — only render toggles the embedded widget needs.
type WidgetConfigResponse struct {
	Attribution bool `json:"attribution"`
}

// WidgetConfigHandler serves GET /api/v1/widget-config for the embedded
// widget. A nil reader disables attribution (fail-quiet).
type WidgetConfigHandler struct {
	policies WidgetConfigPolicyReader
}

// NewWidgetConfigHandler creates a WidgetConfigHandler. A nil reader is valid:
// attribution then resolves to false.
func NewWidgetConfigHandler(policies WidgetConfigPolicyReader) *WidgetConfigHandler {
	return &WidgetConfigHandler{policies: policies}
}

// Get handles GET /api/v1/widget-config. Reader nil, a read error, or an
// absent policy row all resolve attribution to false (fail-quiet — a config
// hiccup must never break widget bootstrap). The response is privately
// cacheable for 5 minutes.
func (h *WidgetConfigHandler) Get(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, max-age=300")
	writeJSON(w, http.StatusOK, WidgetConfigResponse{Attribution: h.attribution(r.Context())})
}

// attribution resolves the widget_attribution policy to a bool. It is on only
// when the policy value is exactly "on".
func (h *WidgetConfigHandler) attribution(ctx context.Context) bool {
	if h.policies == nil {
		return false
	}
	policy, err := h.policies.Get(ctx, domain.PolicyWidgetAttribution)
	if err != nil {
		slog.DebugContext(ctx, "widget-config attribution read failed — defaulting off", "error", err)
		return false
	}
	if policy == nil {
		return false
	}
	return policy.Value == "on"
}
