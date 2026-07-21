package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/authprim"
	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

const asTestAudience = "https://engine.example/mcp"

// stubBaseVerifier returns a fixed sentinel so a test can assert that a token
// was routed to the base verifier rather than the strict AS path.
type stubBaseVerifier struct {
	called bool
	claims plugin.Claims
	err    error
}

func (s *stubBaseVerifier) Verify(string) (plugin.Claims, error) {
	s.called = true
	return s.claims, s.err
}

// signToken signs an EdDSA JWT with the given kid header (kid omitted when
// empty) and claims — full control for the security test matrix.
func signToken(t *testing.T, priv ed25519.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	if kid != "" {
		token.Header["kid"] = kid
	}
	signed, err := token.SignedString(priv)
	require.NoError(t, err)
	return signed
}

func newASKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return pub, priv, KeyID(pub)
}

func TestCompositeVerifier_RejectsASTokenWithoutAudience(t *testing.T) {
	pub, priv, kid := newASKeypair(t)
	base := &stubBaseVerifier{claims: plugin.Claims{Subject: "sentinel"}}
	cv := NewCompositeVerifier(base, map[string]ed25519.PublicKey{kid: pub}, asTestAudience)

	tok := signToken(t, priv, kid, jwt.MapClaims{
		"sub":   "user-1",
		"scope": "provision",
		"exp":   time.Now().Add(time.Minute).Unix(),
	})

	_, err := cv.Verify(tok)
	require.Error(t, err)
	assert.False(t, base.called, "AS token must not fall through to base verifier")
}

func TestCompositeVerifier_RejectsAdminScope(t *testing.T) {
	pub, priv, kid := newASKeypair(t)
	cv := NewCompositeVerifier(&stubBaseVerifier{}, map[string]ed25519.PublicKey{kid: pub}, asTestAudience)

	tok := signToken(t, priv, kid, jwt.MapClaims{
		"sub":   "user-1",
		"aud":   asTestAudience,
		"scope": "admin",
		"exp":   time.Now().Add(time.Minute).Unix(),
	})

	_, err := cv.Verify(tok)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disallowed scope")
}

func TestCompositeVerifier_RejectsTypClaim(t *testing.T) {
	pub, priv, kid := newASKeypair(t)
	cv := NewCompositeVerifier(&stubBaseVerifier{}, map[string]ed25519.PublicKey{kid: pub}, asTestAudience)

	tok := signToken(t, priv, kid, jwt.MapClaims{
		"sub":   "user-1",
		"aud":   asTestAudience,
		"scope": "provision",
		"typ":   "oauth_code",
		"exp":   time.Now().Add(time.Minute).Unix(),
	})

	_, err := cv.Verify(tok)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "typ")
}

func TestCompositeVerifier_RejectsUnknownKid(t *testing.T) {
	pub, priv, kid := newASKeypair(t)
	base := &stubBaseVerifier{claims: plugin.Claims{Subject: "sentinel"}}
	// Register the keypair under its real kid, but sign with a different kid.
	cv := NewCompositeVerifier(base, map[string]ed25519.PublicKey{kid: pub}, asTestAudience)

	tok := signToken(t, priv, "deadbeefdeadbeef", jwt.MapClaims{
		"sub":   "user-1",
		"aud":   asTestAudience,
		"scope": "provision",
		"exp":   time.Now().Add(time.Minute).Unix(),
	})

	_, err := cv.Verify(tok)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown key id")
	assert.False(t, base.called, "unknown-kid token must fail closed, not reach base")
}

func TestCompositeVerifier_ValidProvisionToken(t *testing.T) {
	pub, priv, kid := newASKeypair(t)
	cv := NewCompositeVerifier(&stubBaseVerifier{}, map[string]ed25519.PublicKey{kid: pub}, asTestAudience)

	tok := signToken(t, priv, kid, jwt.MapClaims{
		"sub":       "user-42",
		"tenant_id": "11111111-1111-1111-1111-111111111111",
		"aud":       asTestAudience,
		"scope":     "provision",
		"exp":       time.Now().Add(time.Minute).Unix(),
	})

	claims, err := cv.Verify(tok)
	require.NoError(t, err)
	assert.Equal(t, authprim.ScopeProvisionMask, claims.Scopes)
	assert.Equal(t, "user-42", claims.Subject)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", claims.TenantID)
	assert.Equal(t, 0, claims.Scopes&plugin.ScopeAdmin, "AS token must never carry admin bit")
}

func TestCompositeVerifier_ValidManageToken(t *testing.T) {
	pub, priv, kid := newASKeypair(t)
	cv := NewCompositeVerifier(&stubBaseVerifier{}, map[string]ed25519.PublicKey{kid: pub}, asTestAudience)

	tok := signToken(t, priv, kid, jwt.MapClaims{
		"sub":   "user-7",
		"aud":   asTestAudience,
		"scope": "manage",
		"exp":   time.Now().Add(time.Minute).Unix(),
	})

	claims, err := cv.Verify(tok)
	require.NoError(t, err)
	assert.Equal(t, authprim.ScopeManageMask, claims.Scopes)
	assert.Equal(t, 0, claims.Scopes&plugin.ScopeAdmin, "AS token must never carry admin bit")
}

func TestCompositeVerifier_NoKidRoutesToBase(t *testing.T) {
	pub, priv, kid := newASKeypair(t)
	sentinel := plugin.Claims{Subject: "base-sentinel", Scopes: plugin.ScopeAdmin}
	base := &stubBaseVerifier{claims: sentinel}
	cv := NewCompositeVerifier(base, map[string]ed25519.PublicKey{kid: pub}, asTestAudience)

	// A token with NO kid header — signed by any key, since the base stub does
	// not actually verify the signature. The point is the routing decision.
	tok := signToken(t, priv, "", jwt.MapClaims{
		"sub": "session-user",
		"exp": time.Now().Add(time.Minute).Unix(),
	})

	claims, err := cv.Verify(tok)
	require.NoError(t, err)
	assert.True(t, base.called, "no-kid token must route to base verifier")
	assert.Equal(t, sentinel, claims)
}

func TestCompositeVerifier_EmptyExpectedAudienceRejectsAS(t *testing.T) {
	pub, priv, kid := newASKeypair(t)
	cv := NewCompositeVerifier(&stubBaseVerifier{}, map[string]ed25519.PublicKey{kid: pub}, "")

	tok := signToken(t, priv, kid, jwt.MapClaims{
		"sub":   "user-1",
		"aud":   asTestAudience,
		"scope": "provision",
		"exp":   time.Now().Add(time.Minute).Unix(),
	})

	_, err := cv.Verify(tok)
	require.Error(t, err)
}
