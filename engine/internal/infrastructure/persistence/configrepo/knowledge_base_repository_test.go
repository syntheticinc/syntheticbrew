package configrepo

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// setupKBTestDB returns an in-memory SQLite DB with the columns and tables
// that ReplaceAgentKBs touches: agents, knowledge_bases, knowledge_base_agents.
// Column shapes mirror the production Postgres schema; SQLite is used here
// because the test runs in the same configrepo package and does not need
// Postgres-specific features (gen_random_uuid is handled by explicit IDs).
func setupKBTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)

	require.NoError(t, db.Exec(`CREATE TABLE agents (
		id         TEXT PRIMARY KEY,
		tenant_id  TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
		name       TEXT NOT NULL,
		created_at DATETIME,
		updated_at DATETIME
	)`).Error)

	require.NoError(t, db.Exec(`CREATE TABLE knowledge_bases (
		id                  TEXT PRIMARY KEY,
		tenant_id           TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
		name                TEXT NOT NULL,
		description         TEXT,
		embedding_model_id  TEXT,
		created_at          DATETIME,
		updated_at          DATETIME
	)`).Error)

	require.NoError(t, db.Exec(`CREATE TABLE knowledge_base_agents (
		knowledge_base_id TEXT NOT NULL,
		agent_id          TEXT NOT NULL,
		PRIMARY KEY (knowledge_base_id, agent_id)
	)`).Error)

	return db
}

// seedAgent inserts a minimal agent row with the given id/tenant.
func seedAgent(t *testing.T, db *gorm.DB, agentID, tenantID, name string) {
	t.Helper()
	require.NoError(t, db.Exec(
		"INSERT INTO agents (id, tenant_id, name) VALUES (?, ?, ?)",
		agentID, tenantID, name,
	).Error)
}

// seedKB inserts a minimal knowledge_bases row.
func seedKB(t *testing.T, db *gorm.DB, kbID, tenantID, name string) {
	t.Helper()
	require.NoError(t, db.Exec(
		"INSERT INTO knowledge_bases (id, tenant_id, name) VALUES (?, ?, ?)",
		kbID, tenantID, name,
	).Error)
}

// countKBLinks returns the number of knowledge_base_agents rows for an agent.
func countKBLinks(t *testing.T, db *gorm.DB, agentID string) int {
	t.Helper()
	var n int64
	require.NoError(t, db.Model(&models.KnowledgeBaseAgent{}).
		Where("agent_id = ?", agentID).Count(&n).Error)
	return int(n)
}

const (
	tenantA = "aaaaaaaa-0000-0000-0000-000000000001"
	tenantB = "bbbbbbbb-0000-0000-0000-000000000002"
)

// TestReplaceAgentKBs_HappyPath covers the ordinary case: agent + two KBs
// in the same tenant, caller links both.
func TestReplaceAgentKBs_HappyPath(t *testing.T) {
	db := setupKBTestDB(t)
	repo := NewGORMKnowledgeBaseRepository(db)

	agentID := "agent-1"
	kb1, kb2 := "kb-1", "kb-2"
	seedAgent(t, db, agentID, tenantA, "a1")
	seedKB(t, db, kb1, tenantA, "knowledge-1")
	seedKB(t, db, kb2, tenantA, "knowledge-2")

	ctx := domain.WithTenantID(context.Background(), tenantA)
	require.NoError(t, repo.ReplaceAgentKBs(ctx, agentID, []string{kb1, kb2}))

	assert.Equal(t, 2, countKBLinks(t, db, agentID))
}

// TestReplaceAgentKBs_CrossTenantAgent: agent lives in tenantA, caller
// operates under tenantB. Must return ErrAgentNotInTenant (sentinel) and
// not mutate the table.
func TestReplaceAgentKBs_CrossTenantAgent(t *testing.T) {
	db := setupKBTestDB(t)
	repo := NewGORMKnowledgeBaseRepository(db)

	agentID := "agent-1"
	kb1 := "kb-1"
	seedAgent(t, db, agentID, tenantA, "a1")
	seedKB(t, db, kb1, tenantB, "k-from-b")

	ctx := domain.WithTenantID(context.Background(), tenantB)
	err := repo.ReplaceAgentKBs(ctx, agentID, []string{kb1})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAgentNotInTenant),
		"expected ErrAgentNotInTenant, got %v", err)
	assert.Equal(t, 0, countKBLinks(t, db, agentID),
		"failed call must not mutate the join table")
}

// TestReplaceAgentKBs_CrossTenantKB: agent + KB live in different tenants.
// Must return ErrKBsNotInTenant (sentinel) and not mutate.
func TestReplaceAgentKBs_CrossTenantKB(t *testing.T) {
	db := setupKBTestDB(t)
	repo := NewGORMKnowledgeBaseRepository(db)

	agentID := "agent-1"
	kbOther := "kb-other"
	seedAgent(t, db, agentID, tenantA, "a1")
	seedKB(t, db, kbOther, tenantB, "k-other")

	ctx := domain.WithTenantID(context.Background(), tenantA)
	err := repo.ReplaceAgentKBs(ctx, agentID, []string{kbOther})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrKBsNotInTenant),
		"expected ErrKBsNotInTenant, got %v", err)
	assert.Equal(t, 0, countKBLinks(t, db, agentID),
		"failed call must not mutate the join table")
}

// TestReplaceAgentKBs_EmptySlice_UnlinksAll: calling with [] must remove all
// existing links. Idempotent: calling again with [] must remain at 0 links.
func TestReplaceAgentKBs_EmptySlice_UnlinksAll(t *testing.T) {
	db := setupKBTestDB(t)
	repo := NewGORMKnowledgeBaseRepository(db)

	agentID := "agent-1"
	kb1, kb2 := "kb-1", "kb-2"
	seedAgent(t, db, agentID, tenantA, "a1")
	seedKB(t, db, kb1, tenantA, "k1")
	seedKB(t, db, kb2, tenantA, "k2")

	ctx := domain.WithTenantID(context.Background(), tenantA)
	// Seed initial membership = {kb1, kb2}.
	require.NoError(t, repo.ReplaceAgentKBs(ctx, agentID, []string{kb1, kb2}))
	require.Equal(t, 2, countKBLinks(t, db, agentID))

	// Unlink all.
	require.NoError(t, repo.ReplaceAgentKBs(ctx, agentID, []string{}))
	assert.Equal(t, 0, countKBLinks(t, db, agentID))

	// Idempotent: unlinking an already-empty set is a no-op, not an error.
	require.NoError(t, repo.ReplaceAgentKBs(ctx, agentID, []string{}))
	assert.Equal(t, 0, countKBLinks(t, db, agentID))
}

// TestReplaceAgentKBs_DuplicateKBIDs: caller passes the same KB twice. The
// repo must dedupe internally so the final COUNT is 2 (not 3) without failing
// on the primary-key constraint.
func TestReplaceAgentKBs_DuplicateKBIDs(t *testing.T) {
	db := setupKBTestDB(t)
	repo := NewGORMKnowledgeBaseRepository(db)

	agentID := "agent-1"
	kb1, kb2 := "kb-1", "kb-2"
	seedAgent(t, db, agentID, tenantA, "a1")
	seedKB(t, db, kb1, tenantA, "k1")
	seedKB(t, db, kb2, tenantA, "k2")

	ctx := domain.WithTenantID(context.Background(), tenantA)
	// kb1 appears twice in the input — must be deduped, not fail on PK.
	require.NoError(t, repo.ReplaceAgentKBs(ctx, agentID, []string{kb1, kb1, kb2}))
	assert.Equal(t, 2, countKBLinks(t, db, agentID),
		"duplicates in the input must be deduped before insert")
}

// TestReplaceAgentKBs_Replacement_DeletesOldAddsNew: starting with {kb1, kb2},
// replacing with {kb2, kb3} must leave exactly {kb2, kb3} in the join table.
func TestReplaceAgentKBs_Replacement_DeletesOldAddsNew(t *testing.T) {
	db := setupKBTestDB(t)
	repo := NewGORMKnowledgeBaseRepository(db)

	agentID := "agent-1"
	kb1, kb2, kb3 := "kb-1", "kb-2", "kb-3"
	seedAgent(t, db, agentID, tenantA, "a1")
	for _, id := range []string{kb1, kb2, kb3} {
		seedKB(t, db, id, tenantA, id)
	}

	ctx := domain.WithTenantID(context.Background(), tenantA)
	require.NoError(t, repo.ReplaceAgentKBs(ctx, agentID, []string{kb1, kb2}))
	require.Equal(t, 2, countKBLinks(t, db, agentID))

	require.NoError(t, repo.ReplaceAgentKBs(ctx, agentID, []string{kb2, kb3}))
	assert.Equal(t, 2, countKBLinks(t, db, agentID))

	// Verify concrete IDs: kb1 removed, kb3 added.
	var ids []string
	require.NoError(t, db.Model(&models.KnowledgeBaseAgent{}).
		Where("agent_id = ?", agentID).Pluck("knowledge_base_id", &ids).Error)
	assert.ElementsMatch(t, []string{kb2, kb3}, ids)
}

// TestReplaceAgentKBs_ValidationFailsBeforeWrite: verifies that when validation
// rejects the input (cross-tenant KB), no writes are applied. This is not a
// transaction-rollback test per se (validation runs before the tx begins) but
// it covers the equivalent user-facing invariant: a failed call leaves the
// existing membership exactly as it was.
func TestReplaceAgentKBs_ValidationFailsBeforeWrite(t *testing.T) {
	db := setupKBTestDB(t)
	repo := NewGORMKnowledgeBaseRepository(db)

	agentID := "agent-1"
	kbOK := "kb-ok"
	kbBad := "kb-wrong-tenant"
	seedAgent(t, db, agentID, tenantA, "a1")
	seedKB(t, db, kbOK, tenantA, "k-ok")
	seedKB(t, db, kbBad, tenantB, "k-wrong-tenant")

	ctx := domain.WithTenantID(context.Background(), tenantA)

	// Seed initial membership = {kbOK}.
	require.NoError(t, repo.ReplaceAgentKBs(ctx, agentID, []string{kbOK}))
	require.Equal(t, 1, countKBLinks(t, db, agentID))

	// Attempt to replace with a cross-tenant KB — must fail cleanly and
	// leave the existing link intact (no partial wipe).
	err := repo.ReplaceAgentKBs(ctx, agentID, []string{kbOK, kbBad})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrKBsNotInTenant))
	assert.Equal(t, 1, countKBLinks(t, db, agentID),
		"existing membership must remain intact after a validation failure")
}
