package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func newGuardedServer() http.Handler {
	guard := NewOAuthOriginGuard("engine.example")
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return guard.Handler(mux)
}

func TestOAuthOriginGuard_HostAllowlist(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		host     string
		wantCode int
	}{
		{"oauth token allowed issuer host", "/oauth/token", "engine.example", http.StatusOK},
		{"oauth token allowed issuer host with port", "/oauth/token", "engine.example:8443", http.StatusOK},
		{"oauth token allowed loopback", "/oauth/token", "127.0.0.1:9555", http.StatusOK},
		{"oauth token allowed localhost", "/oauth/register", "localhost", http.StatusOK},
		{"oauth token foreign host rejected", "/oauth/token", "attacker.evil", http.StatusForbidden},
		{"api oauth foreign host rejected", "/api/v1/oauth/register", "attacker.evil", http.StatusForbidden},
		{"local-session foreign host rejected", "/api/v1/auth/local-session", "attacker.evil", http.StatusForbidden},
		{"unrelated path never guarded", "/api/v1/health", "attacker.evil", http.StatusOK},
		{"well-known never guarded", "/.well-known/oauth-authorization-server", "attacker.evil", http.StatusOK},
		{"empty host allowed (non-browser)", "/oauth/token", "", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, nil)
			if tt.host != "" {
				req.Host = tt.host
			} else {
				req.Host = ""
			}
			rec := httptest.NewRecorder()
			newGuardedServer().ServeHTTP(rec, req)
			assert.Equal(t, tt.wantCode, rec.Code)
		})
	}
}

func TestOAuthOriginGuard_SecFetchSiteOnLocalSession(t *testing.T) {
	tests := []struct {
		name        string
		secFetch    string
		setHeader   bool
		wantCode    int
		description string
	}{
		{"cross-site rejected", "cross-site", true, http.StatusForbidden, "explicit cross-site fetch"},
		{"same-origin allowed", "same-origin", true, http.StatusOK, "same-origin fetch"},
		{"none allowed", "none", true, http.StatusOK, "browser direct navigation"},
		{"absent allowed", "", false, http.StatusOK, "curl/CLI/older browsers send no header"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/local-session", nil)
			req.Host = "engine.example"
			if tt.setHeader {
				req.Header.Set("Sec-Fetch-Site", tt.secFetch)
			}
			rec := httptest.NewRecorder()
			newGuardedServer().ServeHTTP(rec, req)
			assert.Equal(t, tt.wantCode, rec.Code, tt.description)
		})
	}
}

func TestOAuthOriginGuard_SecFetchNotAppliedToOAuthEndpoints(t *testing.T) {
	// The Sec-Fetch-Site gate is local-session-specific; a cross-site fetch to
	// /oauth/token from an allowed host is fine (in-browser MCP clients).
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", nil)
	req.Host = "engine.example"
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	newGuardedServer().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestOAuthOriginGuard_NoIssuerHostAllowsLoopbackOnly(t *testing.T) {
	guard := NewOAuthOriginGuard("")
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := guard.Handler(mux)

	loopback := httptest.NewRequest(http.MethodPost, "/oauth/token", nil)
	loopback.Host = "localhost:9555"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loopback)
	assert.Equal(t, http.StatusOK, rec.Code)

	foreign := httptest.NewRequest(http.MethodPost, "/oauth/token", nil)
	foreign.Host = "engine.example"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, foreign)
	assert.Equal(t, http.StatusForbidden, rec.Code, "no configured issuer → only loopback allowed")
}
