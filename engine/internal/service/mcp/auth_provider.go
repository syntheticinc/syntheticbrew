package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/pkg/secrets"
)

// AuthProvider applies authentication to MCP server HTTP requests
// based on the server's auth configuration. Secret values are resolved by
// dynamic env-var name (mcp_servers.auth_key_env / auth_token_env) through
// the pkg/secrets chokepoint. Per-tenant encrypted storage is target
// architecture (mirrors models.api_key_encrypted) — tracked separately.
type AuthProvider struct{}

// NewAuthProvider creates a new MCP auth provider.
func NewAuthProvider() *AuthProvider {
	return &AuthProvider{}
}

// ApplyAuth applies the configured auth to an HTTP request for an MCP server call.
// incomingHeaders are the headers from the original user request (for forward_headers).
func (p *AuthProvider) ApplyAuth(req *http.Request, cfg domain.MCPAuthConfig, incomingHeaders map[string]string) error {
	switch cfg.Type {
	case domain.MCPAuthNone:
		return nil

	case domain.MCPAuthAPIKey:
		key := secrets.Lookup(cfg.KeyEnv)
		if key == "" {
			return fmt.Errorf("env var %s not set for MCP auth", cfg.KeyEnv)
		}
		req.Header.Set("Authorization", "Bearer "+key)
		return nil

	case domain.MCPAuthForwardHeaders:
		for name, value := range incomingHeaders {
			req.Header.Set(name, value)
		}
		return nil

	case domain.MCPAuthServiceAccount:
		token := secrets.Lookup(cfg.TokenEnv)
		if token == "" {
			return fmt.Errorf("env var %s not set for MCP service account auth", cfg.TokenEnv)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return nil

	case domain.MCPAuthOAuth2:
		// OAuth2 token refresh not implemented; treats stored token as static.
		slog.WarnContext(context.Background(), "[MCPAuth] OAuth2 using static token — full refresh not yet implemented")
		token := secrets.Lookup(cfg.TokenEnv)
		if token == "" {
			return fmt.Errorf("oauth2 token env var %s not set", cfg.TokenEnv)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return nil

	default:
		return fmt.Errorf("unsupported MCP auth type: %s", cfg.Type)
	}
}
