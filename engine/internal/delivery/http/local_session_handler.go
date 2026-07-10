package http

import (
	"crypto/ed25519"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// LocalSessionHandler issues short-lived EdDSA-signed admin session tokens
// to unauthenticated callers — the CE "local admin" flow.
//
// Only wired into the router when AUTH_MODE=local (CE single-node installs).
// In external mode the route 404s; admin sessions are minted by the
// landing service and handed to the SPA via URL fragment.
//
// There is no username/password check: if the engine process itself is
// reachable, the caller is trusted (CE is expected to run behind an auth
// proxy or on a private network — same assumption as Postgres, Redis, etc.).
// The synthetic sub "local-admin" gives operations a stable actor identity
// for audit logs without needing a users table.
type LocalSessionHandler struct {
	privateKey ed25519.PrivateKey
	ttl        time.Duration
}

// NewLocalSessionHandler creates a handler that signs tokens with the given
// Ed25519 private key. ttl controls the token lifetime; pass 0 for the
// default (1 hour).
func NewLocalSessionHandler(privateKey ed25519.PrivateKey, ttl time.Duration) *LocalSessionHandler {
	if ttl <= 0 {
		ttl = time.Hour
	}
	return &LocalSessionHandler{privateKey: privateKey, ttl: ttl}
}

type localSessionResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresAt   string `json:"expires_at"`
	TokenType   string `json:"token_type"`
}

// Issue mints a new local-admin access token. POST /api/v1/auth/local-session.
func (h *LocalSessionHandler) Issue(w http.ResponseWriter, r *http.Request) {
	if h.privateKey == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "local session signing key missing"})
		return
	}

	now := time.Now()
	expiresAt := now.Add(h.ttl)

	claims := jwt.MapClaims{
		"sub":       "local-admin",
		"tenant_id": "",
		"iat":       now.Unix(),
		"exp":       expiresAt.Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signed, err := token.SignedString(h.privateKey)
	if err != nil {
		// Don't echo the underlying error — Ed25519 sign failures are
		// internal and the message could leak key-material details.
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to sign session token"})
		return
	}

	writeJSON(w, http.StatusOK, localSessionResponse{
		AccessToken: signed,
		ExpiresAt:   expiresAt.Format(time.RFC3339),
		TokenType:   "Bearer",
	})
}

// Refresh verifies the current Bearer local-session token and issues a fresh
// one with the same claims and a new TTL. Stateless — CE trusts the network
// perimeter, no revocation list needed.
// POST /api/v1/auth/local-session/refresh
func (h *LocalSessionHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	if h.privateKey == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "local session signing key missing"})
		return
	}

	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing bearer token"})
		return
	}
	tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

	pubKey := h.privateKey.Public()
	parsed, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return pubKey, nil
	}, jwt.WithExpirationRequired())
	if err != nil || !parsed.Valid {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
		return
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token claims"})
		return
	}

	sub, _ := claims["sub"].(string)
	tenantID, _ := claims["tenant_id"].(string)

	now := time.Now()
	expiresAt := now.Add(h.ttl)

	newClaims := jwt.MapClaims{
		"sub":       sub,
		"tenant_id": tenantID,
		"iat":       now.Unix(),
		"exp":       expiresAt.Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, newClaims)
	signed, err := token.SignedString(h.privateKey)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to sign session token"})
		return
	}

	writeJSON(w, http.StatusOK, localSessionResponse{
		AccessToken: signed,
		ExpiresAt:   expiresAt.Format(time.RFC3339),
		TokenType:   "Bearer",
	})
}
