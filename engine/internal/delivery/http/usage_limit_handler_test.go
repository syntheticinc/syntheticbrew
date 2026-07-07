package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

type fakeUsageLimitService struct {
	limits   []domain.UsageLimit
	setErr   error
	delErr   error
	lastSet  domain.UsageLimit
	deleted  string
	setCalls int
	delCalls int
}

func (f *fakeUsageLimitService) GetLimits(context.Context) ([]domain.UsageLimit, error) {
	return f.limits, nil
}

func (f *fakeUsageLimitService) SetLimit(_ context.Context, scope, unit string, limitValue, intervalSeconds int64, enabled bool) error {
	f.setCalls++
	f.lastSet = domain.UsageLimit{Scope: scope, Unit: unit, LimitValue: limitValue, IntervalSeconds: intervalSeconds, Enabled: enabled}
	return f.setErr
}

func (f *fakeUsageLimitService) DeleteLimit(_ context.Context, scope string) error {
	f.delCalls++
	f.deleted = scope
	return f.delErr
}

func TestUsageLimitHandler_List(t *testing.T) {
	svc := &fakeUsageLimitService{limits: []domain.UsageLimit{
		{Scope: domain.ScopeTenant, Unit: domain.UnitTurns, LimitValue: 50, IntervalSeconds: 2592000, Enabled: true},
	}}
	h := NewUsageLimitHandler(svc)

	rr := httptest.NewRecorder()
	h.List(rr, httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage-limits", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	var resp []UsageLimitResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp) != 1 || resp[0].Scope != domain.ScopeTenant || resp[0].LimitValue != 50 {
		t.Fatalf("unexpected list: %+v", resp)
	}
}

func TestUsageLimitHandler_Set_HappyAndValidation(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantCode int
		wantSet  bool
	}{
		{"valid", `{"scope":"tenant","unit":"turns","limit_value":50,"interval_seconds":2592000,"enabled":true}`, http.StatusOK, true},
		{"bad scope", `{"scope":"nope","unit":"turns","limit_value":50,"interval_seconds":10}`, http.StatusBadRequest, false},
		{"bad unit", `{"scope":"tenant","unit":"credits","limit_value":50,"interval_seconds":10}`, http.StatusBadRequest, false},
		{"zero limit", `{"scope":"tenant","unit":"turns","limit_value":0,"interval_seconds":10}`, http.StatusBadRequest, false},
		{"malformed json", `{`, http.StatusBadRequest, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeUsageLimitService{}
			h := NewUsageLimitHandler(svc)
			rr := httptest.NewRecorder()
			h.Set(rr, httptest.NewRequest(http.MethodPut, "/api/v1/admin/usage-limits", strings.NewReader(tc.body)))
			if rr.Code != tc.wantCode {
				t.Fatalf("want %d, got %d (body %s)", tc.wantCode, rr.Code, rr.Body.String())
			}
			if (svc.setCalls > 0) != tc.wantSet {
				t.Fatalf("setCalls=%d, wantSet=%v", svc.setCalls, tc.wantSet)
			}
		})
	}
}

func TestUsageLimitHandler_Set_ServiceError500(t *testing.T) {
	svc := &fakeUsageLimitService{setErr: errors.New("db down")}
	h := NewUsageLimitHandler(svc)
	rr := httptest.NewRecorder()
	h.Set(rr, httptest.NewRequest(http.MethodPut, "/api/v1/admin/usage-limits",
		strings.NewReader(`{"scope":"tenant","unit":"turns","limit_value":50,"interval_seconds":10,"enabled":true}`)))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 on service error, got %d", rr.Code)
	}
}

func TestUsageLimitHandler_Delete(t *testing.T) {
	// valid scope
	svc := &fakeUsageLimitService{}
	h := NewUsageLimitHandler(svc)
	rr := httptest.NewRecorder()
	req := withScopeParam(httptest.NewRequest(http.MethodDelete, "/api/v1/admin/usage-limits/per_user", nil), "per_user")
	h.Delete(rr, req)
	if rr.Code != http.StatusOK || svc.deleted != domain.ScopeUser {
		t.Fatalf("want 200 delete per_user, got %d deleted=%q", rr.Code, svc.deleted)
	}

	// invalid scope → 400, no service call
	svc2 := &fakeUsageLimitService{}
	h2 := NewUsageLimitHandler(svc2)
	rr2 := httptest.NewRecorder()
	req2 := withScopeParam(httptest.NewRequest(http.MethodDelete, "/api/v1/admin/usage-limits/bogus", nil), "bogus")
	h2.Delete(rr2, req2)
	if rr2.Code != http.StatusBadRequest || svc2.delCalls != 0 {
		t.Fatalf("want 400 no-call for bad scope, got %d calls=%d", rr2.Code, svc2.delCalls)
	}
}

// withScopeParam injects a chi {scope} URL param into the request context.
func withScopeParam(r *http.Request, scope string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("scope", scope)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}
