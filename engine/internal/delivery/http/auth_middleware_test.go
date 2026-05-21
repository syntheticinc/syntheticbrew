package http

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/authprim"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/auth"
)

// testKeypair holds the Ed25519 keys used by every test in this file. It is
// generated once per test so each case is hermetic.
type testKeypair struct {
	public  ed25519.PublicKey
	private ed25519.PrivateKey
}

func newTestKeypair(t *testing.T) testKeypair {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return testKeypair{public: pub, private: priv}
}

// newTestAuthMiddleware wires a fresh EdDSA-based AuthMiddleware the same way
// the production server does (Wave 1+7): verifier built from an Ed25519
// public key, no fallback to HMAC. Returns the middleware plus the private
// key so individual test cases can sign arbitrary tokens.
func newTestAuthMiddleware(t *testing.T, tokenVerifier APITokenVerifier) (*AuthMiddleware, testKeypair) {
	t.Helper()
	kp := newTestKeypair(t)
	verifier, err := auth.NewEdDSAVerifier(kp.public)
	require.NoError(t, err)
	return NewAuthMiddlewareWithVerifier(verifier, tokenVerifier), kp
}

type mockTokenVerifier struct {
	tokens map[string]APITokenInfo
}

func newMockTokenVerifier() *mockTokenVerifier {
	return &mockTokenVerifier{
		tokens: make(map[string]APITokenInfo),
	}
}

func (m *mockTokenVerifier) addToken(rawToken string, name string, scopes int) {
	hash := authprim.Hash(rawToken)
	m.tokens[hash] = APITokenInfo{Name: name, ScopesMask: scopes}
}

func (m *mockTokenVerifier) VerifyToken(_ context.Context, tokenHash string) (APITokenInfo, error) {
	t, ok := m.tokens[tokenHash]
	if !ok {
		return APITokenInfo{}, fmt.Errorf("token not found")
	}
	return t, nil
}

// signTestJWT mints an Ed25519-signed token with a mandatory `exp` claim.
// The verifier rejects tokens without `exp`; every test supplies one.
func signTestJWT(t *testing.T, privateKey ed25519.PrivateKey, subject string, expiry time.Duration) string {
	t.Helper()
	claims := jwt.MapClaims{
		"sub": subject,
		"exp": time.Now().Add(expiry).Unix(),
		"iat": time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signed, err := token.SignedString(privateKey)
	require.NoError(t, err)
	return signed
}

func TestAuthMiddleware_NoAuthHeader(t *testing.T) {
	verifier := newMockTokenVerifier()
	mw, _ := newTestAuthMiddleware(t, verifier)

	handler := mw.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "unauthorized")
}

func TestAuthMiddleware_InvalidBearerFormat(t *testing.T) {
	verifier := newMockTokenVerifier()
	mw, _ := newTestAuthMiddleware(t, verifier)

	handler := mw.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic abc123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthMiddleware_ValidJWT(t *testing.T) {
	verifier := newMockTokenVerifier()
	mw, kp := newTestAuthMiddleware(t, verifier)

	var capturedActorType, capturedActorID string
	var capturedScopes int

	handler := mw.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedActorType, _ = r.Context().Value(ContextKeyActorType).(string)
		capturedActorID, _ = r.Context().Value(ContextKeyActorID).(string)
		capturedScopes, _ = r.Context().Value(ContextKeyScopes).(int)
		w.WriteHeader(http.StatusOK)
	}))

	token := signTestJWT(t, kp.private, "admin-user", time.Hour)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "admin", capturedActorType)
	assert.Equal(t, "admin-user", capturedActorID)
	assert.Equal(t, ScopeAdmin, capturedScopes)
}

func TestAuthMiddleware_ExpiredJWT(t *testing.T) {
	verifier := newMockTokenVerifier()
	mw, kp := newTestAuthMiddleware(t, verifier)

	handler := mw.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	token := signTestJWT(t, kp.private, "admin-user", -time.Hour)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_token")
}

func TestAuthMiddleware_WrongKeyJWT(t *testing.T) {
	verifier := newMockTokenVerifier()
	mw, _ := newTestAuthMiddleware(t, verifier)

	// Sign with an unrelated keypair — verifier rejects.
	other := newTestKeypair(t)
	token := signTestJWT(t, other.private, "admin-user", time.Hour)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	mw.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthMiddleware_ValidAPIToken(t *testing.T) {
	verifier := newMockTokenVerifier()
	rawToken := "bb_abc123def456"
	verifier.addToken(rawToken, "my-cli-token", ScopeChat|ScopeTasks)

	mw, _ := newTestAuthMiddleware(t, verifier)

	var capturedActorType, capturedActorID string
	var capturedScopes int

	handler := mw.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedActorType, _ = r.Context().Value(ContextKeyActorType).(string)
		capturedActorID, _ = r.Context().Value(ContextKeyActorID).(string)
		capturedScopes, _ = r.Context().Value(ContextKeyScopes).(int)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "api_token", capturedActorType)
	assert.Equal(t, "my-cli-token", capturedActorID)
	assert.Equal(t, ScopeChat|ScopeTasks, capturedScopes)
}

func TestAuthMiddleware_InvalidAPIToken(t *testing.T) {
	verifier := newMockTokenVerifier()
	mw, _ := newTestAuthMiddleware(t, verifier)

	handler := mw.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer bb_unknown_token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_token")
}

func TestRequireScope_Allowed(t *testing.T) {
	tests := []struct {
		name        string
		scopes      int
		required    int
		expectAllow bool
	}{
		{"admin has all", ScopeAdmin, ScopeChat, true},
		{"admin bypasses agents_write", ScopeAdmin, ScopeAgentsWrite, true},
		{"admin bypasses models_write", ScopeAdmin, ScopeModelsWrite, true},
		{"admin bypasses mcp_write", ScopeAdmin, ScopeMCPWrite, true},
		{"exact scope", ScopeChat, ScopeChat, true},
		{"multiple scopes", ScopeChat | ScopeTasks, ScopeTasks, true},
		{"missing scope", ScopeChat, ScopeTasks, false},
		{"no scopes", 0, ScopeChat, false},
		{"agents_read allows agents_read", ScopeAgentsRead, ScopeAgentsRead, true},
		{"agents_read denies agents_write", ScopeAgentsRead, ScopeAgentsWrite, false},
		{"agents_write allows agents_write", ScopeAgentsWrite, ScopeAgentsWrite, true},
		{"models_read allows models_read", ScopeModelsRead, ScopeModelsRead, true},
		{"models_read denies models_write", ScopeModelsRead, ScopeModelsWrite, false},
		{"models_write allows models_write", ScopeModelsWrite, ScopeModelsWrite, true},
		{"mcp_read allows mcp_read", ScopeMCPRead, ScopeMCPRead, true},
		{"mcp_read denies mcp_write", ScopeMCPRead, ScopeMCPWrite, false},
		{"mcp_write allows mcp_write", ScopeMCPWrite, ScopeMCPWrite, true},
		{"triggers_read allows triggers_read", ScopeTriggersRead, ScopeTriggersRead, true},
		{"triggers_read denies triggers_write", ScopeTriggersRead, ScopeTriggersWrite, false},
		{"triggers_write allows triggers_write", ScopeTriggersWrite, ScopeTriggersWrite, true},
		{"combined read scopes", ScopeAgentsRead | ScopeModelsRead | ScopeMCPRead, ScopeModelsRead, true},
		{"combined read denies write", ScopeAgentsRead | ScopeModelsRead, ScopeAgentsWrite, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var called bool
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})

			handler := RequireScope(tt.required)(inner)

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			ctx := context.WithValue(req.Context(), ContextKeyScopes, tt.scopes)
			req = req.WithContext(ctx)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if tt.expectAllow {
				assert.True(t, called)
				assert.Equal(t, http.StatusOK, rec.Code)
			} else {
				assert.False(t, called)
				assert.Equal(t, http.StatusForbidden, rec.Code)
			}
		})
	}
}

func TestRequireAdminSession(t *testing.T) {
	tests := []struct {
		name        string
		actorType   string
		expectAllow bool
	}{
		{"admin allowed", "admin", true},
		{"api_token denied", "api_token", false},
		{"empty denied", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var called bool
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})

			handler := RequireAdminSession(inner)

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			ctx := context.WithValue(req.Context(), ContextKeyActorType, tt.actorType)
			req = req.WithContext(ctx)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if tt.expectAllow {
				assert.True(t, called)
			} else {
				assert.False(t, called)
				assert.Equal(t, http.StatusForbidden, rec.Code)
			}
		})
	}
}

func TestSha256Hash(t *testing.T) {
	hash := authprim.Hash("bb_test123")
	require.NotEmpty(t, hash)
	assert.Len(t, hash, 64) // SHA-256 hex = 64 chars

	// Same input produces same hash
	assert.Equal(t, hash, authprim.Hash("bb_test123"))

	// Different input produces different hash
	assert.NotEqual(t, hash, authprim.Hash("bb_test456"))
}

// captureLogs swaps slog.Default for the duration of t with a JSON handler
// writing into a returned bytes.Buffer. Tests use it to assert that an auth
// failure path emitted the expected WARN record.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// TestAuthMiddleware_LogsJWTVerifyFailure is the diagnostic regression for the
// 2026-04-27 prod incident: cloud chat returned 401 invalid_token but engine
// silently swallowed the verifier error so the operator had no idea whether
// the token was expired, signed with the wrong key, malformed, or missing
// claims. The middleware now emits a WARN with the underlying error.
func TestAuthMiddleware_LogsJWTVerifyFailure(t *testing.T) {
	logs := captureLogs(t)

	verifier := newMockTokenVerifier()
	mw, _ := newTestAuthMiddleware(t, verifier)

	// Sign with an unrelated keypair — verifier rejects with a clear cause.
	other := newTestKeypair(t)
	token := signTestJWT(t, other.private, "admin-user", time.Hour)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/assistant/chat", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	mw.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_token")

	out := logs.String()
	assert.Contains(t, out, "auth: jwt verification failed",
		"expected WARN log on JWT verify failure, got: %s", out)
	assert.Contains(t, out, "/api/v1/admin/assistant/chat",
		"expected request path in log, got: %s", out)
	assert.Contains(t, out, `"level":"WARN"`,
		"expected WARN level, got: %s", out)
}

// TestAuthMiddleware_LogsExpiredJWT exercises the same logging path with the
// most common cause of "invalid_token" in prod — an expired admin session.
// The captured error text must mention expiry so operators can distinguish
// it from key-mismatch or signature-tampering at a glance.
func TestAuthMiddleware_LogsExpiredJWT(t *testing.T) {
	logs := captureLogs(t)

	verifier := newMockTokenVerifier()
	mw, kp := newTestAuthMiddleware(t, verifier)

	token := signTestJWT(t, kp.private, "admin-user", -time.Hour)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/assistant/chat", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	mw.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	out := logs.String()
	assert.Contains(t, out, "auth: jwt verification failed",
		"expected WARN log on expired JWT, got: %s", out)
	assert.Contains(t, strings.ToLower(out), "expired",
		"expected expiry mention in log error, got: %s", out)
}

// TestAuthMiddleware_LogsBadAPIToken covers the bb_-prefix branch: a 401
// invalid_token from a stale CLI token must surface the verifier's reason
// in the log instead of the silent reject the operator used to see.
func TestAuthMiddleware_LogsBadAPIToken(t *testing.T) {
	logs := captureLogs(t)

	verifier := newMockTokenVerifier()
	mw, _ := newTestAuthMiddleware(t, verifier)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer bb_unknown_token")
	rec := httptest.NewRecorder()

	mw.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	out := logs.String()
	assert.Contains(t, out, "auth: api token verification failed",
		"expected WARN log on bad api token, got: %s", out)
}
