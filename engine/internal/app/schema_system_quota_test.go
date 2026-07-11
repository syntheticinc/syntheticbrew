package app

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
)

// setupSchemaQuotaDB creates an in-memory SQLite DB with the schemas table
// (plus the agents/agent_relations tables that List's agent derivation reads)
// so the real GORMSchemaRepository can seed and count schemas.
func setupSchemaQuotaDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE schemas (
		id                  TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
		tenant_id           TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
		name                TEXT NOT NULL,
		description         TEXT,
		chat_enabled        INTEGER NOT NULL DEFAULT 0,
		chat_last_fired_at  DATETIME,
		is_system           INTEGER NOT NULL DEFAULT 0,
		entry_agent_id      TEXT,
		created_at          DATETIME,
		updated_at          DATETIME,
		UNIQUE(tenant_id, name)
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS agents (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
		name TEXT NOT NULL
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS agent_relations (
		id TEXT PRIMARY KEY,
		schema_id TEXT NOT NULL,
		tenant_id TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
		source_agent_id TEXT NOT NULL,
		target_agent_id TEXT NOT NULL,
		relation_type TEXT NOT NULL
	)`).Error)
	return db
}

// TestSchemaQuota_ExcludesSystemSchema reproduces B6: a freshly provisioned tenant is
// seeded with one user schema ("my-workspace") plus one engine-managed system
// schema ("builder-schema"). Both the quota counter (the plugin limit gate) and the
// usage-status aggregate must count only the user schema — otherwise a tenant
// with schemas_limit=1 is blocked at "200% of Schemas limit" out of the box.
func TestSchemaQuota_ExcludesSystemSchema(t *testing.T) {
	const tenant = "aaaaaaaa-0000-0000-0000-000000000001"

	db := setupSchemaQuotaDB(t)
	repo := configrepo.NewGORMSchemaRepository(db)

	seedCtx := domain.WithTenantID(context.Background(), tenant)
	require.NoError(t, repo.Create(seedCtx, &configrepo.SchemaRecord{Name: "my-workspace"}))
	require.NoError(t, repo.Create(seedCtx, &configrepo.SchemaRecord{Name: "builder-schema", IsSystem: true}))

	// Quota count (SchemaCounterFunc consumed by the plugin limit gate).
	counter := NewSchemaCounter(repo)
	quotaCount, err := counter(context.Background(), tenant)
	require.NoError(t, err)
	assert.Equal(t, 1, quotaCount, "schema quota must exclude the system builder-schema")

	// Usage-status schemas.used aggregate.
	adapter := newUsageStatusAdapter(
		&fakeUsagePolicyReader{values: map[string]string{domain.PolicySchemasLimit: "1"}},
		fakeActiveUserCounter{count: 0},
		repo,
		fakeKnowledgeCounter{count: 0},
		fakeTurnLimitReader{cfg: nil},
		fakeTurnCounterReader{counter: nil},
	)
	got, err := adapter.UsageStatus(seedCtx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), got.Schemas.Used, "usage-status schemas.used must exclude the system builder-schema")
	require.NotNil(t, got.Schemas.Limit)
	assert.Equal(t, int64(1), *got.Schemas.Limit)
}
