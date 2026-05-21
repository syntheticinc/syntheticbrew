package mcp

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

func TestAuthProvider_None(t *testing.T) {
	p := NewAuthProvider()
	req, _ := http.NewRequest("GET", "http://example.com", nil)

	err := p.ApplyAuth(req, domain.MCPAuthConfig{Type: domain.MCPAuthNone}, nil)
	require.NoError(t, err)
	assert.Empty(t, req.Header.Get("Authorization"))
}

func TestAuthProvider_APIKey(t *testing.T) {
	p := NewAuthProvider()
	req, _ := http.NewRequest("GET", "http://example.com", nil)

	// AC-AUTH-03: API key from env var
	t.Setenv("TEST_MCP_KEY", "secret-key-123")
	err := p.ApplyAuth(req, domain.MCPAuthConfig{
		Type:   domain.MCPAuthAPIKey,
		KeyEnv: "TEST_MCP_KEY",
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, "Bearer secret-key-123", req.Header.Get("Authorization"))
}

func TestAuthProvider_APIKey_MissingEnv(t *testing.T) {
	p := NewAuthProvider()
	req, _ := http.NewRequest("GET", "http://example.com", nil)

	err := p.ApplyAuth(req, domain.MCPAuthConfig{
		Type:   domain.MCPAuthAPIKey,
		KeyEnv: "NONEXISTENT_ENV_VAR_XYZ",
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not set")
}

func TestAuthProvider_ForwardHeaders(t *testing.T) {
	p := NewAuthProvider()
	req, _ := http.NewRequest("GET", "http://example.com", nil)

	// AC-AUTH-02: forward_headers proxied from incoming request
	incoming := map[string]string{
		"X-Customer-ID": "cust-42",
		"Authorization": "Bearer user-token",
	}
	err := p.ApplyAuth(req, domain.MCPAuthConfig{
		Type: domain.MCPAuthForwardHeaders,
	}, incoming)
	require.NoError(t, err)
	assert.Equal(t, "cust-42", req.Header.Get("X-Customer-ID"))
	assert.Equal(t, "Bearer user-token", req.Header.Get("Authorization"))
}

func TestAuthProvider_ServiceAccount(t *testing.T) {
	p := NewAuthProvider()
	req, _ := http.NewRequest("GET", "http://example.com", nil)

	t.Setenv("TEST_SA_TOKEN", "sa-token-xyz")
	err := p.ApplyAuth(req, domain.MCPAuthConfig{
		Type:     domain.MCPAuthServiceAccount,
		TokenEnv: "TEST_SA_TOKEN",
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, "Bearer sa-token-xyz", req.Header.Get("Authorization"))
}

func TestAuthProvider_UnsupportedType(t *testing.T) {
	p := NewAuthProvider()
	req, _ := http.NewRequest("GET", "http://example.com", nil)

	err := p.ApplyAuth(req, domain.MCPAuthConfig{Type: "magic"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported")
}
