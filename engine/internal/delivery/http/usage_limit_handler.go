package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// UsageLimitService manages operator-declared usage limits. Defined
// consumer-side; the concrete *usagelimit.Enforcer satisfies it.
type UsageLimitService interface {
	GetLimits(ctx context.Context) ([]domain.UsageLimit, error)
	SetLimit(ctx context.Context, scope, unit string, limitValue, intervalSeconds int64, enabled bool) error
	DeleteLimit(ctx context.Context, scope string) error
}

// UsageLimitResponse is the API representation of one configured usage limit.
type UsageLimitResponse struct {
	Scope           string `json:"scope"`
	Unit            string `json:"unit"`
	LimitValue      int64  `json:"limit_value"`
	IntervalSeconds int64  `json:"interval_seconds"`
	Enabled         bool   `json:"enabled"`
}

// SetUsageLimitRequest is the body for PUT /api/v1/admin/usage-limits.
type SetUsageLimitRequest struct {
	Scope           string `json:"scope"`
	Unit            string `json:"unit"`
	LimitValue      int64  `json:"limit_value"`
	IntervalSeconds int64  `json:"interval_seconds"`
	Enabled         bool   `json:"enabled"`
}

// UsageLimitHandler serves the admin usage-limit configuration endpoints.
type UsageLimitHandler struct {
	service UsageLimitService
}

// NewUsageLimitHandler creates a UsageLimitHandler.
func NewUsageLimitHandler(service UsageLimitService) *UsageLimitHandler {
	return &UsageLimitHandler{service: service}
}

// List handles GET /api/v1/admin/usage-limits.
func (h *UsageLimitHandler) List(w http.ResponseWriter, r *http.Request) {
	limits, err := h.service.GetLimits(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	resp := make([]UsageLimitResponse, 0, len(limits))
	for _, l := range limits {
		resp = append(resp, UsageLimitResponse{
			Scope:           l.Scope,
			Unit:            l.Unit,
			LimitValue:      l.LimitValue,
			IntervalSeconds: l.IntervalSeconds,
			Enabled:         l.Enabled,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// Set handles PUT /api/v1/admin/usage-limits. Bad scope/unit/limit → 400.
func (h *UsageLimitHandler) Set(w http.ResponseWriter, r *http.Request) {
	var req SetUsageLimitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDomainError(w, pkgerrors.InvalidInput(fmt.Sprintf("invalid request body: %s", err.Error())))
		return
	}

	// Validate at the boundary so malformed input is a 400, not the 500 the
	// usecase's plain validation error would otherwise map to.
	limit := domain.UsageLimit{
		Scope:           req.Scope,
		Unit:            req.Unit,
		LimitValue:      req.LimitValue,
		IntervalSeconds: req.IntervalSeconds,
		Enabled:         req.Enabled,
	}
	if err := limit.Validate(); err != nil {
		writeDomainError(w, pkgerrors.InvalidInput(err.Error()))
		return
	}

	if err := h.service.SetLimit(r.Context(), req.Scope, req.Unit, req.LimitValue, req.IntervalSeconds, req.Enabled); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, UsageLimitResponse(req))
}

// Delete handles DELETE /api/v1/admin/usage-limits/{scope}. Bad scope → 400.
func (h *UsageLimitHandler) Delete(w http.ResponseWriter, r *http.Request) {
	scope := chi.URLParam(r, "scope")
	if scope != domain.ScopeTenant && scope != domain.ScopeUser {
		writeDomainError(w, pkgerrors.InvalidInput(fmt.Sprintf("scope must be %q or %q, got %q", domain.ScopeTenant, domain.ScopeUser, scope)))
		return
	}
	if err := h.service.DeleteLimit(r.Context(), scope); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "scope": scope})
}
