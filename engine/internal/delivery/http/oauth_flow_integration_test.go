package http

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/authprim"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/auth"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/oauthapprove"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/oauthauthorizeinfo"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/oauthregister"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/oauthtoken"
	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// memRefreshRepo is an in-memory RefreshTokenRepository for the OAuth flow test.
// It reproduces the two persistence contracts the token usecase relies on:
// code-JTI single-use (Store collision → domain.ErrOAuthCodeReplayed) and
// atomic single-row rotation (RotateRevoke returns the affected row count).
type memRefreshRepo struct {
	mu       sync.Mutex
	byHash   map[string]domain.OAuthRefreshToken
	codeJTIs map[string]string // code_jti → family_id
}

func newMemRefreshRepo() *memRefreshRepo {
	return &memRefreshRepo{byHash: map[string]domain.OAuthRefreshToken{}, codeJTIs: map[string]string{}}
}

func (m *memRefreshRepo) Store(_ context.Context, t domain.OAuthRefreshToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.CodeJTI != "" {
		if _, exists := m.codeJTIs[t.CodeJTI]; exists {
			return domain.ErrOAuthCodeReplayed
		}
		m.codeJTIs[t.CodeJTI] = t.FamilyID
	}
	m.byHash[t.TokenHash] = t
	return nil
}

func (m *memRefreshRepo) GetByHash(_ context.Context, hash string) (domain.OAuthRefreshToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.byHash[hash]
	if !ok {
		return domain.OAuthRefreshToken{}, assertNotFound
	}
	return t, nil
}

func (m *memRefreshRepo) RotateRevoke(_ context.Context, hash string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.byHash[hash]
	if !ok || t.RevokedAt != nil {
		return 0, nil
	}
	now := time.Now()
	t.RevokedAt = &now
	m.byHash[hash] = t
	return 1, nil
}

func (m *memRefreshRepo) RevokeFamily(_ context.Context, familyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for h, t := range m.byHash {
		if t.FamilyID == familyID && t.RevokedAt == nil {
			now := time.Now()
			t.RevokedAt = &now
			m.byHash[h] = t
		}
	}
	return nil
}

func (m *memRefreshRepo) FindFamilyByCodeJTI(_ context.Context, codeJTI string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if fam, ok := m.codeJTIs[codeJTI]; ok {
		return fam, nil
	}
	return "", assertNotFound
}

var assertNotFound = &notFoundErr{}

type notFoundErr struct{}

func (*notFoundErr) Error() string { return "not found" }

// oauthFlowHarness wires the real handler + usecases + signer behind httptest,
// with a stub session middleware injecting the admin subject/tenant on the
// consent endpoints.
type oauthFlowHarness struct {
	server   *httptest.Server
	signer   *auth.OAuthTokenSigner
	resource string
	sub      string
	tenant   string
}

func newOAuthFlowHarness(t *testing.T) *oauthFlowHarness {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	const issuer = "https://engine.example"
	resource := issuer + "/api/v1/mcp/rpc"
	signer := auth.NewOAuthTokenSigner(priv, issuer)
	repo := newMemRefreshRepo()

	registerUC := oauthregister.New(signer)
	authorizeInfoUC := oauthauthorizeinfo.New(signer, signer)
	approveUC := oauthapprove.New(signer, signer)
	tokenUC := oauthtoken.New(signer, signer, signer, repo, nil, resource)
	h := NewOAuthHandler(issuer, issuer+"/admin/oauth/consent", registerUC, authorizeInfoUC, approveUC, tokenUC)

	sub := "admin-user"
	tenant := uuid.New().String()
	withSession := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := domain.WithUserSub(r.Context(), sub)
			ctx = domain.WithTenantID(ctx, tenant)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	router := chi.NewRouter()
	router.Post("/oauth/register", h.Register)
	router.Post("/oauth/token", h.Token)
	router.With(withSession).Get("/api/v1/oauth/authorize-info", h.AuthorizeInfo)
	router.With(withSession).Post("/api/v1/oauth/approve", h.Approve)

	return &oauthFlowHarness{
		server:   httptest.NewServer(router),
		signer:   signer,
		resource: resource,
		sub:      sub,
		tenant:   tenant,
	}
}

// TestOAuthFullFlow exercises the whole authorization-code + refresh flow end to
// end through the real HTTP handler, and asserts the minted access token
// resolves through the composite verifier to the provision scope mask.
func TestOAuthFullFlow(t *testing.T) {
	h := newOAuthFlowHarness(t)
	defer h.server.Close()

	const redirectURI = "https://client.example/cb"
	verifier := "pkce-code-verifier-of-sufficient-length-abcdef012345"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	// 1. Register a public client.
	clientID := h.register(t, redirectURI)
	require.NotEmpty(t, clientID)

	// 2. authorize-info with the stub admin session → consent nonce.
	nonce := h.authorizeInfo(t, clientID, redirectURI, "provision", challenge)
	require.NotEmpty(t, nonce, "consent nonce must be minted for an authenticated session")

	// 3. approve → authorization-code redirect.
	code := h.approve(t, nonce, clientID, redirectURI, "provision", challenge, verifier)
	require.NotEmpty(t, code)

	// 4. token: authorization_code → access + refresh.
	tok := h.token(t, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	})
	require.NotEmpty(t, tok.AccessToken)
	require.NotEmpty(t, tok.RefreshToken)
	assert.Equal(t, "provision", tok.Scope)

	// 5. The access token must resolve through the composite verifier to the
	// provision mask — never admin — and carry the session subject/tenant.
	composite := auth.NewCompositeVerifier(rejectingBase{}, map[string]ed25519.PublicKey{h.signer.KID(): h.signer.PublicKey()}, h.resource)
	claims, err := composite.Verify(tok.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, authprim.ScopeProvisionMask, claims.Scopes)
	assert.Zero(t, claims.Scopes&authprim.ScopeAdmin, "AS token must never carry admin")
	assert.Equal(t, h.sub, claims.Subject)
	assert.Equal(t, h.tenant, claims.TenantID)

	// 6. refresh_token → rotated pair.
	rotated := h.token(t, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {tok.RefreshToken},
		"client_id":     {clientID},
	})
	require.NotEmpty(t, rotated.AccessToken)
	require.NotEmpty(t, rotated.RefreshToken)
	assert.NotEqual(t, tok.RefreshToken, rotated.RefreshToken, "refresh token must rotate")

	// 7. Reuse of the now-rotated refresh token → invalid_grant.
	code2, body := h.tokenRaw(t, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {tok.RefreshToken},
		"client_id":     {clientID},
	})
	assert.Equal(t, http.StatusBadRequest, code2)
	assert.Equal(t, "invalid_grant", body["error"])
}

// --- harness step helpers ---

func (h *oauthFlowHarness) register(t *testing.T, redirectURI string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"redirect_uris": []string{redirectURI},
		"client_name":   "Test MCP Client",
	})
	resp, err := http.Post(h.server.URL+"/oauth/register", "application/json", strings.NewReader(string(body)))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var out struct {
		ClientID string `json:"client_id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out.ClientID
}

func (h *oauthFlowHarness) authorizeInfo(t *testing.T, clientID, redirectURI, scope, challenge string) string {
	t.Helper()
	q := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {scope},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	resp, err := http.Get(h.server.URL + "/api/v1/oauth/authorize-info?" + q.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out struct {
		ConsentNonce string `json:"consent_nonce"`
		Scopes       []string
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out.ConsentNonce
}

func (h *oauthFlowHarness) approve(t *testing.T, nonce, clientID, redirectURI, scope, challenge, _ string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"consent_nonce":         nonce,
		"client_id":             clientID,
		"redirect_uri":          redirectURI,
		"scope":                 scope,
		"state":                 "state-xyz",
		"code_challenge":        challenge,
		"code_challenge_method": "S256",
		"resource":              h.resource,
		"approved_scopes":       []string{"provision"},
	})
	resp, err := http.Post(h.server.URL+"/api/v1/oauth/approve", "application/json", strings.NewReader(string(body)))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out struct {
		RedirectURL string `json:"redirect_url"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	u, err := url.Parse(out.RedirectURL)
	require.NoError(t, err)
	assert.Equal(t, "state-xyz", u.Query().Get("state"))
	return u.Query().Get("code")
}

type tokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

func (h *oauthFlowHarness) token(t *testing.T, form url.Values) tokenResp {
	t.Helper()
	resp, err := http.PostForm(h.server.URL+"/oauth/token", form)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out tokenResp
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

func (h *oauthFlowHarness) tokenRaw(t *testing.T, form url.Values) (int, map[string]string) {
	t.Helper()
	resp, err := http.PostForm(h.server.URL+"/oauth/token", form)
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return resp.StatusCode, out
}

// rejectingBase is a base verifier that rejects everything — it proves the AS
// token is resolved by the strict AS path (via kid), never delegated to the base.
type rejectingBase struct{}

func (rejectingBase) Verify(string) (plugin.Claims, error) {
	return plugin.Claims{}, assertNotFound
}
