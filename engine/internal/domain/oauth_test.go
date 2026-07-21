package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRedirectURI(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"https any host", "https://app.example.com/callback", false},
		{"http loopback localhost any port", "http://localhost:53123/cb", false},
		{"http loopback 127.0.0.1", "http://127.0.0.1:8080/cb", false},
		{"http non-loopback rejected", "http://evil.example.com/cb", true},
		{"custom scheme with dot allowed", "cursor://host.tld/callback", false},
		{"reverse-domain custom scheme allowed", "com.example.app://callback.done", false},
		{"custom scheme without dot rejected", "myapp://callback", true},
		{"no scheme rejected", "example.com/cb", true},
		{"fragment rejected", "https://app.example.com/cb#frag", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRedirectURI(tt.raw)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestMatchRedirectURI_LoopbackPortAgnostic(t *testing.T) {
	// Registered loopback URI matches presented with a different port.
	assert.True(t, MatchRedirectURI("http://127.0.0.1:0/cb", "http://127.0.0.1:53210/cb"))
	assert.True(t, MatchRedirectURI("http://localhost/cb", "http://localhost:9999/cb"))
	// Path must still match exactly.
	assert.False(t, MatchRedirectURI("http://127.0.0.1/cb", "http://127.0.0.1:1/other"))
	// Query must still match exactly.
	assert.False(t, MatchRedirectURI("http://localhost/cb?a=1", "http://localhost:2/cb?a=2"))
	// Non-loopback requires exact string match.
	assert.True(t, MatchRedirectURI("https://app.example.com/cb", "https://app.example.com/cb"))
	assert.False(t, MatchRedirectURI("https://app.example.com/cb", "https://app.example.com/cb?x=1"))
}

func TestValidateAuthorizeRequest(t *testing.T) {
	client := OAuthClient{
		Name:         "cli",
		RedirectURIs: []string{"http://127.0.0.1:0/cb"},
	}

	t.Run("valid S256 request with subset scope", func(t *testing.T) {
		scopes, oerr := ValidateAuthorizeRequest(client, "http://127.0.0.1:51000/cb", "provision offline_access", "abc123", "S256")
		require.Nil(t, oerr)
		assert.Equal(t, []string{"provision", "offline_access"}, scopes)
	})

	t.Run("plain PKCE method rejected", func(t *testing.T) {
		_, oerr := ValidateAuthorizeRequest(client, "http://127.0.0.1:51000/cb", "provision", "abc123", "plain")
		require.NotNil(t, oerr)
		assert.Equal(t, "invalid_request", oerr.Code)
	})

	t.Run("missing challenge rejected", func(t *testing.T) {
		_, oerr := ValidateAuthorizeRequest(client, "http://127.0.0.1:51000/cb", "provision", "", "S256")
		require.NotNil(t, oerr)
		assert.Equal(t, "invalid_request", oerr.Code)
	})

	t.Run("unregistered redirect rejected", func(t *testing.T) {
		_, oerr := ValidateAuthorizeRequest(client, "https://evil.example.com/cb", "provision", "abc", "S256")
		require.NotNil(t, oerr)
		assert.Equal(t, "invalid_request", oerr.Code)
	})

	t.Run("unsupported scope rejected", func(t *testing.T) {
		_, oerr := ValidateAuthorizeRequest(client, "http://127.0.0.1:51000/cb", "provision admin", "abc", "S256")
		require.NotNil(t, oerr)
		assert.Equal(t, "invalid_scope", oerr.Code)
	})

	t.Run("empty scope rejected", func(t *testing.T) {
		_, oerr := ValidateAuthorizeRequest(client, "http://127.0.0.1:51000/cb", "", "abc", "S256")
		require.NotNil(t, oerr)
		assert.Equal(t, "invalid_scope", oerr.Code)
	})
}

func TestScopeSubset(t *testing.T) {
	allowed := SupportedOAuthScopes()
	assert.True(t, ScopeSubset([]string{"provision"}, allowed))
	assert.True(t, ScopeSubset([]string{"provision", "manage", "offline_access"}, allowed))
	assert.False(t, ScopeSubset([]string{"provision", "admin"}, allowed))
	assert.False(t, ScopeSubset([]string{"config"}, allowed))
}
