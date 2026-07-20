package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/pkg/config"
)

// localASConfig builds a bootstrap config with the AS enabled in local mode,
// keyed to a fresh temp dir so keypair generation is isolated per test.
func localASConfig(t *testing.T, issuer, publicBaseURL string) *config.BootstrapConfig {
	t.Helper()
	return &config.BootstrapConfig{
		Engine:   config.EngineBootstrap{PublicBaseURL: publicBaseURL},
		Security: config.BootstrapSecurity{AuthMode: config.AuthModeLocal, JWTKeysDir: t.TempDir()},
		OAuth:    config.BootstrapOAuth{ASEnabled: true, Issuer: issuer},
	}
}

func TestResolveOAuthAS_Disabled(t *testing.T) {
	cfg := localASConfig(t, "https://engine.example", "")
	cfg.OAuth.ASEnabled = false

	got, err := resolveOAuthAS(context.Background(), cfg)
	require.NoError(t, err)
	assert.False(t, got.Enabled)
	assert.Nil(t, got.Signer)
}

func TestResolveOAuthAS_AutoDisableWhenIssuerEmpty(t *testing.T) {
	// C-3: enabled but neither issuer nor public base URL set → disabled, no error.
	cfg := localASConfig(t, "", "")

	got, err := resolveOAuthAS(context.Background(), cfg)
	require.NoError(t, err)
	assert.False(t, got.Enabled, "AS must auto-disable when no issuer resolves")
	assert.Nil(t, got.Signer)
}

func TestResolveOAuthAS_IssuerFallsBackToPublicBaseURL(t *testing.T) {
	cfg := localASConfig(t, "", "https://engine.example")

	got, err := resolveOAuthAS(context.Background(), cfg)
	require.NoError(t, err)
	require.True(t, got.Enabled)
	assert.Equal(t, "https://engine.example", got.Issuer)
	assert.Equal(t, "https://engine.example/api/v1/mcp/rpc", got.Resource)
	assert.Equal(t, "https://engine.example/admin/oauth/consent", got.ConsentURL)
	require.NotNil(t, got.Signer)
	assert.NotEmpty(t, got.KID)
	assert.Len(t, got.Pub, ed25519.PublicKeySize)
}

func TestResolveOAuthAS_TrimsTrailingSlashAndKeepsExplicitConsent(t *testing.T) {
	cfg := localASConfig(t, "https://engine.example/", "")
	cfg.OAuth.ConsentURL = "https://custom.example/consent"

	got, err := resolveOAuthAS(context.Background(), cfg)
	require.NoError(t, err)
	require.True(t, got.Enabled)
	assert.Equal(t, "https://engine.example", got.Issuer)
	assert.Equal(t, "https://custom.example/consent", got.ConsentURL)
}

func TestResolveOAuthAS_LocalKeypairIsSeparateFromSessionKey(t *testing.T) {
	// H-1: the AS key is a distinct file, never the session key.
	cfg := localASConfig(t, "https://engine.example", "")

	got, err := resolveOAuthAS(context.Background(), cfg)
	require.NoError(t, err)
	require.True(t, got.Enabled)

	_, err = os.Stat(filepath.Join(cfg.Security.JWTKeysDir, "as_ed25519.priv"))
	require.NoError(t, err, "AS private key must be persisted under its own name")
}

func TestResolveOAuthAS_NoKeyPathAndNoKeysDirIsFatal(t *testing.T) {
	// With neither an explicit key nor a keys directory the AS has nowhere to
	// get or persist a signing key — fail fast rather than boot without one.
	cfg := &config.BootstrapConfig{
		Security: config.BootstrapSecurity{AuthMode: config.AuthModeExternal, JWTPublicKeyPath: "/tmp/pub"},
		OAuth:    config.BootstrapOAuth{ASEnabled: true, Issuer: "https://engine.example"},
	}

	_, err := resolveOAuthAS(context.Background(), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SYNTHETICBREW_OAUTH_AS_KEY_PATH")
	assert.Contains(t, err.Error(), "SYNTHETICBREW_JWT_KEYS_DIR")
}

func TestResolveOAuthAS_AutoGeneratesFromKeysDirRegardlessOfAuthMode(t *testing.T) {
	// Auth mode does not gate AS key generation: external mode with a keys dir
	// (but no explicit key path) auto-generates, exactly like local mode.
	cfg := &config.BootstrapConfig{
		Security: config.BootstrapSecurity{AuthMode: config.AuthModeExternal, JWTKeysDir: t.TempDir()},
		OAuth:    config.BootstrapOAuth{ASEnabled: true, Issuer: "https://engine.example"},
	}

	got, err := resolveOAuthAS(context.Background(), cfg)
	require.NoError(t, err)
	require.True(t, got.Enabled)
	_, statErr := os.Stat(filepath.Join(cfg.Security.JWTKeysDir, "as_ed25519.priv"))
	require.NoError(t, statErr, "AS key must be generated and persisted in the keys dir")
}

func TestResolveOAuthAS_LoadsProvisionedKeyPath(t *testing.T) {
	// An explicit key path is used verbatim, independent of auth mode — the
	// shared-secret path for multi-instance deployments.
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	keyPath := filepath.Join(t.TempDir(), "as.key")
	require.NoError(t, os.WriteFile(keyPath, []byte(hex.EncodeToString(priv)+"\n"), 0o600))

	cfg := &config.BootstrapConfig{
		Security: config.BootstrapSecurity{AuthMode: config.AuthModeExternal, JWTPublicKeyPath: "/tmp/pub"},
		OAuth:    config.BootstrapOAuth{ASEnabled: true, Issuer: "https://engine.example", ASKeyPath: keyPath},
	}

	got, err := resolveOAuthAS(context.Background(), cfg)
	require.NoError(t, err)
	require.True(t, got.Enabled)
	// The signer must use the provisioned key, so its public half matches.
	assert.Equal(t, ed25519.PublicKey(priv.Public().(ed25519.PublicKey)), got.Pub)
}

func TestOAuthGuardHost(t *testing.T) {
	assert.Equal(t, "engine.example", oauthGuardHost(&config.BootstrapConfig{
		OAuth: config.BootstrapOAuth{Issuer: "https://engine.example/"},
	}))
	assert.Equal(t, "engine.example:8443", oauthGuardHost(&config.BootstrapConfig{
		Engine: config.EngineBootstrap{PublicBaseURL: "https://engine.example:8443"},
	}))
	assert.Equal(t, "", oauthGuardHost(&config.BootstrapConfig{}))
}
