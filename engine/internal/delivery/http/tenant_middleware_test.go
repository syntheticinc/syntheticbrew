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

// TestJWTTenantExtractor_IgnoresXTenantIDHeader guards the Fable #3 fix: tenant
// identity comes ONLY from the verified JWT claim (stamped into ctx by the auth
// middleware). A client-controlled X-Tenant-ID header must never set the tenant —
// otherwise a validly-signed but tenant-less credential could target another
// tenant's registry / MCP clients via /config/reload or admin CRUD.
func TestJWTTenantExtractor_IgnoresXTenantIDHeader(t *testing.T) {
	ext := NewJWTTenantExtractor("tenant_id")

	// No verified tenant in ctx + a spoofed header → must resolve to empty
	// (the required middleware then 403s; the header is never trusted).
	req := httptest.NewRequest(http.MethodPost, "/api/v1/config/reload", nil)
	req.Header.Set("X-Tenant-ID", "victim-tenant")
	got, err := ext.ExtractTenantID(req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "" {
		t.Errorf("X-Tenant-ID header must not set the tenant; got %q", got)
	}

	// A verified ctx tenant is returned; the header is ignored even when present.
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/config/reload", nil)
	req2.Header.Set("X-Tenant-ID", "victim-tenant")
	req2 = req2.WithContext(domain.WithTenantID(req2.Context(), "real-tenant"))
	got2, err := ext.ExtractTenantID(req2)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got2 != "real-tenant" {
		t.Errorf("verified ctx tenant must win; got %q want %q", got2, "real-tenant")
	}
}
