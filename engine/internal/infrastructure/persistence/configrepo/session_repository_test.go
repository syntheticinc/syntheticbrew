package configrepo

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// setupSessionTestDB creates an in-memory SQLite DB with the sessions table.
// SessionModel uses Postgres-specific JSONB which SQLite cannot honour, so
// the metadata column is approximated as TEXT — sufficient for testing
// the CreateIfNotExists conflict-handling behaviour.
func setupSessionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)

	const ddl = `
CREATE TABLE sessions (
	id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
	schema_id TEXT NOT NULL,
	user_sub TEXT NOT NULL DEFAULT '',
	title TEXT,
	metadata TEXT NOT NULL DEFAULT '{}',
	status TEXT NOT NULL DEFAULT 'active',
	created_at DATETIME,
	updated_at DATETIME,
	completed_at DATETIME
)`
	require.NoError(t, db.Exec(ddl).Error)
	return db
}

// TestSessionRepository_CreateIfNotExists_Idempotent verifies the WARN A
// fix: the second insert of a session with the same ID must succeed
// silently without producing a duplicate-key error, AND must not
// modify the existing row. This is the scenario partner saw after
// every engine restart — the in-memory registry lost its state, every
// existing session triggered a "first seen" path that re-INSERTed.
func TestSessionRepository_CreateIfNotExists_Idempotent(t *testing.T) {
	db := setupSessionTestDB(t)
	repo := NewGORMSessionRepository(db)

	sessionID := uuid.NewString()
	schemaID := uuid.NewString()
	tenant := "00000000-0000-0000-0000-000000000001"
	ctx := domain.WithTenantID(context.Background(), tenant)

	first := &models.SessionModel{
		ID:       sessionID,
		SchemaID: schemaID,
		UserSub:  "alice",
		Status:   "active",
	}
	require.NoError(t, repo.CreateIfNotExists(ctx, first))

	// Second call with same ID but different UserSub — should be no-op,
	// not overwrite. Confirms ON CONFLICT DO NOTHING semantic.
	second := &models.SessionModel{
		ID:       sessionID,
		SchemaID: schemaID,
		UserSub:  "bob-should-be-ignored",
		Status:   "active",
	}
	require.NoError(t, repo.CreateIfNotExists(ctx, second),
		"second CreateIfNotExists must succeed (no duplicate-key error)")

	// Verify the original row is unchanged.
	got, err := repo.Get(ctx, sessionID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "alice", got.UserSub,
		"existing row must NOT be overwritten by second CreateIfNotExists")
}

// TestSessionRepository_CreateIfNotExists_FreshSession verifies the
// happy path — a session that does NOT already exist gets inserted.
func TestSessionRepository_CreateIfNotExists_FreshSession(t *testing.T) {
	db := setupSessionTestDB(t)
	repo := NewGORMSessionRepository(db)

	sessionID := uuid.NewString()
	tenant := "00000000-0000-0000-0000-000000000001"
	ctx := domain.WithTenantID(context.Background(), tenant)

	session := &models.SessionModel{
		ID:       sessionID,
		SchemaID: uuid.NewString(),
		UserSub:  "alice",
		Status:   "active",
	}
	require.NoError(t, repo.CreateIfNotExists(ctx, session))

	got, err := repo.Get(ctx, sessionID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "alice", got.UserSub)
	assert.Equal(t, tenant, got.TenantID)
}
