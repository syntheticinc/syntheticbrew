package app

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/pkg/config"
)

const bootTestCETenant = "00000000-0000-0000-0000-000000000001"

// bootModelsTestDB returns an in-memory sqlite DB with the models table,
// mirroring the production schema columns resolveBootChatModel's repo reads.
func bootModelsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	err = db.Exec(`CREATE TABLE models (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		type VARCHAR(30) NOT NULL,
		kind VARCHAR(20) NOT NULL DEFAULT 'chat',
		is_default BOOLEAN NOT NULL DEFAULT FALSE,
		base_url VARCHAR(500),
		model_name VARCHAR(255) NOT NULL,
		api_key_encrypted VARCHAR(1000),
		api_version VARCHAR(30) DEFAULT '',
		config TEXT NOT NULL DEFAULT '{}',
		tenant_id TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
		created_at DATETIME,
		updated_at DATETIME
	)`).Error
	require.NoError(t, err)
	return db
}

func seedBootDefaultChatModel(t *testing.T, db *gorm.DB, modelName string) {
	t.Helper()
	err := db.Exec(`INSERT INTO models
		(id, name, type, kind, is_default, base_url, model_name, api_key_encrypted, config, tenant_id, created_at, updated_at)
		VALUES ('m-default', 'default-chat', 'openai_compatible', 'chat', TRUE, 'https://openrouter.ai/api/v1', ?, 'k', '{}', ?, datetime('now'), datetime('now'))`,
		modelName, bootTestCETenant).Error
	require.NoError(t, err)
}

func bootEnvCfg(provider, modelName string) config.Config {
	var c config.Config
	c.LLM.DefaultProvider = provider
	c.LLM.OpenRouter.Model = modelName
	c.LLM.OpenRouter.BaseURL = "https://openrouter.ai/api/v1"
	c.LLM.OpenRouter.APIKey = "k"
	return c
}

// The DB default must win over an env-configured model — the engine is
// DB-authoritative so an admin/brewctl-set default survives restarts and is
// not silently overridden by leftover env config.
func TestResolveBootChatModel_DBDefaultWinsOverEnv(t *testing.T) {
	db := bootModelsTestDB(t)
	seedBootDefaultChatModel(t, db, "db-default-model")

	cfg := bootEnvCfg("openrouter", "env-model") // env ALSO set

	chatModel, name, err := resolveBootChatModel(cfg, db)
	require.NoError(t, err)
	require.NotNil(t, chatModel, "DB default must yield a chat model so AgentService initializes (no 502 after restart)")
	assert.Equal(t, "db-default-model", name, "DB default must win over env config")
}

// With no DB default, the env model is the fallback (env-only deployments).
func TestResolveBootChatModel_EnvFallbackWhenNoDBDefault(t *testing.T) {
	db := bootModelsTestDB(t) // empty
	cfg := bootEnvCfg("openrouter", "env-model")

	chatModel, name, err := resolveBootChatModel(cfg, db)
	require.NoError(t, err)
	require.NotNil(t, chatModel)
	assert.Equal(t, "env-model", name)
}

// Nothing configured anywhere → no chat model (AgentService is skipped; the
// pre-existing configless behavior is preserved).
func TestResolveBootChatModel_NilWhenNothingConfigured(t *testing.T) {
	db := bootModelsTestDB(t) // empty
	var cfg config.Config     // empty LLM

	chatModel, name, err := resolveBootChatModel(cfg, db)
	require.NoError(t, err)
	assert.Nil(t, chatModel)
	assert.Equal(t, "", name)
}

// db == nil (configless/no-DB boot) falls back to env.
func TestResolveBootChatModel_DBNilUsesEnv(t *testing.T) {
	cfg := bootEnvCfg("openrouter", "env-model")

	chatModel, name, err := resolveBootChatModel(cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, chatModel)
	assert.Equal(t, "env-model", name)
}
