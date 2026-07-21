package app

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
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
//   - Shared-key-across-instances: every instance serving the same issuer must
//     sign with the same key. An explicit OAuth.ASKeyPath is the shared key;
//     with none set the key is generated and persisted under JWTKeysDir. When
//     neither is available the server fails fast (see resolveASPrivateKey).
//   - H-1 (separate key): the AS keypair uses a distinct file name (as_ed25519),
//     never reusing the local-admin session key, in every mode.
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

	priv, err := resolveASPrivateKey(ctx, cfg)
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

// resolveASPrivateKey resolves the authorization-server signing key. The choice
// is driven by whether an explicit key path is configured — NOT by auth mode,
// which governs how user/session tokens are verified and says nothing about
// where the AS key lives. It mirrors how the session keypair is resolved.
//
// The invariant the AS actually needs: every instance that serves the same
// issuer must sign and verify with the same key. That is a deployment-topology
// concern (one instance vs many), not an auth-mode one.
func resolveASPrivateKey(ctx context.Context, cfg *config.BootstrapConfig) (ed25519.PrivateKey, error) {
	// Explicit key: the same file is mounted on every instance (a k8s Secret,
	// for example). Use this for multi-instance deployments that don't share a
	// keys directory, or to control rotation.
	if cfg.OAuth.ASKeyPath != "" {
		priv, err := auth.LoadPrivateKey(cfg.OAuth.ASKeyPath)
		if err != nil {
			return nil, fmt.Errorf("load oauth authorization-server private key: %w", err)
		}
		return priv, nil
	}

	// No explicit key: the engine generates one once in the keys directory and
	// reuses it across restarts. Correct for a single instance or instances that
	// share a persisted keys volume. A multi-instance deployment with
	// per-instance ephemeral storage MUST instead set
	// SYNTHETICBREW_OAUTH_AS_KEY_PATH, or each instance would sign with a
	// different key and reject the others' tokens.
	if cfg.Security.JWTKeysDir == "" {
		return nil, fmt.Errorf("oauth authorization server is enabled but neither " +
			"SYNTHETICBREW_OAUTH_AS_KEY_PATH nor SYNTHETICBREW_JWT_KEYS_DIR is set: the AS needs " +
			"either an explicit signing key or a keys directory to generate and persist one")
	}
	// Detect first-generation so it never happens silently: a freshly generated
	// signing key on a multi-instance deployment that forgot the shared key path
	// is the one misconfiguration this path can't reject, and it only surfaces
	// later as intermittent 401s behind the load balancer.
	keyPath := filepath.Join(cfg.Security.JWTKeysDir, asKeypairName+".priv")
	_, statErr := os.Stat(keyPath)
	firstGen := os.IsNotExist(statErr)

	kp, err := auth.LoadOrGenerateKeypairNamed(cfg.Security.JWTKeysDir, asKeypairName)
	if err != nil {
		return nil, fmt.Errorf("load/generate oauth authorization-server keypair: %w", err)
	}
	if firstGen {
		if cfg.Security.AuthMode == config.AuthModeExternal {
			slog.WarnContext(ctx, "generated a new OAuth authorization-server signing key in the keys "+
				"directory; a multi-instance deployment MUST instead set SYNTHETICBREW_OAUTH_AS_KEY_PATH "+
				"to a shared key, or every instance will sign with a different key and reject the others",
				"path", keyPath, "kid", auth.KeyID(kp.Public))
		} else {
			slog.InfoContext(ctx, "generated a new OAuth authorization-server signing key",
				"path", keyPath, "kid", auth.KeyID(kp.Public))
		}
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
