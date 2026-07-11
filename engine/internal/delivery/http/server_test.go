package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewServer_DefaultCORS(t *testing.T) {
	srv := NewServer(0)
	srv.Router().Get("/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Same-origin policy: no wildcard, cross-origin requests get no Allow-Origin header.
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://random-site.com")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"),
		"default server must not grant CORS to arbitrary origins")
}

func TestNewServerWithCORS_CustomOrigins(t *testing.T) {
	allowed := []string{"https://example.com", "https://app.example.com"}
	srv := NewServerWithCORS(0, allowed)
	srv.Router().Get("/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("allowed origin gets CORS header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Origin", "https://example.com")
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "https://example.com", rec.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("disallowed origin gets no CORS header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Origin", "https://evil.com")
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
	})
}

func TestNewServerWithCORS_EmptyOrigins(t *testing.T) {
	// Empty slice means same-origin only — no wildcard.
	srv := NewServerWithCORS(0, []string{})
	srv.Router().Get("/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://any-site.com")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"),
		"empty origins must not grant CORS to arbitrary origins")
}

func TestNewServerWithCORS_NilOrigins(t *testing.T) {
	// nil means same-origin only — no wildcard.
	srv := NewServerWithCORS(0, nil)
	srv.Router().Get("/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://any-site.com")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"),
		"nil origins must not grant CORS to arbitrary origins")
}

func TestCORS_Preflight(t *testing.T) {
	allowed := []string{"https://example.com"}
	srv := NewServerWithCORS(0, allowed)
	srv.Router().Post("/api/v1/agents/test/chat", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("preflight with allowed origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/api/v1/agents/test/chat", nil)
		req.Header.Set("Origin", "https://example.com")
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "Content-Type, Authorization")
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "https://example.com", rec.Header().Get("Access-Control-Allow-Origin"))
		assert.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), "POST")
	})

	t.Run("preflight with disallowed origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/api/v1/agents/test/chat", nil)
		req.Header.Set("Origin", "https://evil.com")
		req.Header.Set("Access-Control-Request-Method", "POST")
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)

		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
	})
}

func TestCORS_WidgetAPIPublicCrossOrigin(t *testing.T) {
	// Same-origin default policy for the admin API; the widget API paths must
	// still accept cross-origin preflights from ANY customer site with the
	// Authorization header (the widget sends a Bearer chat token).
	srv := NewServer(0)
	srv.Router().Get("/api/v1/widget-config", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	srv.Router().Post("/api/v1/schemas/{id}/chat", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	srv.Router().Get("/api/v1/agents", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	cases := []struct {
		name, method, path string
	}{
		{"widget-config GET", http.MethodGet, "/api/v1/widget-config"},
		{"widget chat POST", http.MethodPost, "/api/v1/schemas/support-bot/chat"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodOptions, tc.path, nil)
			req.Header.Set("Origin", "https://a-customer-site.example")
			req.Header.Set("Access-Control-Request-Method", tc.method)
			req.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
			rec := httptest.NewRecorder()
			srv.Router().ServeHTTP(rec, req)

			assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"),
				"widget API must allow any origin")
			assert.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), tc.method)
			assert.Contains(t, rec.Header().Get("Access-Control-Allow-Headers"), "Authorization",
				"widget API preflight must allow the Bearer Authorization header")
		})
	}

	t.Run("non-widget path stays same-origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/api/v1/agents", nil)
		req.Header.Set("Origin", "https://a-customer-site.example")
		req.Header.Set("Access-Control-Request-Method", http.MethodGet)
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)

		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"),
			"a non-widget path must not be exposed cross-origin")
	})
}

func TestCORS_ExposedHeaders(t *testing.T) {
	// Must use an explicitly-allowed origin; the default server uses same-origin policy.
	srv := NewServerWithCORS(0, []string{"https://example.com"})
	srv.Router().Get("/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	exposed := rec.Header().Get("Access-Control-Expose-Headers")
	// Header names are canonicalized by the CORS middleware.
	assert.Contains(t, exposed, "X-Ratelimit-Limit")
	assert.Contains(t, exposed, "Retry-After")
}
