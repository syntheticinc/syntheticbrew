package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

type mockTenantExtractor struct {
	tenantID string
	err      error
}

func (m *mockTenantExtractor) ExtractTenantID(_ *http.Request) (string, error) {
	return m.tenantID, m.err
}

func TestTenantMiddleware_SetsTenantInContext(t *testing.T) {
	extractor := &mockTenantExtractor{tenantID: "tenant-123"}
	mw := NewTenantMiddleware(extractor, false)

	var capturedTenantID string
	handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedTenantID = domain.TenantIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if capturedTenantID != "tenant-123" {
		t.Errorf("expected tenant %q, got %q", "tenant-123", capturedTenantID)
	}
}

func TestTenantMiddleware_Required_RejectEmpty(t *testing.T) {
	extractor := &mockTenantExtractor{tenantID: ""}
	mw := NewTenantMiddleware(extractor, true) // required

	handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestTenantMiddleware_NotRequired_AllowEmpty(t *testing.T) {
	extractor := &mockTenantExtractor{tenantID: ""}
	mw := NewTenantMiddleware(extractor, false) // not required (CE mode)

	var called bool
	handler := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		tenantID := domain.TenantIDFromContext(r.Context())
		if tenantID != "" {
			t.Errorf("expected empty tenant, got %q", tenantID)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("handler should be called in CE mode")
	}
}
