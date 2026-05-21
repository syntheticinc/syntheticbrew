package http_test

import (
	"context"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	eehttp "github.com/syntheticinc/syntheticbrew/internal/delivery/http"
)

// fakeLookup is a test double for WidgetEmbedOriginsLookup.
type fakeLookup struct {
	origins map[string][]string
}

func (f *fakeLookup) GetWidgetEmbedOrigins(_ context.Context, tenantID string) []string {
	return f.origins[tenantID]
}

// ---------------------------------------------------------------------------
// SecurityHeadersMiddleware — API/admin routes
// ---------------------------------------------------------------------------

func TestSecurityHeadersMiddleware_NoSniff(t *testing.T) {
	sink := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
	})
	srv := httptest.NewServer(eehttp.SecurityHeadersMiddleware(sink))
	defer srv.Close()

	resp, err := nethttp.Get(srv.URL + "/api/v1/health")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"),
		"X-Content-Type-Options: nosniff must be set on API routes")
}

func TestSecurityHeadersMiddleware_XFrameOptions(t *testing.T) {
	sink := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
	})
	srv := httptest.NewServer(eehttp.SecurityHeadersMiddleware(sink))
	defer srv.Close()

	resp, err := nethttp.Get(srv.URL + "/api/v1/agents")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"),
		"X-Frame-Options: DENY must be set on API routes")
}

func TestSecurityHeadersMiddleware_CSP(t *testing.T) {
	sink := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
	})
	srv := httptest.NewServer(eehttp.SecurityHeadersMiddleware(sink))
	defer srv.Close()

	resp, err := nethttp.Get(srv.URL + "/api/v1/agents")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	csp := resp.Header.Get("Content-Security-Policy")
	assert.Contains(t, csp, "default-src 'self'", "CSP must contain default-src 'self'")
	assert.Contains(t, csp, "frame-ancestors 'none'", "CSP must block framing on API routes")
}

func TestSecurityHeadersMiddleware_ReferrerPolicy(t *testing.T) {
	sink := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
	})
	srv := httptest.NewServer(eehttp.SecurityHeadersMiddleware(sink))
	defer srv.Close()

	resp, err := nethttp.Get(srv.URL + "/api/v1/agents")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, "strict-origin-when-cross-origin",
		resp.Header.Get("Referrer-Policy"),
		"Referrer-Policy must be set")
}

func TestSecurityHeadersMiddleware_HSTS_OnlyForTLS(t *testing.T) {
	sink := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
	})

	t.Run("no HSTS on plain HTTP", func(t *testing.T) {
		req := httptest.NewRequest(nethttp.MethodGet, "/api/v1/agents", nil)
		// no X-Forwarded-Proto header, no TLS
		rec := httptest.NewRecorder()
		eehttp.SecurityHeadersMiddleware(sink).ServeHTTP(rec, req)
		assert.Empty(t, rec.Header().Get("Strict-Transport-Security"),
			"HSTS must not be set on plain HTTP")
	})

	t.Run("HSTS set when X-Forwarded-Proto: https", func(t *testing.T) {
		req := httptest.NewRequest(nethttp.MethodGet, "/api/v1/agents", nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		rec := httptest.NewRecorder()
		eehttp.SecurityHeadersMiddleware(sink).ServeHTTP(rec, req)
		hsts := rec.Header().Get("Strict-Transport-Security")
		assert.Contains(t, hsts, "max-age=31536000", "HSTS max-age must be set when behind TLS proxy")
		assert.Contains(t, hsts, "includeSubDomains", "HSTS must include subdomains")
	})
}

func TestSecurityHeadersMiddleware_DoesNotOverrideDownstreamCSP(t *testing.T) {
	customCSP := "default-src 'none'; script-src 'self'"
	sink := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.Header().Set("Content-Security-Policy", customCSP)
		w.WriteHeader(nethttp.StatusOK)
	})
	req := httptest.NewRequest(nethttp.MethodGet, "/api/v1/agents", nil)
	rec := httptest.NewRecorder()
	eehttp.SecurityHeadersMiddleware(sink).ServeHTTP(rec, req)
	assert.Equal(t, customCSP, rec.Header().Get("Content-Security-Policy"),
		"middleware must not override a CSP already set by a downstream handler")
}

// ---------------------------------------------------------------------------
// WidgetSecurityHeadersMiddleware
// ---------------------------------------------------------------------------

func TestWidgetSecurityHeadersMiddleware_NoXFrameOptions(t *testing.T) {
	sink := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
	})
	lookup := &fakeLookup{origins: map[string][]string{
		"tenant-1": {"https://customer.example.com"},
	}}
	mw := eehttp.WidgetSecurityHeadersMiddleware(lookup)
	req := httptest.NewRequest(nethttp.MethodGet, "/widget.js", nil)
	req = req.WithContext(domain.WithTenantID(req.Context(), "tenant-1"))
	rec := httptest.NewRecorder()
	mw(sink).ServeHTTP(rec, req)

	assert.Empty(t, rec.Header().Get("X-Frame-Options"),
		"X-Frame-Options must NOT be set on widget routes (widget must embed)")
}

func TestWidgetSecurityHeadersMiddleware_FrameAncestorsOrigins(t *testing.T) {
	lookup := &fakeLookup{origins: map[string][]string{
		"tenant-1": {"https://customer.example.com", "https://store.example.org"},
	}}
	sink := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
	})
	mw := eehttp.WidgetSecurityHeadersMiddleware(lookup)
	req := httptest.NewRequest(nethttp.MethodGet, "/widget.js", nil)
	req = req.WithContext(domain.WithTenantID(req.Context(), "tenant-1"))
	rec := httptest.NewRecorder()
	mw(sink).ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	assert.Contains(t, csp, "https://customer.example.com",
		"CSP frame-ancestors must list configured embed origins")
	assert.Contains(t, csp, "https://store.example.org",
		"CSP frame-ancestors must list all configured embed origins")
}

func TestWidgetSecurityHeadersMiddleware_EmptyOrigins_BlocksFraming(t *testing.T) {
	sink := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
	})
	mw := eehttp.WidgetSecurityHeadersMiddleware(nil)
	req := httptest.NewRequest(nethttp.MethodGet, "/widget.js", nil)
	rec := httptest.NewRecorder()
	mw(sink).ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	assert.Contains(t, csp, "frame-ancestors 'none'",
		"empty embed origins must produce frame-ancestors 'none'")
}

func TestWidgetSecurityHeadersMiddleware_NoSniffAndReferrer(t *testing.T) {
	sink := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
	})
	mw := eehttp.WidgetSecurityHeadersMiddleware(nil)
	req := httptest.NewRequest(nethttp.MethodGet, "/widget.js", nil)
	rec := httptest.NewRecorder()
	mw(sink).ServeHTTP(rec, req)

	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "strict-origin-when-cross-origin", rec.Header().Get("Referrer-Policy"))
}

// ---------------------------------------------------------------------------
// WidgetSecurityHeadersMiddleware — per-tenant lookup (new behaviour)
// ---------------------------------------------------------------------------

func TestWidgetSecurityHeadersMiddleware_ReadsPerTenantOrigins(t *testing.T) {
	lookup := &fakeLookup{origins: map[string][]string{
		"tenant-abc": {"https://partner.example.com", "https://staging.example.com"},
	}}
	sink := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
	})
	mw := eehttp.WidgetSecurityHeadersMiddleware(lookup)

	t.Run("tenant with origins gets them in frame-ancestors", func(t *testing.T) {
		req := httptest.NewRequest(nethttp.MethodGet, "/widget.js", nil)
		req = req.WithContext(domain.WithTenantID(req.Context(), "tenant-abc"))
		rec := httptest.NewRecorder()
		mw(sink).ServeHTTP(rec, req)

		csp := rec.Header().Get("Content-Security-Policy")
		assert.Contains(t, csp, "https://partner.example.com",
			"CSP must contain first configured origin")
		assert.Contains(t, csp, "https://staging.example.com",
			"CSP must contain second configured origin")
		assert.NotContains(t, csp, "'none'",
			"CSP must not block framing when origins are configured")
		assert.Empty(t, rec.Header().Get("X-Frame-Options"),
			"X-Frame-Options must not be set on widget routes")
	})

	t.Run("no tenant in ctx defaults to frame-ancestors none", func(t *testing.T) {
		req := httptest.NewRequest(nethttp.MethodGet, "/widget.js", nil)
		// no tenant injected into context
		rec := httptest.NewRecorder()
		mw(sink).ServeHTTP(rec, req)

		csp := rec.Header().Get("Content-Security-Policy")
		assert.Contains(t, csp, "frame-ancestors 'none'",
			"missing tenant must fall back to frame-ancestors 'none'")
	})

	t.Run("global CSP set before widget middleware is overwritten", func(t *testing.T) {
		// Simulate the global SecurityHeadersMiddleware having run first.
		req := httptest.NewRequest(nethttp.MethodGet, "/widget.js", nil)
		req = req.WithContext(domain.WithTenantID(req.Context(), "tenant-abc"))
		rec := httptest.NewRecorder()
		// Pre-set the global default CSP as the global middleware would.
		rec.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
		mw(sink).ServeHTTP(rec, req)

		csp := rec.Header().Get("Content-Security-Policy")
		assert.Contains(t, csp, "https://partner.example.com",
			"widget middleware must overwrite the global CSP with per-tenant origins")
	})
}

// ---------------------------------------------------------------------------
// CORS whitelist — SCC-02 variant: attacker origin blocked on admin APIs
// ---------------------------------------------------------------------------

// Admin-scope endpoints (anything under /api/v1/ that isn't the public widget
// chat endpoint) must honour the configured allowlist — attacker origins get
// no ACAO header, configured origins do.
func TestCORSWhitelist_AttackerOriginBlocked(t *testing.T) {
	srv := eehttp.NewServerWithCORS(0, []string{"https://legit.example.com"})
	srv.Router().Get("/api/v1/agents", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
	})

	t.Run("preflight from attacker origin gets no ACAO header", func(t *testing.T) {
		req := httptest.NewRequest(nethttp.MethodOptions, "/api/v1/agents", nil)
		req.Header.Set("Origin", "https://evil.example.com")
		req.Header.Set("Access-Control-Request-Method", "GET")
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)

		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"),
			"attacker origin must not receive Access-Control-Allow-Origin")
	})

	t.Run("preflight from legit origin gets ACAO header", func(t *testing.T) {
		req := httptest.NewRequest(nethttp.MethodOptions, "/api/v1/agents", nil)
		req.Header.Set("Origin", "https://legit.example.com")
		req.Header.Set("Access-Control-Request-Method", "GET")
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)

		assert.Equal(t, "https://legit.example.com",
			rec.Header().Get("Access-Control-Allow-Origin"),
			"legit origin must receive Access-Control-Allow-Origin")
	})
}

// The public widget chat endpoint (POST /api/v1/schemas/{id}/chat) is embedded
// on third-party customer sites — CORS must accept arbitrary origins. Tenant
// isolation is enforced by the handler resolving schema_id → tenant_id, NOT by
// the CORS policy. This is the standard model for public chat widgets
// (Intercom, Crisp, Drift all operate the same way).
func TestCORSPublic_WidgetChatEndpoint_AcceptsAnyOrigin(t *testing.T) {
	srv := eehttp.NewServerWithCORS(0, []string{"https://legit.example.com"})
	srv.Router().Post("/api/v1/schemas/{id}/chat", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
	})

	for _, origin := range []string{
		"https://customer-a.example.com",
		"https://random-blog.example.net",
		"https://evil.example.com",
	} {
		t.Run("preflight from "+origin+" is accepted", func(t *testing.T) {
			req := httptest.NewRequest(nethttp.MethodOptions, "/api/v1/schemas/abc-123/chat", nil)
			req.Header.Set("Origin", origin)
			req.Header.Set("Access-Control-Request-Method", "POST")
			req.Header.Set("Access-Control-Request-Headers", "Content-Type")
			rec := httptest.NewRecorder()
			srv.Router().ServeHTTP(rec, req)

			assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"),
				"public widget chat endpoint must accept any origin (wildcard)")
		})
	}
}

func TestCORSWhitelist_DefaultServer_NoWildcard(t *testing.T) {
	srv := eehttp.NewServer(0)
	srv.Router().Get("/api/v1/health", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
	})

	req := httptest.NewRequest(nethttp.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Origin", "https://attacker.com")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"),
		"default server must not grant CORS to any origin (no wildcard)")
	assert.NotEqual(t, "*", rec.Header().Get("Access-Control-Allow-Origin"),
		"wildcard CORS must never be returned by default server")
}
