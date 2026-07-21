package oauthregister

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/auth"
)

func newSigner(t *testing.T) *auth.OAuthTokenSigner {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return auth.NewOAuthTokenSigner(priv, "https://engine.example")
}

func TestRegister_ValidReturnsSignedClientID(t *testing.T) {
	s := newSigner(t)
	uc := New(s)

	out, err := uc.Execute(Input{
		RedirectURIs: []string{"https://client.example/callback"},
		ClientName:   "Test Client",
	})
	require.NoError(t, err)
	require.NotEmpty(t, out.ClientID)

	claims, err := s.VerifyOAuthBlob(out.ClientID, "oauth_client")
	require.NoError(t, err)
	assert.Equal(t, "Test Client", claims["client_name"])
}

func TestRegister_Errors(t *testing.T) {
	tests := []struct {
		name         string
		redirectURIs []string
		wantCode     string
	}{
		{"no redirect uris", nil, "invalid_redirect_uri"},
		{"plain http non-loopback", []string{"http://client.example/cb"}, "invalid_redirect_uri"},
		{"custom scheme without dot", []string{"myapp://callback"}, "invalid_redirect_uri"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc := New(newSigner(t))
			_, err := uc.Execute(Input{RedirectURIs: tt.redirectURIs})
			var oerr *domain.OAuthError
			require.ErrorAs(t, err, &oerr)
			assert.Equal(t, tt.wantCode, oerr.Code)
		})
	}
}

func TestRegister_LoopbackAndCustomSchemeAccepted(t *testing.T) {
	uc := New(newSigner(t))
	out, err := uc.Execute(Input{
		RedirectURIs: []string{"http://127.0.0.1:1455/cb", "cursor://host.tld/cb"},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, out.ClientID)
}
