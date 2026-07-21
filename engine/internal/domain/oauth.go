package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ConsentRequestHash binds an anti-CSRF consent nonce to the specific
// authorization request it was minted for. Hashing client_id + redirect_uri +
// scope + code_challenge means a nonce issued while rendering one client's
// consent page cannot be replayed to approve a different client, redirect
// target, or scope set — only the exact request the user actually saw. The
// fields are joined with a NUL separator so no concatenation ambiguity can
// collide two distinct requests.
func ConsentRequestHash(clientID, redirectURI, scope, codeChallenge string) string {
	sum := sha256.Sum256([]byte(clientID + "\x00" + redirectURI + "\x00" + scope + "\x00" + codeChallenge))
	return hex.EncodeToString(sum[:])
}

// ErrOAuthCodeReplayed signals that an authorization code is being exchanged a
// second time. The refresh-token store returns it (via errors.Is) when an
// insert collides with the per-code uniqueness constraint, so the token
// usecase can revoke the implicated token family. It lives in the domain layer
// so the usecase can match it without importing the persistence package.
var ErrOAuthCodeReplayed = errors.New("authorization code replay detected")

// OAuth 2.1 scope vocabulary advertised by the authorization server.
const (
	OAuthScopeProvision     = "provision"
	OAuthScopeManage        = "manage"
	OAuthScopeOfflineAccess = "offline_access"
)

// SupportedOAuthScopes returns the scopes advertised in server metadata.
func SupportedOAuthScopes() []string {
	return []string{OAuthScopeProvision, OAuthScopeManage, OAuthScopeOfflineAccess}
}

// ParseScope splits a space-delimited OAuth scope string into tokens.
func ParseScope(scope string) []string {
	return strings.Fields(scope)
}

// ScopeSubset reports whether every requested scope is present in allowed.
func ScopeSubset(requested, allowed []string) bool {
	set := make(map[string]struct{}, len(allowed))
	for _, s := range allowed {
		set[s] = struct{}{}
	}
	for _, s := range requested {
		if _, ok := set[s]; !ok {
			return false
		}
	}
	return true
}

// OAuthError is an RFC 6749 protocol error (error + error_description).
// Delivery maps it to the raw OAuth error JSON, not the API envelope.
type OAuthError struct {
	Code        string
	Description string
}

// Error implements the error interface.
func (e *OAuthError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Description)
}

// NewOAuthError creates an RFC 6749 error with the given code and description.
func NewOAuthError(code, description string) *OAuthError {
	return &OAuthError{Code: code, Description: description}
}

// OAuthClient is the metadata carried inside a stateless client_id JWT.
type OAuthClient struct {
	Name         string
	RedirectURIs []string
}

// OAuthClientFromClaims extracts client metadata from the verified claims of
// a client_id blob.
func OAuthClientFromClaims(claims map[string]any) (OAuthClient, error) {
	name, _ := claims["client_name"].(string)
	rawURIs, ok := claims["redirect_uris"].([]any)
	if !ok || len(rawURIs) == 0 {
		return OAuthClient{}, fmt.Errorf("client_id has no redirect_uris")
	}
	uris := make([]string, 0, len(rawURIs))
	for _, raw := range rawURIs {
		uri, ok := raw.(string)
		if !ok {
			return OAuthClient{}, fmt.Errorf("client_id has a non-string redirect_uri")
		}
		uris = append(uris, uri)
	}
	return OAuthClient{Name: name, RedirectURIs: uris}, nil
}

// MatchesRedirectURI reports whether presented matches any registered
// redirect URI of the client (loopback URIs match port-agnostically).
func (c OAuthClient) MatchesRedirectURI(presented string) bool {
	for _, registered := range c.RedirectURIs {
		if MatchRedirectURI(registered, presented) {
			return true
		}
	}
	return false
}

// ValidateAuthorizeRequest checks an authorization request against the
// registered client: redirect_uri membership, PKCE S256 challenge and scope
// subset. Returns the parsed requested scopes on success.
func ValidateAuthorizeRequest(client OAuthClient, redirectURI, scope, codeChallenge, codeChallengeMethod string) ([]string, *OAuthError) {
	if !client.MatchesRedirectURI(redirectURI) {
		return nil, NewOAuthError("invalid_request", "redirect_uri is not registered for this client")
	}
	if codeChallengeMethod != "S256" {
		return nil, NewOAuthError("invalid_request", "code_challenge_method must be S256")
	}
	if codeChallenge == "" {
		return nil, NewOAuthError("invalid_request", "code_challenge is required")
	}
	scopes := ParseScope(scope)
	if len(scopes) == 0 {
		return nil, NewOAuthError("invalid_scope", "scope is required")
	}
	if !ScopeSubset(scopes, SupportedOAuthScopes()) {
		return nil, NewOAuthError("invalid_scope", "scope contains unsupported values")
	}
	return scopes, nil
}

// ValidateRedirectURI checks a redirect URI at client registration time.
// Allowed: https (any host), custom schemes whose URI contains a dot
// (e.g. cursor://host.tld/... or com.example.app://callback), and plain-http
// loopback (localhost / 127.0.0.1, any port, any path). Everything else —
// notably plain http on a non-loopback host — is rejected.
func ValidateRedirectURI(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("redirect_uri %q is not a valid URI", raw)
	}
	if u.Scheme == "" {
		return fmt.Errorf("redirect_uri %q has no scheme", raw)
	}
	if u.Fragment != "" {
		return fmt.Errorf("redirect_uri %q must not contain a fragment", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("redirect_uri %q: plain http is only allowed for loopback hosts", raw)
	default:
		// Native-app custom schemes must look intentional: require a dot
		// somewhere in the URI (reverse-domain scheme or dotted host).
		if strings.Contains(raw, ".") {
			return nil
		}
		return fmt.Errorf("redirect_uri %q: custom scheme URIs must contain a dot", raw)
	}
}

// MatchRedirectURI reports whether presented matches registered.
// RFC 8252 loopback rule: when the registered URI is plain http on
// localhost / 127.0.0.1, the port is ignored (scheme, host, path and query
// must still match exactly). All other URIs require an exact string match.
func MatchRedirectURI(registered, presented string) bool {
	if registered == presented {
		return true
	}
	reg, err := url.Parse(registered)
	if err != nil || reg.Scheme != "http" || !isLoopbackHost(reg.Hostname()) {
		return false
	}
	pres, err := url.Parse(presented)
	if err != nil {
		return false
	}
	return pres.Scheme == "http" &&
		pres.Hostname() == reg.Hostname() &&
		pres.Path == reg.Path &&
		pres.RawQuery == reg.RawQuery
}

func isLoopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1"
}
