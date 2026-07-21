package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/authprim"
)

func newSigner(t *testing.T) *OAuthTokenSigner {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return NewOAuthTokenSigner(priv, "https://engine.example")
}

func TestSignMCPAccessToken_SetsKidHeaderAndClaims(t *testing.T) {
	s := newSigner(t)
	tok, err := s.SignMCPAccessToken("user-1", "tenant-1", []string{"provision"}, asTestAudience)
	require.NoError(t, err)

	parsed, err := jwt.Parse(tok, func(*jwt.Token) (interface{}, error) { return s.PublicKey(), nil })
	require.NoError(t, err)

	assert.Equal(t, s.KID(), parsed.Header["kid"])

	mc, ok := parsed.Claims.(jwt.MapClaims)
	require.True(t, ok)
	assert.Equal(t, "user-1", mc["sub"])
	assert.Equal(t, "tenant-1", mc["tenant_id"])
	assert.Equal(t, "provision", mc["scope"])
	assert.Equal(t, asTestAudience, mc["aud"])
	_, hasTyp := mc["typ"]
	assert.False(t, hasTyp, "access token must not carry typ")
	_, hasRole := mc["role"]
	assert.False(t, hasRole, "access token must not carry role")
}

func TestKID_MatchesKeyIDOfPublicKey(t *testing.T) {
	s := newSigner(t)
	assert.Equal(t, KeyID(s.PublicKey()), s.KID())
	assert.Len(t, s.KID(), 16)
}

func TestSignVerifyOAuthBlob_TypEnforced(t *testing.T) {
	s := newSigner(t)
	blob, err := s.SignOAuthBlob("oauth_code", map[string]any{"cid": "abc"}, time.Minute)
	require.NoError(t, err)

	claims, err := s.VerifyOAuthBlob(blob, "oauth_code")
	require.NoError(t, err)
	assert.Equal(t, "abc", claims["cid"])

	// A blob minted as oauth_code must not verify as oauth_client.
	_, err = s.VerifyOAuthBlob(blob, "oauth_client")
	require.Error(t, err)
}

func TestSignOAuthBlob_SetsKidHeader(t *testing.T) {
	s := newSigner(t)
	blob, err := s.SignOAuthBlob("oauth_client", map[string]any{"client_name": "cli"}, 0)
	require.NoError(t, err)

	parsed, _, err := jwt.NewParser().ParseUnverified(blob, jwt.MapClaims{})
	require.NoError(t, err)
	assert.Equal(t, s.KID(), parsed.Header["kid"])
}

func TestNewOpaqueRefreshToken(t *testing.T) {
	s := newSigner(t)
	tok, hash, err := s.NewOpaqueRefreshToken()
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(tok, "rt_"))
	assert.Equal(t, authprim.Hash(tok), hash)

	// Distinct tokens each call.
	tok2, _, err := s.NewOpaqueRefreshToken()
	require.NoError(t, err)
	assert.NotEqual(t, tok, tok2)
}
