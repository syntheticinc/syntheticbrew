//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	ceserver "github.com/syntheticinc/syntheticbrew/pkg/server"
)

// TestBYOKEnvReconcileOnBoot proves the GitOps boot wiring:
//
//	SYNTHETICBREW_BYOK_* env → config.LoadBootstrap (ManagedEnabled/ManagedProviders)
//	→ bootstrapSeeds → reconcileBYOKConfig → settings rows overwritten on startup.
//
// It boots a fully self-contained engine (own pgvector container, own port,
// own config) with the env vars set, then opens a direct GORM connection and
// asserts the sentinel-tenant settings rows carry the operator-declared jsonb
// shapes. It deliberately does NOT touch the shared suite globals (baseURL,
// testDB) so it stays hermetic.
func TestBYOKEnvReconcileOnBoot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pg, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("syntheticbrew_ce_test"),
		tcpostgres.WithUsername("syntheticbrew"),
		tcpostgres.WithPassword("syntheticbrew_ce_test_pass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		// Mirror the suite's behaviour: no Docker → skip, don't fail.
		t.Skipf("byok env-reconcile test skipped — cannot start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })

	connStr, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "postgres connection string")

	migrationsDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	require.NoError(t, err, "resolve migrations dir")
	_, statErr := os.Stat(migrationsDir)
	require.NoError(t, statErr, "migrations dir not found")
	require.NoError(t, applyLiquibaseMigrations(ctx, pg, migrationsDir), "apply liquibase migrations")

	httpPort, err := pickFreePort()
	require.NoError(t, err, "pick free port")

	dataDir, err := os.MkdirTemp("", "syntheticbrew-byok-reconcile-")
	require.NoError(t, err, "mkdir data")
	t.Cleanup(func() { _ = os.RemoveAll(dataDir) })

	configPath := filepath.Join(dataDir, "config.yaml")
	require.NoError(t, writeBootstrapConfig(configPath, connStr, httpPort), "write bootstrap config")

	// Isolate the engine's portfile / logs inside dataDir.
	restoreEnv := setEnvIsolated(dataDir)
	t.Cleanup(restoreEnv)

	// Operator-declared BYOK state via env. The space after the comma also
	// proves the CSV→[]string trimming in config.LoadBootstrap.
	t.Setenv("SYNTHETICBREW_BYOK_ENABLED", "true")
	t.Setenv("SYNTHETICBREW_BYOK_ALLOWED_PROVIDERS", "openai, ollama")

	serverCtx, serverCancel := context.WithCancel(context.Background())
	t.Cleanup(serverCancel)

	go func() {
		_ = ceserver.Run(ceserver.Config{
			ConfigPath:     configPath,
			ConfigExplicit: true,
			RequireTenant:  false,
			Version:        "ce-byok-reconcile-test",
			Commit:         "none",
			Date:           "none",
		})
		_ = serverCtx
	}()

	base := fmt.Sprintf("http://127.0.0.1:%d", httpPort)
	require.NoError(t, waitForHealthy(ctx, base, 60*time.Second), "wait for engine healthy")

	// Direct GORM connection to assert the reconciled rows. Separate from the
	// engine pool on purpose — we read the durable state the boot path wrote.
	db, err := gorm.Open(gormpostgres.Open(connStr), &gorm.Config{
		Logger: gormlogger.Discard,
	})
	require.NoError(t, err, "open assertion gorm connection")

	t.Run("byok.enabled reconciled to jsonb true", func(t *testing.T) {
		var row models.SettingModel
		err := db.WithContext(ctx).
			Where("tenant_id = ? AND key = ?", ceTenantID, "byok.enabled").
			First(&row).Error
		require.NoError(t, err, "byok.enabled row must exist")

		var enabled bool
		require.NoError(t, json.Unmarshal(row.Value, &enabled),
			"byok.enabled must be a jsonb boolean: %q", string(row.Value))
		assert.True(t, enabled, "operator-declared byok.enabled=true must win on boot")
	})

	t.Run("byok.allowed_providers reconciled to trimmed jsonb array", func(t *testing.T) {
		var row models.SettingModel
		err := db.WithContext(ctx).
			Where("tenant_id = ? AND key = ?", ceTenantID, "byok.allowed_providers").
			First(&row).Error
		require.NoError(t, err, "byok.allowed_providers row must exist")

		var providers []string
		require.NoError(t, json.Unmarshal(row.Value, &providers),
			"byok.allowed_providers must be a jsonb array: %q", string(row.Value))
		// The CSV "openai, ollama" must reconcile to a trimmed array.
		assert.Equal(t, []string{"openai", "ollama"}, providers,
			"allowlist must be trimmed and split: %q", string(row.Value))
	})
}
