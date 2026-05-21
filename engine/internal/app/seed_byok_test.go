package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
)

// settingsTableDDL is the SQLite-compatible analogue of migration 018.
// It mirrors the production DDL shape closely enough for unit tests
// (composite PK on tenant_id+key, value column for jsonb-style content).
const settingsTableDDL = `CREATE TABLE IF NOT EXISTS settings (
    tenant_id   TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
    key         TEXT NOT NULL,
    value       BLOB NOT NULL DEFAULT '{}',
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, key)
);`

func setupSettingsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)
	require.NoError(t, db.Exec(settingsTableDDL).Error)
	return db
}

// TestSeedBYOKConfig_FreshDB writes both keys with the YAML values when
// the table is empty. Round-trip via loadBYOKConfig must return the
// same shape (booleans, arrays — not strings).
func TestSeedBYOKConfig_FreshDB(t *testing.T) {
	db := setupSettingsTestDB(t)
	ctx := context.Background()

	cfg := config.BYOKConfig{
		Enabled:          true,
		AllowedProviders: []string{"openai", "anthropic"},
	}
	seedBYOKConfig(ctx, db, cfg)

	loaded := loadBYOKConfig(ctx, db, config.BYOKConfig{})
	assert.True(t, loaded.Enabled)
	assert.ElementsMatch(t, []string{"openai", "anthropic"}, loaded.AllowedProviders)

	// Inspect the raw rows — the stored shapes must be jsonb-compatible
	// (boolean true, JSON array) so admin queries via SettingsPage can
	// round-trip them as structured values.
	repo := configrepo.NewGORMSettingRepository(db)

	enabledRow, err := repo.Get(ctx, settingKeyBYOKEnabled)
	require.NoError(t, err)
	require.NotNil(t, enabledRow)
	assert.Equal(t, "true", string(enabledRow.Value))

	providersRow, err := repo.Get(ctx, settingKeyBYOKAllowedProviders)
	require.NoError(t, err)
	require.NotNil(t, providersRow)
	var providers []string
	require.NoError(t, json.Unmarshal(providersRow.Value, &providers))
	assert.ElementsMatch(t, []string{"openai", "anthropic"}, providers)
}

// TestSeedBYOKConfig_PreservesAdminEdits ensures the seeder only
// initialises missing rows. Admin-side updates persist across restart.
func TestSeedBYOKConfig_PreservesAdminEdits(t *testing.T) {
	db := setupSettingsTestDB(t)
	ctx := context.Background()
	repo := configrepo.NewGORMSettingRepository(db)

	// Admin has previously toggled BYOK off and narrowed providers.
	require.NoError(t, repo.SetJSON(ctx, settingKeyBYOKEnabled, []byte("false")))
	require.NoError(t, repo.SetJSON(ctx, settingKeyBYOKAllowedProviders, []byte(`["anthropic"]`)))

	// Booting again with a permissive YAML must NOT undo admin's choice.
	seedBYOKConfig(ctx, db, config.BYOKConfig{
		Enabled:          true,
		AllowedProviders: []string{"openai", "anthropic", "openrouter"},
	})

	loaded := loadBYOKConfig(ctx, db, config.BYOKConfig{})
	assert.False(t, loaded.Enabled, "admin override must survive seeder")
	assert.Equal(t, []string{"anthropic"}, loaded.AllowedProviders)
}

// TestLoadBYOKConfig_FallbackOnEmpty returns the fallback when the
// table has no rows yet — keeps the bootstrap path safe (e.g. stale
// tests that did not run the seeder).
func TestLoadBYOKConfig_FallbackOnEmpty(t *testing.T) {
	db := setupSettingsTestDB(t)
	ctx := context.Background()

	fallback := config.BYOKConfig{
		Enabled:          false,
		AllowedProviders: []string{"openai"},
	}

	loaded := loadBYOKConfig(ctx, db, fallback)
	assert.False(t, loaded.Enabled)
	assert.Equal(t, []string{"openai"}, loaded.AllowedProviders)
}

// TestLoadBYOKConfig_NilDBReturnsFallback documents the contract for
// callers that may not have a DB available (e.g. the test server runs
// without persistence).
func TestLoadBYOKConfig_NilDBReturnsFallback(t *testing.T) {
	fallback := config.BYOKConfig{Enabled: true, AllowedProviders: []string{"x"}}
	loaded := loadBYOKConfig(context.Background(), nil, fallback)
	assert.Equal(t, fallback, loaded)
}
