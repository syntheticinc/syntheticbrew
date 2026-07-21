package oauthapprove

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/auth"
)

const (
	testUser        = "user-1"
	testRedirect    = "https://client.example/callback"
	testChallenge   = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	testConsentTyp  = "oauth_consent"
	testConsentTTL  = 10 * time.Minute
	testClientScope = domain.OAuthScopeProvision + " " + domain.OAuthScopeManage
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

func mintConsent(t *testing.T, s *auth.OAuthTokenSigner, sub, clientID string) string {
	t.Helper()
	nonce, err := s.SignOAuthBlob(testConsentTyp, map[string]any{
		"sub": sub,
		"req": domain.ConsentRequestHash(clientID, testRedirect, testClientScope, testChallenge),
	}, testConsentTTL)
	require.NoError(t, err)
	return nonce
}

func baseInput(clientID, nonce string) Input {
	return Input{
		UserID:              testUser,
		TenantID:            "t1",
		ConsentNonce:        nonce,
		ClientID:            clientID,
		RedirectURI:         testRedirect,
		Scope:               testClientScope,
		State:               "xyz",
		CodeChallenge:       testChallenge,
		CodeChallengeMethod: "S256",
		Resource:            "https://engine.example/api/v1/mcp/rpc",
	}
}

func codeFromRedirect(t *testing.T, redirectURL string) string {
	t.Helper()
	u, err := url.Parse(redirectURL)
	require.NoError(t, err)
	return u.Query().Get("code")
}

func TestApprove_ManageGrantedWhenApproved(t *testing.T) {
	s := newSigner(t)
	uc := New(s, s)
	cid := mintClientID(t, s)
	in := baseInput(cid, mintConsent(t, s, testUser, cid))
	in.ApprovedScopes = []string{domain.OAuthScopeManage}

	out, err := uc.Execute(in)
	require.NoError(t, err)

	code := codeFromRedirect(t, out.RedirectURL)
	require.NotEmpty(t, code)
	claims, err := s.VerifyOAuthBlob(code, "oauth_code")
	require.NoError(t, err)
	assert.Equal(t, testClientScope, claims["scope"])
	assert.Equal(t, "t1", claims["tenant_id"])
	assert.Equal(t, testUser, claims["sub"])
}

func TestApprove_ProvisionOnlyWhenManageNotApproved(t *testing.T) {
	s := newSigner(t)
	uc := New(s, s)
	cid := mintClientID(t, s)
	in := baseInput(cid, mintConsent(t, s, testUser, cid))
	in.ApprovedScopes = nil

	out, err := uc.Execute(in)
	require.NoError(t, err)

	claims, err := s.VerifyOAuthBlob(codeFromRedirect(t, out.RedirectURL), "oauth_code")
	require.NoError(t, err)
	assert.Equal(t, domain.OAuthScopeProvision, claims["scope"])
}

func TestApprove_EmptyTenantPreserved(t *testing.T) {
	s := newSigner(t)
	uc := New(s, s)
	cid := mintClientID(t, s)
	in := baseInput(cid, mintConsent(t, s, testUser, cid))
	in.TenantID = ""

	out, err := uc.Execute(in)
	require.NoError(t, err)

	claims, err := s.VerifyOAuthBlob(codeFromRedirect(t, out.RedirectURL), "oauth_code")
	require.NoError(t, err)
	assert.Equal(t, "", claims["tenant_id"], "empty tenant must not default to the user sub")
}

func TestApprove_DenyRedirectsAccessDenied(t *testing.T) {
	s := newSigner(t)
	uc := New(s, s)
	cid := mintClientID(t, s)
	in := baseInput(cid, mintConsent(t, s, testUser, cid))
	in.Deny = true

	out, err := uc.Execute(in)
	require.NoError(t, err)

	u, err := url.Parse(out.RedirectURL)
	require.NoError(t, err)
	assert.Equal(t, "access_denied", u.Query().Get("error"))
	assert.Empty(t, u.Query().Get("code"))
}

func TestApprove_MissingConsentNonce(t *testing.T) {
	s := newSigner(t)
	uc := New(s, s)
	in := baseInput(mintClientID(t, s), "")

	_, err := uc.Execute(in)
	var oerr *domain.OAuthError
	require.ErrorAs(t, err, &oerr)
	assert.Equal(t, "invalid_request", oerr.Code)
}

func TestApprove_ConsentNonceSubMismatch(t *testing.T) {
	s := newSigner(t)
	uc := New(s, s)
	// Nonce minted for a different subject than the one approving.
	cid := mintClientID(t, s)
	in := baseInput(cid, mintConsent(t, s, "someone-else", cid))

	_, err := uc.Execute(in)
	var oerr *domain.OAuthError
	require.ErrorAs(t, err, &oerr)
	assert.Equal(t, "invalid_request", oerr.Code)
}

func TestApprove_ConsentNonceRequestMismatch(t *testing.T) {
	s := newSigner(t)
	uc := New(s, s)
	// A nonce bound to one authorization request must not be replayable to
	// approve a different one. Here the nonce's req hash is bound to a
	// different redirect_uri than the request being approved.
	cid := mintClientID(t, s)
	mismatchedNonce, err := s.SignOAuthBlob(testConsentTyp, map[string]any{
		"sub": testUser,
		"req": domain.ConsentRequestHash(cid, "https://evil.example/callback", testClientScope, testChallenge),
	}, testConsentTTL)
	require.NoError(t, err)
	in := baseInput(cid, mismatchedNonce)

	_, err = uc.Execute(in)
	var oerr *domain.OAuthError
	require.ErrorAs(t, err, &oerr)
	assert.Equal(t, "invalid_request", oerr.Code)
}

func TestApprove_ConsentNonceScopeMismatch(t *testing.T) {
	s := newSigner(t)
	uc := New(s, s)
	// A nonce minted for a provision-only request must not approve a request
	// that additionally asks for manage — the nonce is bound to the scope set.
	cid := mintClientID(t, s)
	provisionOnlyNonce, err := s.SignOAuthBlob(testConsentTyp, map[string]any{
		"sub": testUser,
		"req": domain.ConsentRequestHash(cid, testRedirect, domain.OAuthScopeProvision, testChallenge),
	}, testConsentTTL)
	require.NoError(t, err)
	in := baseInput(cid, provisionOnlyNonce) // baseInput.Scope = "provision manage"

	_, err = uc.Execute(in)
	var oerr *domain.OAuthError
	require.ErrorAs(t, err, &oerr)
	assert.Equal(t, "invalid_request", oerr.Code)
}

func TestApprove_ConsentNonceWrongTyp(t *testing.T) {
	s := newSigner(t)
	uc := New(s, s)
	// A client blob replayed as a consent nonce must be rejected on typ.
	in := baseInput(mintClientID(t, s), mintClientID(t, s))

	_, err := uc.Execute(in)
	var oerr *domain.OAuthError
	require.ErrorAs(t, err, &oerr)
	assert.Equal(t, "invalid_request", oerr.Code)
}
