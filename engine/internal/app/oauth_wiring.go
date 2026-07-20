package app

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/auth"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
	pluginpkg "github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// oauthMCPResourcePath is the path segment of the canonical MCP resource URI —
// the single source of truth stamped as the `aud` of every AS access token,
// expected by the composite verifier, and advertised in protected-resource
// metadata. Issuer + this path yields the resource string.
const oauthMCPResourcePath = "/api/v1/mcp/rpc"

// asKeypairName is the file base for the authorization-server keypair, kept
// physically separate from the local-admin session key ("jwt_ed25519").
const asKeypairName = "as_ed25519"

// oauthASWiring is the resolved authorization-server configuration. When
// Enabled is false the server is not wired at all (routes unregistered, the
// verifier left unwrapped) — either because it was disabled by config or
// auto-disabled because no issuer could be resolved (C-3).
type oauthASWiring struct {
	Enabled bool
	// Issuer is the AS base URL (no trailing slash).
	Issuer string
	// Resource is the canonical MCP resource URI (Issuer + oauthMCPResourcePath).
	// It is the ONE source of truth used for the token aud, the composite
	// verifier's expected audience, and the protected-resource metadata.
	Resource string
	// ConsentURL is the consent page advertised as the authorization_endpoint.
	ConsentURL string
	// Signer mints AS credentials; KID/Pub route and verify its access tokens.
	Signer *auth.OAuthTokenSigner
	KID    string
	Pub    ed25519.PublicKey
}

// resolveOAuthAS resolves the embedded OAuth 2.1 authorization-server wiring
// from bootstrap config. It implements three security requirements:
//
//   - C-3 (auto-disable, never brick boot): AS enabled but no issuer resolvable
//     → loud WARN and return a disabled wiring rather than failing startup.
//     PUBLIC_BASE_URL is optional and often unset on existing installs.
//   - C-2 (external key is a pre-provisioned Secret): in external auth mode the
//     AS private key is loaded from OAuth.ASKeyPath and MUST NOT be generated —
//     AS enabled + external + no key path is a hard startup error.
//   - H-1 (separate key): in local mode the AS keypair is generated under a
//     distinct file name (as_ed25519), never reusing the session key.
func resolveOAuthAS(ctx context.Context, cfg *config.BootstrapConfig) (*oauthASWiring, error) {
	if cfg == nil || !cfg.OAuth.ASEnabled {
		return &oauthASWiring{Enabled: false}, nil
	}

	issuer := strings.TrimSuffix(resolveOAuthIssuer(cfg), "/")
	if issuer == "" {
		slog.WarnContext(ctx, "OAuth authorization server is enabled but no issuer is configured "+
			"(SYNTHETICBREW_OAUTH_ISSUER / SYNTHETICBREW_PUBLIC_BASE_URL unset) — disabling it; "+
			"MCP OAuth endpoints will not be registered and the token verifier is left unwrapped")
		return &oauthASWiring{Enabled: false}, nil
	}

	priv, err := resolveASPrivateKey(cfg)
	if err != nil {
		return nil, err
	}

	signer := auth.NewOAuthTokenSigner(priv, issuer)
	consentURL := cfg.OAuth.ConsentURL
	if consentURL == "" {
		consentURL = issuer + "/admin/oauth/consent"
	}

	return &oauthASWiring{
		Enabled:    true,
		Issuer:     issuer,
		Resource:   issuer + oauthMCPResourcePath,
		ConsentURL: consentURL,
		Signer:     signer,
		KID:        signer.KID(),
		Pub:        signer.PublicKey(),
	}, nil
}

// resolveOAuthIssuer returns the configured AS issuer, falling back to the
// deployment's public base URL when the explicit issuer is unset.
func resolveOAuthIssuer(cfg *config.BootstrapConfig) string {
	if cfg.OAuth.Issuer != "" {
		return cfg.OAuth.Issuer
	}
	return cfg.Engine.PublicBaseURL
}

// resolveASPrivateKey loads (external mode) or loads-or-generates (local mode)
// the authorization-server Ed25519 private key.
func resolveASPrivateKey(cfg *config.BootstrapConfig) (ed25519.PrivateKey, error) {
	if cfg.Security.AuthMode == config.AuthModeExternal {
		if cfg.OAuth.ASKeyPath == "" {
			return nil, fmt.Errorf("oauth authorization server is enabled in external auth mode but " +
				"SYNTHETICBREW_OAUTH_AS_KEY_PATH is not set: the AS signing key must be a pre-provisioned " +
				"secret identical across replicas and is never auto-generated in external mode")
		}
		priv, err := auth.LoadPrivateKey(cfg.OAuth.ASKeyPath)
		if err != nil {
			return nil, fmt.Errorf("load oauth authorization-server private key: %w", err)
		}
		return priv, nil
	}

	kp, err := auth.LoadOrGenerateKeypairNamed(cfg.Security.JWTKeysDir, asKeypairName)
	if err != nil {
		return nil, fmt.Errorf("load/generate oauth authorization-server keypair: %w", err)
	}
	return kp.Private, nil
}

// issuerHost extracts the host (with port) from the issuer URL, for the Host
// allowlist that guards the credential-issuing endpoints. Returns "" when the
// issuer is empty or unparseable.
func issuerHost(issuer string) string {
	if issuer == "" {
		return ""
	}
	u, err := url.Parse(issuer)
	if err != nil {
		return ""
	}
	return u.Host
}

// oauthGuardHost returns the issuer host used to seed the Host allowlist that
// guards the credential-issuing endpoints. It resolves the issuer the same way
// resolveOAuthAS does (explicit issuer, else public base URL) so the guard is
// derived even when the AS itself is disabled but local-session still exists.
func oauthGuardHost(cfg *config.BootstrapConfig) string {
	if cfg == nil {
		return ""
	}
	return issuerHost(strings.TrimSuffix(resolveOAuthIssuer(cfg), "/"))
}

// mcpConnectNotifier adapts Plugin.OnMCPClientConnected to the token usecase's
// ConnectNotifier seam so a fresh MCP client connection (authorization-code
// exchange) is reported to the plugin. CE's Noop makes it a no-op; EE forwards
// the activation event.
type mcpConnectNotifier struct {
	plugin pluginpkg.Plugin
}

func (n mcpConnectNotifier) OnMCPConnected(ctx context.Context, tenantID string) {
	n.plugin.OnMCPClientConnected(ctx, tenantID)
}
