package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// oauthProtectedResourceMetadata is the RFC 9728 document served at
// /.well-known/oauth-protected-resource so MCP clients can discover the
// authorization server protecting /api/v1/mcp/rpc.
type oauthProtectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	ScopesSupported        []string `json:"scopes_supported"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
}

// OAuthProtectedResource serves RFC 9728 protected-resource metadata for the
// MCP endpoint and decorates 401 responses under /api/v1/mcp with the
// WWW-Authenticate challenge that bootstraps the OAuth flow (RFC 9728 §5).
type OAuthProtectedResource struct {
	metadata  oauthProtectedResourceMetadata
	challenge string
}

// NewOAuthProtectedResource creates an OAuthProtectedResource. resource is the
// canonical MCP resource URI (the aud of MCP access tokens); authorizationServer
// is the authorization-server issuer URL advertised to clients; challenge is the
// precomputed WWW-Authenticate value (e.g. `Bearer resource_metadata="…",
// scope="provision"`).
func NewOAuthProtectedResource(resource, authorizationServer, challenge string) *OAuthProtectedResource {
	return &OAuthProtectedResource{
		metadata: oauthProtectedResourceMetadata{
			Resource:               resource,
			AuthorizationServers:   []string{authorizationServer},
			ScopesSupported:        []string{domain.OAuthScopeProvision, domain.OAuthScopeManage},
			BearerMethodsSupported: []string{"header"},
		},
		challenge: challenge,
	}
}

// Metadata handles GET /.well-known/oauth-protected-resource (and the
// path-suffixed /.well-known/oauth-protected-resource/api/v1/mcp/rpc variant).
func (p *OAuthProtectedResource) Metadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(p.metadata); err != nil {
		slog.ErrorContext(r.Context(), "encode protected resource metadata", "error", err)
	}
}

// Challenge decorates 401 responses under /api/v1/mcp with a WWW-Authenticate
// challenge pointing at the protected-resource metadata, so MCP clients can
// bootstrap the OAuth flow (RFC 9728 §5). All other paths and statuses pass
// through untouched.
func (p *OAuthProtectedResource) Challenge() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isMCPPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(&challengeResponseWriter{ResponseWriter: w, challenge: p.challenge}, r)
		})
	}
}

func isMCPPath(path string) bool {
	return path == "/api/v1/mcp" || strings.HasPrefix(path, "/api/v1/mcp/")
}

// challengeResponseWriter injects the WWW-Authenticate challenge the moment a
// 401 status is committed — headers must be set before WriteHeader flushes them
// downstream. The challenge replaces any value the handler set itself.
type challengeResponseWriter struct {
	http.ResponseWriter
	challenge   string
	wroteHeader bool
}

func (w *challengeResponseWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.wroteHeader = true
		if status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", w.challenge)
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *challengeResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// Flush keeps SSE streaming working through the wrapper.
func (w *challengeResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (w *challengeResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
