package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBootstrapEnvOverrides_JWTExpectedAudience(t *testing.T) {
	t.Setenv(EnvJWTExpectedAudience, "https://engine.example.test/api/v1/mcp/rpc")

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	yaml := `
engine:
  port: 8443
database:
  url: "postgresql://localhost/db"
`
	require.NoError(t, os.WriteFile(configPath, []byte(yaml), 0644))

	cfg, err := LoadBootstrap(configPath)
	require.NoError(t, err)
	assert.Equal(t, "https://engine.example.test/api/v1/mcp/rpc", cfg.Security.JWTExpectedAudience)
}

func TestBootstrap_JWTExpectedAudienceDefaultEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	yaml := `
engine:
  port: 8443
database:
  url: "postgresql://localhost/db"
`
	require.NoError(t, os.WriteFile(configPath, []byte(yaml), 0644))

	cfg, err := LoadBootstrap(configPath)
	require.NoError(t, err)
	assert.Empty(t, cfg.Security.JWTExpectedAudience)
}
