package http

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/syntheticinc/syntheticbrew/internal/authprim"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	pluginpkg "github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// writeUnauthorized emits a 401 response with an RFC 7235 §3.1 compliant
// `WWW-Authenticate: Bearer` challenge. errCode/description follow RFC 6750
// §3: "invalid_request" | "invalid_token" | "insufficient_scope" — or empty
// for the "credentials missing entirely" case where only a bare challenge is
// appropriate.
func writeUnauthorized(w http.ResponseWriter, errCode, description string) {
	challenge := `Bearer realm="syntheticbrew"`
	if errCode != "" {
		challenge += fmt.Sprintf(`, error=%q, error_description=%q`, errCode, description)
	}
	w.Header().Set("WWW-Authenticate", challenge)
	msg := errCode
	if msg == "" {
		msg = "unauthorized"
	}
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": msg})
}

type contextKey string

const (
	// ContextKeyActorType holds the actor type: "admin" or "api_token".
	ContextKeyActorType contextKey = "actor_type"
	// ContextKeyActorID holds the actor identifier (subject for JWT, name for API token).
	ContextKeyActorID contextKey = "actor_id"
	// ContextKeyScopes holds the bitmask of allowed scopes.
	ContextKeyScopes contextKey = "scopes"
)

// ActorTypeFromContext returns the authenticated actor type ("admin" or
// "api_token"), or "" when none was stamped. Exported so packages outside the
// delivery layer can distinguish operator traffic from end-user traffic
// without importing the unexported context-key type.
func ActorTypeFromContext(ctx context.Context) string {
	actorType, _ := ctx.Value(ContextKeyActorType).(string)
	return actorType
}

// contextKeyOperatorChat marks a chat request as the operator's own builder-
// assistant, not a deployment end-user. It is set by the admin-assistant
// handler and read by the chat service to keep operator traffic out of the
// distinct-end-user accounting. It is NOT actor-based: JWT-authenticated end
// users (e.g. behind the identity broker) present as the "admin" actor too, so
// only this explicit route-level marker distinguishes the operator surface.
const contextKeyOperatorChat contextKey = "operator_chat"

// WithOperatorChat marks the context as operator (builder-assistant) traffic.
func WithOperatorChat(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKeyOperatorChat, true)
}

// IsOperatorChat reports whether the context was marked as operator traffic.
func IsOperatorChat(ctx context.Context) bool {
	v, _ := ctx.Value(contextKeyOperatorChat).(bool)
	return v
}

// Scope bitmask constants matching ERD api_tokens.scopes_mask.
//
// Canonical definitions live in internal/authprim (leaf package) so
// non-delivery layers — e.g. the EdDSA JWT verifier in
// internal/infrastructure/auth, which parses the JWT `scope` claim — share
// one registry without importing the delivery layer. Re-exported here under
// the original names because delivery code and its callers are the primary
// consumers.
const (
	ScopeChat          = authprim.ScopeChat
	ScopeTasks         = authprim.ScopeTasks
	ScopeAgentsRead    = authprim.ScopeAgentsRead
	ScopeConfig        = authprim.ScopeConfig
	ScopeAdmin         = authprim.ScopeAdmin
	ScopeAgentsWrite   = authprim.ScopeAgentsWrite
	ScopeModelsRead    = authprim.ScopeModelsRead
	ScopeModelsWrite   = authprim.ScopeModelsWrite
	ScopeMCPRead       = authprim.ScopeMCPRead
	ScopeMCPWrite      = authprim.ScopeMCPWrite
	ScopeTriggersRead  = authprim.ScopeTriggersRead
	ScopeTriggersWrite = authprim.ScopeTriggersWrite
	ScopeSchemasRead   = authprim.ScopeSchemasRead
	ScopeSchemasWrite  = authprim.ScopeSchemasWrite

	ScopeSessionsRead    = authprim.ScopeSessionsRead
	ScopeSessionsWrite   = authprim.ScopeSessionsWrite
	ScopeSettingsRead    = authprim.ScopeSettingsRead
	ScopeSettingsWrite   = authprim.ScopeSettingsWrite
	ScopeAuditRead       = authprim.ScopeAuditRead
	ScopeResilienceRead  = authprim.ScopeResilienceRead
	ScopeResilienceWrite = authprim.ScopeResilienceWrite
	ScopeToolsRead       = authprim.ScopeToolsRead

	ScopeManage = authprim.ScopeManage
)

// Composite masks — see authprim for the full rationale of each.
//
// Bug 3 (ScopeAPIMask): clients POST /auth/tokens with `scopes: ["api"]` —
// we expand that name into the mask. An empty mask was previously stored
// (0), which authenticated the token but 403'd every request.
const (
	ScopeAPIMask       = authprim.ScopeAPIMask
	ScopeProvisionMask = authprim.ScopeProvisionMask
	ScopeManageMask    = authprim.ScopeManageMask
)

// ScopeNameToMask maps canonical scope name tokens accepted by
// POST /auth/tokens `scopes: [...]` to their underlying bitmask.
// Canonical registry lives in authprim.
var ScopeNameToMask = authprim.ScopeNameToMask

// ScopesToMask converts a list of scope names into a bitmask. Unknown
// names are dropped (no error) — defensive against front-end typos that
// might otherwise privilege-escalate. An empty list returns 0.
func ScopesToMask(scopes []string) int {
	return authprim.ScopesToMask(scopes)
}

// APITokenInfo is the decoded API-token record returned by the verifier.
type APITokenInfo struct {
	Name       string
	ScopesMask int
	TenantID   string
}

// APITokenVerifier looks up API tokens by their SHA-256 hash.
type APITokenVerifier interface {
	VerifyToken(ctx context.Context, tokenHash string) (APITokenInfo, error)
}

// AuthMiddleware handles dual authentication: admin session JWT and API tokens (bb_ prefix).
type AuthMiddleware struct {
	jwtVerifier   pluginpkg.JWTVerifier
	tokenVerifier APITokenVerifier
}

// NewAuthMiddlewareWithVerifier creates an AuthMiddleware backed by the given
// JWT verifier. Wave 7 collapsed CE and multi-tenant modes onto a single EdDSA verifier,
// so there is no longer a "default HMAC" constructor — the caller is
// responsible for building (or loading) the verifier.
func NewAuthMiddlewareWithVerifier(jwtVerifier pluginpkg.JWTVerifier, tokenVerifier APITokenVerifier) *AuthMiddleware {
	return &AuthMiddleware{
		jwtVerifier:   jwtVerifier,
		tokenVerifier: tokenVerifier,
	}
}

// JWTVerifier returns the middleware's JWT verifier. Other delivery layers
// (e.g. gRPC) reuse it to decode tokens consistently across transports.
func (m *AuthMiddleware) JWTVerifier() pluginpkg.JWTVerifier {
	return m.jwtVerifier
}

// Authenticate is the middleware handler that validates Bearer tokens.
func (m *AuthMiddleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			writeUnauthorized(w, "", "")
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")

		if strings.HasPrefix(token, "bb_") {
			m.authenticateAPIToken(w, r, next, token)
			return
		}

		m.authenticateJWT(w, r, next, token)
	})
}

func (m *AuthMiddleware) authenticateAPIToken(w http.ResponseWriter, r *http.Request, next http.Handler, token string) {
	hash := authprim.Hash(token)
	info, err := m.tokenVerifier.VerifyToken(r.Context(), hash)
	if err != nil {
		slog.WarnContext(r.Context(), "auth: api token verification failed",
			"path", r.URL.Path, "remote", r.RemoteAddr, "error", err)
		writeUnauthorized(w, "invalid_token", "invalid api token")
		return
	}
	ctx := context.WithValue(r.Context(), ContextKeyActorType, "api_token")
	ctx = context.WithValue(ctx, ContextKeyActorID, info.Name)
	ctx = context.WithValue(ctx, ContextKeyScopes, info.ScopesMask)
	// SECURITY: api_token actors must have a non-empty UserSub in the context.
	// Without this, downstream handlers (e.g. chat_handler.resolveUserSub) fall
	// back to client-controlled `req.UserSub` from the request body — which
	// allows an api_token holder with ScopeChat to create sessions / write
	// memories under any user_sub they choose (impersonation). The token name
	// (info.Name, e.g. "bb_xxx" or operator-declared "ai-assistant-proxy") is
	// the canonical service identity; treat it as the user_sub for any flow
	// that scopes data per end-user.
	ctx = domain.WithUserSub(ctx, info.Name)
	if info.TenantID != "" {
		if _, err := uuid.Parse(info.TenantID); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id claim"})
			return
		}
		ctx = domain.WithTenantID(ctx, info.TenantID)
	}
	next.ServeHTTP(w, r.WithContext(ctx))
}

func (m *AuthMiddleware) authenticateJWT(w http.ResponseWriter, r *http.Request, next http.Handler, token string) {
	if m.jwtVerifier == nil {
		slog.WarnContext(r.Context(), "auth: jwt verifier not configured",
			"path", r.URL.Path, "remote", r.RemoteAddr)
		writeUnauthorized(w, "invalid_token", "no jwt verifier configured")
		return
	}
	claims, err := m.jwtVerifier.Verify(token)
	if err != nil {
		slog.WarnContext(r.Context(), "auth: jwt verification failed",
			"path", r.URL.Path, "remote", r.RemoteAddr,
			"token_len", len(token), "error", err)
		writeUnauthorized(w, "invalid_token", "invalid or expired token")
		return
	}
	// Scopes come straight from the verifier. The HMAC verifier grants
	// ScopeAdmin only when role=="admin"; other tokens get 0 and will be
	// rejected by RequireScope. We do NOT default missing scopes to
	// ScopeAdmin here — doing so would re-enable cross-tenant admin hijack
	// for any validly-signed JWT without a role claim.
	scopes := claims.Scopes
	ctx := context.WithValue(r.Context(), ContextKeyActorType, "admin")
	ctx = context.WithValue(ctx, ContextKeyActorID, claims.Subject)
	ctx = domain.WithUserSub(ctx, claims.Subject)
	ctx = context.WithValue(ctx, ContextKeyScopes, scopes)
	if claims.TenantID != "" {
		if _, err := uuid.Parse(claims.TenantID); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id claim"})
			return
		}
		ctx = domain.WithTenantID(ctx, claims.TenantID)
	}
	next.ServeHTTP(w, r.WithContext(ctx))
}

// AuthenticateOptional attaches tenant/user context when a valid Bearer
// token is present. Unlike Authenticate, it does NOT reject the request on
// missing or invalid credentials — it simply passes through without
// populating the context. Use for public routes that serve different
// content based on tenant identity when known (e.g. widget CSP origins).
func (m *AuthMiddleware) AuthenticateOptional(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			next.ServeHTTP(w, r)
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")

		if strings.HasPrefix(token, "bb_") {
			hash := authprim.Hash(token)
			info, err := m.tokenVerifier.VerifyToken(r.Context(), hash)
			if err != nil || info.TenantID == "" {
				next.ServeHTTP(w, r)
				return
			}
			if _, err := uuid.Parse(info.TenantID); err != nil {
				next.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r.WithContext(domain.WithTenantID(r.Context(), info.TenantID)))
			return
		}

		if m.jwtVerifier == nil {
			next.ServeHTTP(w, r)
			return
		}
		claims, err := m.jwtVerifier.Verify(token)
		if err != nil || claims.TenantID == "" {
			next.ServeHTTP(w, r)
			return
		}
		if _, err := uuid.Parse(claims.TenantID); err != nil {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(domain.WithTenantID(r.Context(), claims.TenantID)))
	})
}

// RequireScope returns middleware that checks the authenticated user has the required scope.
func RequireScope(scope int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scopes, _ := r.Context().Value(ContextKeyScopes).(int)
			if scopes&ScopeAdmin != 0 || scopes&scope != 0 {
				next.ServeHTTP(w, r)
				return
			}
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		})
	}
}

// RequireAdminSession ensures only an admin-scoped JWT (not an API token)
// can access. The actor-type check alone is not enough: authenticateJWT
// stamps "admin" for every valid JWT, including audience-bound tokens whose
// scope claim grants less than full admin. Requiring the ScopeAdmin bit keeps
// such tokens away from admin-session-only routes (e.g. API-token minting).
func RequireAdminSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actorType, _ := r.Context().Value(ContextKeyActorType).(string)
		scopes, _ := r.Context().Value(ContextKeyScopes).(int)
		if actorType != "admin" || scopes&ScopeAdmin == 0 {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin session required"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
