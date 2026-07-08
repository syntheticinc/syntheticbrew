package http

import (
	"context"
	"net/http"
)

// UsageStatusMetric is one usage aggregate: how much is used and the
// configured limit. Limit is null when no limit is configured.
type UsageStatusMetric struct {
	Used  int64  `json:"used"`
	Limit *int64 `json:"limit"`
}

// UsageStatusResponse is the API representation of the tenant's usage
// aggregates against their configured limits.
type UsageStatusResponse struct {
	ActiveUsers        UsageStatusMetric `json:"active_users"`
	Schemas            UsageStatusMetric `json:"schemas"`
	KnowledgeDocuments UsageStatusMetric `json:"knowledge_documents"`
	Turns              UsageStatusMetric `json:"turns"`
}

// UsageStatusProvider assembles the usage aggregates for the current tenant.
// Defined consumer-side; the app-layer adapter satisfies it.
type UsageStatusProvider interface {
	UsageStatus(ctx context.Context) (UsageStatusResponse, error)
}

// UsageStatusHandler serves GET /api/v1/usage-status.
type UsageStatusHandler struct {
	provider UsageStatusProvider
}

// NewUsageStatusHandler creates a UsageStatusHandler.
func NewUsageStatusHandler(provider UsageStatusProvider) *UsageStatusHandler {
	return &UsageStatusHandler{provider: provider}
}

// Get handles GET /api/v1/usage-status.
func (h *UsageStatusHandler) Get(w http.ResponseWriter, r *http.Request) {
	status, err := h.provider.UsageStatus(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}
