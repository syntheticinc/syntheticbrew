package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/oauthapprove"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/oauthauthorizeinfo"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/oauthregister"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/oauthtoken"
)

// --- usecase stubs ---

type stubRegister struct {
	out *oauthregister.Output
	err error
}

func (s stubRegister) Execute(oauthregister.Input) (*oauthregister.Output, error) {
	return s.out, s.err
}

type stubAuthorizeInfo struct {
	out     *oauthauthorizeinfo.Output
	err     error
	gotSess string
}

func (s *stubAuthorizeInfo) Execute(in oauthauthorizeinfo.Input) (*oauthauthorizeinfo.Output, error) {
	s.gotSess = in.SessionSub
	return s.out, s.err
}

type stubApprove struct {
	out   *oauthapprove.Output
	err   error
	gotIn oauthapprove.Input
}

func (s *stubApprove) Execute(in oauthapprove.Input) (*oauthapprove.Output, error) {
	s.gotIn = in
	return s.out, s.err
}

type stubToken struct {
	out *oauthtoken.Output
	err error
}

func (s stubToken) Execute(context.Context, oauthtoken.Input) (*oauthtoken.Output, error) {
	return s.out, s.err
}

func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(body, &m))
	return m
}

func TestOAuthHandler_Metadata(t *testing.T) {
	h := NewOAuthHandler("https://engine.example/", "https://app.example/consent", nil, nil, nil, nil)
	rec := httptest.NewRecorder()
	h.Metadata(rec, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	body := decodeBody(t, rec.Body.Bytes())
	assert.Equal(t, "https://engine.example", body["issuer"])
	assert.Equal(t, "https://app.example/consent", body["authorization_endpoint"])
	assert.Equal(t, "https://engine.example/oauth/token", body["token_endpoint"])
	assert.Equal(t, "https://engine.example/oauth/register", body["registration_endpoint"])
}

func TestOAuthHandler_Register(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		uc         stubRegister
		wantStatus int
		wantKey    string
		wantVal    string
	}{
		{
			name:       "happy",
			body:       `{"redirect_uris":["https://c.example/cb"],"client_name":"C"}`,
			uc:         stubRegister{out: &oauthregister.Output{ClientID: "cid-123", RedirectURIs: []string{"https://c.example/cb"}, ClientName: "C"}},
			wantStatus: http.StatusCreated,
			wantKey:    "client_id",
			wantVal:    "cid-123",
		},
		{
			name:       "bad body",
			body:       `not json`,
			uc:         stubRegister{},
			wantStatus: http.StatusBadRequest,
			wantKey:    "error",
			wantVal:    "invalid_client_metadata",
		},
		{
			name:       "usecase error",
			body:       `{"redirect_uris":[]}`,
			uc:         stubRegister{err: domain.NewOAuthError("invalid_redirect_uri", "bad")},
			wantStatus: http.StatusBadRequest,
			wantKey:    "error",
			wantVal:    "invalid_redirect_uri",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewOAuthHandler("https://engine.example", "https://app/consent", tt.uc, nil, nil, nil)
			rec := httptest.NewRecorder()
			h.Register(rec, httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(tt.body)))
			require.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantVal, decodeBody(t, rec.Body.Bytes())[tt.wantKey])
		})
	}
}

func TestOAuthHandler_AuthorizeInfo(t *testing.T) {
	t.Run("happy passes session subject", func(t *testing.T) {
		uc := &stubAuthorizeInfo{out: &oauthauthorizeinfo.Output{
			ClientName: "C", Scopes: []string{"provision"}, RedirectURIValid: true, ConsentNonce: "nonce-1",
		}}
		h := NewOAuthHandler("https://engine.example", "https://app/consent", nil, uc, nil, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/oauth/authorize-info?client_id=cid", nil)
		req = req.WithContext(domain.WithUserSub(req.Context(), "user-9"))
		rec := httptest.NewRecorder()
		h.AuthorizeInfo(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		body := decodeBody(t, rec.Body.Bytes())
		assert.Equal(t, "C", body["client_name"])
		assert.Equal(t, "nonce-1", body["consent_nonce"])
		assert.Equal(t, "user-9", uc.gotSess)
	})

	t.Run("usecase error", func(t *testing.T) {
		uc := &stubAuthorizeInfo{err: domain.NewOAuthError("invalid_client", "bad")}
		h := NewOAuthHandler("https://engine.example", "https://app/consent", nil, uc, nil, nil)
		rec := httptest.NewRecorder()
		h.AuthorizeInfo(rec, httptest.NewRequest(http.MethodGet, "/api/v1/oauth/authorize-info", nil))
		require.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Equal(t, "invalid_client", decodeBody(t, rec.Body.Bytes())["error"])
	})
}

func TestOAuthHandler_Approve(t *testing.T) {
	t.Run("happy reads identity + nonce", func(t *testing.T) {
		uc := &stubApprove{out: &oauthapprove.Output{RedirectURL: "https://c/cb?code=abc"}}
		h := NewOAuthHandler("https://engine.example", "https://app/consent", nil, nil, uc, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/oauth/approve", strings.NewReader(`{"consent_nonce":"n1","client_id":"cid"}`))
		ctx := domain.WithUserSub(req.Context(), "user-2")
		ctx = domain.WithTenantID(ctx, "tenant-2")
		rec := httptest.NewRecorder()
		h.Approve(rec, req.WithContext(ctx))

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "https://c/cb?code=abc", decodeBody(t, rec.Body.Bytes())["redirect_url"])
		assert.Equal(t, "user-2", uc.gotIn.UserID)
		assert.Equal(t, "tenant-2", uc.gotIn.TenantID)
		assert.Equal(t, "n1", uc.gotIn.ConsentNonce)
	})

	t.Run("bad body", func(t *testing.T) {
		h := NewOAuthHandler("https://engine.example", "https://app/consent", nil, nil, &stubApprove{}, nil)
		rec := httptest.NewRecorder()
		h.Approve(rec, httptest.NewRequest(http.MethodPost, "/api/v1/oauth/approve", strings.NewReader("nope")))
		require.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Equal(t, "invalid_request", decodeBody(t, rec.Body.Bytes())["error"])
	})
}

func TestOAuthHandler_Token(t *testing.T) {
	t.Run("happy sets no-store", func(t *testing.T) {
		uc := stubToken{out: &oauthtoken.Output{AccessToken: "at", RefreshToken: "rt", Scope: "provision", ExpiresIn: 1800}}
		h := NewOAuthHandler("https://engine.example", "https://app/consent", nil, nil, nil, uc)

		form := url.Values{"grant_type": {"authorization_code"}, "code": {"c"}, "client_id": {"cid"}}
		req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.Token(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
		assert.Equal(t, "no-cache", rec.Header().Get("Pragma"))
		body := decodeBody(t, rec.Body.Bytes())
		assert.Equal(t, "at", body["access_token"])
		assert.Equal(t, "Bearer", body["token_type"])
	})

	t.Run("usecase invalid_grant is 400", func(t *testing.T) {
		uc := stubToken{err: domain.NewOAuthError("invalid_grant", "bad")}
		h := NewOAuthHandler("https://engine.example", "https://app/consent", nil, nil, nil, uc)
		req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader("grant_type=authorization_code&client_id=c"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.Token(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Equal(t, "invalid_grant", decodeBody(t, rec.Body.Bytes())["error"])
	})

	t.Run("server_error is 500", func(t *testing.T) {
		uc := stubToken{err: domain.NewOAuthError("server_error", "boom")}
		h := NewOAuthHandler("https://engine.example", "https://app/consent", nil, nil, nil, uc)
		req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader("grant_type=refresh_token&client_id=c"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.Token(rec, req)
		require.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}
