package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testPRResource  = "https://engine.example/api/v1/mcp/rpc"
	testPRIssuer    = "https://engine.example"
	testPRChallenge = `Bearer resource_metadata="https://engine.example/.well-known/oauth-protected-resource", scope="provision"`
)

func TestProtectedResource_Metadata(t *testing.T) {
	p := NewOAuthProtectedResource(testPRResource, testPRIssuer, testPRChallenge)
	rec := httptest.NewRecorder()
	p.Metadata(rec, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var body oauthProtectedResourceMetadata
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, testPRResource, body.Resource)
	assert.Equal(t, []string{testPRIssuer}, body.AuthorizationServers)
	assert.Equal(t, []string{"provision", "manage"}, body.ScopesSupported)
	assert.Equal(t, []string{"header"}, body.BearerMethodsSupported)
}

func TestProtectedResource_ChallengeDecorates401OnMCPPath(t *testing.T) {
	p := NewOAuthProtectedResource(testPRResource, testPRIssuer, testPRChallenge)
	handler := p.Challenge()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/mcp/rpc", nil))

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, testPRChallenge, rec.Header().Get("WWW-Authenticate"))
}

func TestProtectedResource_ChallengeIgnoresNon401(t *testing.T) {
	p := NewOAuthProtectedResource(testPRResource, testPRIssuer, testPRChallenge)
	handler := p.Challenge()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/mcp/rpc", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("WWW-Authenticate"))
}

func TestProtectedResource_ChallengeSkipsNonMCPPath(t *testing.T) {
	p := NewOAuthProtectedResource(testPRResource, testPRIssuer, testPRChallenge)
	handler := p.Challenge()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil))

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Empty(t, rec.Header().Get("WWW-Authenticate"), "non-MCP paths must not be decorated")
}

func TestProtectedResource_ChallengeIsSSESafe(t *testing.T) {
	p := NewOAuthProtectedResource(testPRResource, testPRIssuer, testPRChallenge)
	handler := p.Challenge()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		require.True(t, ok, "wrapped writer must remain a Flusher for SSE")
		if _, err := w.Write([]byte("data: hi\n\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
		f.Flush()
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/mcp/rpc", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "data: hi\n\n", rec.Body.String())
}
