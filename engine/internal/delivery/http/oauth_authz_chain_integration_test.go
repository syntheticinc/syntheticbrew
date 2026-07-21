package http

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/authprim"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/auth"
	pluginpkg "github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// rejectingBaseVerifier stands in for the session/plugin verifier: it rejects
// every token, so any success in these tests is attributable solely to the AS
// (kid-routed) strict path of the composite verifier.
type rejectingBaseVerifier struct{}

func (rejectingBaseVerifier) Verify(string) (pluginpkg.Claims, error) {
	return pluginpkg.Claims{}, errBaseReject
}

var errBaseReject = &baseRejectErr{}

type baseRejectErr struct{}

func (*baseRejectErr) Error() string { return "base verifier rejects" }

// rejectingTokenVerifier stands in for the bb_ API-token store: no bb_ tokens
// exist in these tests.
type rejectingTokenVerifier struct{}

func (rejectingTokenVerifier) VerifyToken(context.Context, string) (APITokenInfo, error) {
	return APITokenInfo{}, errBaseReject
}

// newAuthzChainServer wires the REAL auth middleware around a scope-gated
// endpoint, verifying tokens through the REAL composite verifier (AS key
// registered, base rejects everything). This is the production authorization
// path an OAuth access token traverses.
func newAuthzChainServer(t *testing.T, requiredScope int) (*httptest.Server, *auth.OAuthTokenSigner, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	const issuer = "https://engine.example"
	resource := issuer + "/api/v1/mcp/rpc"
	signer := auth.NewOAuthTokenSigner(priv, issuer)

	composite := auth.NewCompositeVerifier(
		rejectingBaseVerifier{},
		map[string]ed25519.PublicKey{signer.KID(): signer.PublicKey()},
		resource,
	)
	authMW := NewAuthMiddlewareWithVerifier(composite, rejectingTokenVerifier{})

	r := chi.NewRouter()
	r.With(authMW.Authenticate, RequireScope(requiredScope)).Get("/gated", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return httptest.NewServer(r), signer, resource
}

func doGet(t *testing.T, url, bearer string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

// TestOAuthToken_AuthorizesScopeGatedEndpoint proves a provision-scoped OAuth
// access token, verified through the real composite verifier + auth middleware,
// authorizes an endpoint gated on a provision-covered scope.
func TestOAuthToken_AuthorizesScopeGatedEndpoint(t *testing.T) {
	// ScopeMCPRead is part of ScopeProvisionMask, so a provision token passes.
	srv, signer, resource := newAuthzChainServer(t, authprim.ScopeMCPRead)
	defer srv.Close()

	token, err := signer.SignMCPAccessToken("agent-1", "", []string{"provision"}, resource)
	require.NoError(t, err)

	if code := doGet(t, srv.URL+"/gated", token); code != http.StatusOK {
		t.Fatalf("provision token on provision-covered endpoint: got %d, want 200", code)
	}
}

// TestOAuthToken_RejectedOnManageGatedEndpoint proves a provision token cannot
// reach a manage-gated (destructive) endpoint — the scope split holds through
// the real middleware.
func TestOAuthToken_RejectedOnManageGatedEndpoint(t *testing.T) {
	srv, signer, resource := newAuthzChainServer(t, authprim.ScopeManage)
	defer srv.Close()

	token, err := signer.SignMCPAccessToken("agent-1", "", []string{"provision"}, resource)
	require.NoError(t, err)

	if code := doGet(t, srv.URL+"/gated", token); code != http.StatusForbidden {
		t.Fatalf("provision token on manage-gated endpoint: got %d, want 403", code)
	}
}

// TestOAuthToken_ManageTokenReachesManageEndpoint confirms the positive manage
// path: a manage-scoped token clears a manage gate.
func TestOAuthToken_ManageTokenReachesManageEndpoint(t *testing.T) {
	srv, signer, resource := newAuthzChainServer(t, authprim.ScopeManage)
	defer srv.Close()

	token, err := signer.SignMCPAccessToken("agent-1", "", []string{"manage"}, resource)
	require.NoError(t, err)

	if code := doGet(t, srv.URL+"/gated", token); code != http.StatusOK {
		t.Fatalf("manage token on manage-gated endpoint: got %d, want 200", code)
	}
}

// TestOAuthAuthzChain_SecurityRejections covers SCC-01 (unauthenticated → 401),
// SCC-03 (garbage token → 401, not 500), and T1 (a typ-tagged blob replayed as
// a Bearer must never authenticate).
func TestOAuthAuthzChain_SecurityRejections(t *testing.T) {
	srv, signer, _ := newAuthzChainServer(t, authprim.ScopeMCPRead)
	defer srv.Close()

	t.Run("SCC-01 unauthenticated rejected", func(t *testing.T) {
		if code := doGet(t, srv.URL+"/gated", ""); code != http.StatusUnauthorized {
			t.Fatalf("no token: got %d, want 401", code)
		}
	})

	t.Run("SCC-03 garbage token rejected without 500", func(t *testing.T) {
		if code := doGet(t, srv.URL+"/gated", "not-a-jwt"); code != http.StatusUnauthorized {
			t.Fatalf("garbage token: got %d, want 401", code)
		}
	})

	t.Run("T1 typ-tagged blob replayed as bearer rejected", func(t *testing.T) {
		// An authorization-code blob carries the AS kid, so it routes to the
		// strict verifier, which rejects it (typ present, no aud).
		blob, err := signer.SignOAuthBlob("oauth_code", map[string]any{"sub": "agent-1"}, 0)
		require.NoError(t, err)
		if code := doGet(t, srv.URL+"/gated", blob); code != http.StatusUnauthorized {
			t.Fatalf("oauth_code blob as bearer: got %d, want 401", code)
		}
	})
}
