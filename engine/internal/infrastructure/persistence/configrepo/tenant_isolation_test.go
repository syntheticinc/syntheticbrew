package configrepo

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// setupSettingDB creates an in-memory SQLite DB with the settings table.
func setupSettingDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE settings (
		tenant_id TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
		key       TEXT NOT NULL,
		value     TEXT NOT NULL DEFAULT '{}',
		created_at DATETIME,
		updated_at DATETIME,
		PRIMARY KEY (tenant_id, key)
	)`).Error)
	return db
}

// setupSchemaDB creates an in-memory SQLite DB with the schemas table.
func setupSchemaDB(t *testing.T) *gorm.DB {
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
	// agent_schemas join table
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS agent_schemas (
		agent_id  TEXT NOT NULL,
		schema_id TEXT NOT NULL,
		PRIMARY KEY (agent_id, schema_id)
	)`).Error)
	// agents and agent_relations needed by deriveAgentNames
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

// ---- setting isolation ----

func TestSettingRepo_TenantIsolation_Set(t *testing.T) {
	db := setupSettingDB(t)
	repo := NewGORMSettingRepository(db)

	ctxA := domain.WithTenantID(context.Background(), "aaaaaaaa-0000-0000-0000-000000000001")
	ctxB := domain.WithTenantID(context.Background(), "bbbbbbbb-0000-0000-0000-000000000002")

	require.NoError(t, repo.Set(ctxA, "color", "red"))

	// tenantB must not see tenantA's setting
	got, err := repo.Get(ctxB, "color")
	require.NoError(t, err)
	assert.Nil(t, got, "tenantB must not see tenantA setting")

	// tenantA can see its own setting
	gotA, err := repo.Get(ctxA, "color")
	require.NoError(t, err)
	require.NotNil(t, gotA)
}

func TestSettingRepo_TenantIsolation_List(t *testing.T) {
	db := setupSettingDB(t)
	repo := NewGORMSettingRepository(db)

	ctxA := domain.WithTenantID(context.Background(), "aaaaaaaa-0000-0000-0000-000000000001")
	ctxB := domain.WithTenantID(context.Background(), "bbbbbbbb-0000-0000-0000-000000000002")

	require.NoError(t, repo.Set(ctxA, "k1", "v1"))
	require.NoError(t, repo.Set(ctxA, "k2", "v2"))

	settings, err := repo.List(ctxB)
	require.NoError(t, err)
	assert.Empty(t, settings, "tenantB must not see tenantA settings")
}

func TestSettingRepo_CEFallback(t *testing.T) {
	db := setupSettingDB(t)
	repo := NewGORMSettingRepository(db)

	// Empty context (CE mode) falls back to CETenantID
	ctx := context.Background()
	require.NoError(t, repo.Set(ctx, "mode", "ce"))

	got, err := repo.Get(ctx, "mode")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, domain.CETenantID, got.TenantID)
}

// ---- schema isolation ----

func TestSchemaRepo_TenantIsolation_Create(t *testing.T) {
	db := setupSchemaDB(t)
	repo := NewGORMSchemaRepository(db)

	ctxA := domain.WithTenantID(context.Background(), "aaaaaaaa-0000-0000-0000-000000000001")
	ctxB := domain.WithTenantID(context.Background(), "bbbbbbbb-0000-0000-0000-000000000002")

	recA := &SchemaRecord{Name: "schema-alpha"}
	require.NoError(t, repo.Create(ctxA, recA))

	// tenantB list must be empty
	schemasB, err := repo.List(ctxB)
	require.NoError(t, err)
	assert.Empty(t, schemasB, "tenantB must not see tenantA schemas")

	// tenantA list must have exactly one schema
	schemasA, err := repo.List(ctxA)
	require.NoError(t, err)
	assert.Len(t, schemasA, 1)
	assert.Equal(t, "schema-alpha", schemasA[0].Name)
}

func TestSchemaRepo_TenantIsolation_GetByID(t *testing.T) {
	db := setupSchemaDB(t)
	repo := NewGORMSchemaRepository(db)

	ctxA := domain.WithTenantID(context.Background(), "aaaaaaaa-0000-0000-0000-000000000001")
	ctxB := domain.WithTenantID(context.Background(), "bbbbbbbb-0000-0000-0000-000000000002")

	recA := &SchemaRecord{Name: "private-schema"}
	require.NoError(t, repo.Create(ctxA, recA))
	require.NotEmpty(t, recA.ID, "Create must populate ID")

	// tenantB GetByID on tenantA's schema must not return the record.
	// Acceptable responses: nil result with a "not found" error, or nil result with no error.
	got, err := repo.GetByID(ctxB, recA.ID)
	if err == nil {
		assert.Nil(t, got, "tenantB must not access tenantA schema by ID")
	} else {
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "not found")
	}
}

func TestSchemaRepo_TenantIsolation_Delete(t *testing.T) {
	db := setupSchemaDB(t)
	repo := NewGORMSchemaRepository(db)

	ctxA := domain.WithTenantID(context.Background(), "aaaaaaaa-0000-0000-0000-000000000001")
	ctxB := domain.WithTenantID(context.Background(), "bbbbbbbb-0000-0000-0000-000000000002")

	recA := &SchemaRecord{Name: "to-delete"}
	require.NoError(t, repo.Create(ctxA, recA))

	// tenantB Delete must not remove tenantA's schema.
	// The call may return an error (not found) — that's acceptable and proves isolation.
	_ = repo.Delete(ctxB, recA.ID)

	// tenantA's schema must still exist regardless of what tenantB did.
	got, err := repo.GetByID(ctxA, recA.ID)
	require.NoError(t, err)
	assert.NotNil(t, got, "tenantA schema must survive tenantB Delete")
}
