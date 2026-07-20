package http

import (
	"net/http"
	"strings"
)

// OAuthOriginGuard defends the credential-issuing endpoints (OAuth register /
// token / authorize-info / approve and the local-admin session) against
// DNS-rebinding and cross-site drive-by attacks (D4-SEC).
//
// Two independent checks, both path-gated so unrelated routes pass through
// untouched:
//
//   - Host allowlist on /oauth/*, /api/v1/oauth/* and /api/v1/auth/local-session:
//     the Host header must be an allowed host. A DNS-rebinding attacker can make
//     a victim's browser resolve an attacker origin to 127.0.0.1, but the Host
//     header still carries the attacker's domain, so pinning Host to the
//     configured issuer host (plus loopback for CE dev) rejects the rebind.
//
//   - Sec-Fetch-Site gate on /api/v1/auth/local-session: a present
//     Sec-Fetch-Site header equal to "cross-site" is rejected. An ABSENT header
//     is allowed on purpose — curl, CLIs and older browsers send none, and the
//     Host allowlist is the primary guard; denying on absence would break those
//     legitimate non-browser callers.
type OAuthOriginGuard struct {
	allowedHosts map[string]struct{}
}

// NewOAuthOriginGuard builds a guard whose Host allowlist is the given hosts
// plus loopback (localhost, 127.0.0.1, [::1]). Hosts are compared
// case-insensitively with any port stripped. Passing no issuer host (local dev
// with no configured issuer) leaves only loopback allowed.
func NewOAuthOriginGuard(issuerHosts ...string) *OAuthOriginGuard {
	allowed := map[string]struct{}{
		"localhost": {},
		"127.0.0.1": {},
		"[::1]":     {},
		"::1":       {},
	}
	for _, h := range issuerHosts {
		h = strings.ToLower(strings.TrimSpace(hostWithoutPort(h)))
		if h != "" {
			allowed[h] = struct{}{}
		}
	}
	return &OAuthOriginGuard{allowedHosts: allowed}
}

// Handler is the middleware. It enforces the checks only on the sensitive
// paths; everything else is forwarded unchanged.
func (g *OAuthOriginGuard) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isOAuthSensitivePath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if !g.hostAllowed(r.Host) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "host not allowed"})
			return
		}
		if isLocalSessionPath(r.URL.Path) {
			// Absent Sec-Fetch-Site is allowed (non-browser callers); only an
			// explicit cross-site fetch is rejected.
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross-site request rejected"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// hostAllowed reports whether the request Host (port stripped) is allowlisted.
// An empty Host is allowed — HTTP/1.0 and some non-browser clients omit it, and
// a browser (the DNS-rebinding vector) always sends one.
func (g *OAuthOriginGuard) hostAllowed(host string) bool {
	if host == "" {
		return true
	}
	_, ok := g.allowedHosts[strings.ToLower(hostWithoutPort(host))]
	return ok
}

// isOAuthSensitivePath reports whether path issues or brokers credentials and
// must be Host-pinned.
func isOAuthSensitivePath(path string) bool {
	if strings.HasPrefix(path, "/oauth/") || strings.HasPrefix(path, "/api/v1/oauth/") {
		return true
	}
	return isLocalSessionPath(path)
}

// isLocalSessionPath reports whether path is the local-admin session issuer.
func isLocalSessionPath(path string) bool {
	return path == "/api/v1/auth/local-session" || path == "/api/v1/auth/local-session/refresh"
}

// hostWithoutPort strips a trailing :port from a host, leaving bracketed IPv6
// literals intact ("[::1]:8443" → "[::1]").
func hostWithoutPort(host string) string {
	if host == "" {
		return ""
	}
	if strings.HasPrefix(host, "[") {
		if end := strings.IndexByte(host, ']'); end >= 0 {
			return host[:end+1]
		}
		return host
	}
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		return host[:i]
	}
	return host
}
