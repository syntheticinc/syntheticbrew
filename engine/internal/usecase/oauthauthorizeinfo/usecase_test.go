package oauthauthorizeinfo

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/auth"
)

const (
	testRedirect  = "https://client.example/callback"
	testChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
)

func newSigner(t *testing.T) *auth.OAuthTokenSigner {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return auth.NewOAuthTokenSigner(priv, "https://engine.example")
}

func mintClientID(t *testing.T, s *auth.OAuthTokenSigner) string {
	t.Helper()
	cid, err := s.SignOAuthBlob("oauth_client", map[string]any{
		"client_name":   "Test Client",
		"redirect_uris": []string{testRedirect},
	}, 0)
	require.NoError(t, err)
	return cid
}

func baseInput(clientID string) Input {
	return Input{
		ClientID:            clientID,
		RedirectURI:         testRedirect,
		Scope:               domain.OAuthScopeProvision + " " + domain.OAuthScopeManage,
		CodeChallenge:       testChallenge,
		CodeChallengeMethod: "S256",
	}
}

func TestAuthorizeInfo_ValidNoSession(t *testing.T) {
	s := newSigner(t)
	uc := New(s, s)

	out, err := uc.Execute(baseInput(mintClientID(t, s)))
	require.NoError(t, err)
	assert.Equal(t, "Test Client", out.ClientName)
	assert.True(t, out.RedirectURIValid)
	assert.Equal(t, []string{domain.OAuthScopeProvision, domain.OAuthScopeManage}, out.Scopes)
	assert.Empty(t, out.ConsentNonce, "no nonce without a session subject")
}

func TestAuthorizeInfo_MintsConsentNonceForSession(t *testing.T) {
	s := newSigner(t)
	uc := New(s, s)
	in := baseInput(mintClientID(t, s))
	in.SessionSub = "user-1"

	out, err := uc.Execute(in)
	require.NoError(t, err)
	require.NotEmpty(t, out.ConsentNonce)

	claims, err := s.VerifyOAuthBlob(out.ConsentNonce, "oauth_consent")
	require.NoError(t, err)
	assert.Equal(t, "user-1", claims["sub"])
}

func TestAuthorizeInfo_InvalidClientID(t *testing.T) {
	s := newSigner(t)
	uc := New(s, s)
	in := baseInput("not-a-valid-blob")

	_, err := uc.Execute(in)
	var oerr *domain.OAuthError
	require.ErrorAs(t, err, &oerr)
	assert.Equal(t, "invalid_client", oerr.Code)
}

func TestAuthorizeInfo_BadPKCEMethod(t *testing.T) {
	s := newSigner(t)
	uc := New(s, s)
	in := baseInput(mintClientID(t, s))
	in.CodeChallengeMethod = "plain"

	_, err := uc.Execute(in)
	var oerr *domain.OAuthError
	require.ErrorAs(t, err, &oerr)
	assert.Equal(t, "invalid_request", oerr.Code)
}
