package repository

import (
	"context"
	"testing"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupTestDB creates in-memory SQLite DB for tests
func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err, "failed to open in-memory SQLite")

	err = db.AutoMigrate(&models.AgentContextSnapshotModel{})
	require.NoError(t, err, "failed to migrate table")

	return db
}

// createTestSnapshot creates a test domain snapshot
func createTestSnapshot(sessionID, agentID string, status domain.AgentContextStatus) *domain.AgentContextSnapshot {
	return &domain.AgentContextSnapshot{
		ID:            uuid.New().String(),
		SessionID:     sessionID,
		AgentID:       agentID,
		SchemaVersion: domain.CurrentSchemaVersion,
		ContextData:   []byte(`[{"role":"user","content":"test"}]`),
		StepNumber:    1,
		TokenCount:    100,
		Status:        status,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
}

func TestAgentContextRepository_SaveAndLoad(t *testing.T) {
	db := setupTestDB(t)
	repo := NewAgentContextRepository(db)
	ctx := context.Background()

	sessionID := uuid.New().String()
	agentID := "supervisor"

	// Create and save snapshot
	snapshot := createTestSnapshot(sessionID, agentID, domain.AgentContextStatusActive)
	originalData := snapshot.ContextData

	err := repo.Save(ctx, snapshot)
	require.NoError(t, err)
	assert.NotEmpty(t, snapshot.ID, "ID should be generated")

	// Load snapshot
	loaded, err := repo.Load(ctx, sessionID, agentID)
	require.NoError(t, err)
	require.NotNil(t, loaded)

	// Verify data identity
	assert.Equal(t, sessionID, loaded.SessionID)
	assert.Equal(t, agentID, loaded.AgentID)
	assert.Equal(t, "supervisor", loaded.AgentID)
	assert.Equal(t, domain.CurrentSchemaVersion, loaded.SchemaVersion)
	assert.Equal(t, originalData, loaded.ContextData)
	assert.Equal(t, 1, loaded.StepNumber)
	assert.Equal(t, 100, loaded.TokenCount)
	assert.Equal(t, domain.AgentContextStatusActive, loaded.Status)
}

func TestAgentContextRepository_Upsert(t *testing.T) {
	db := setupTestDB(t)
	repo := NewAgentContextRepository(db)
	ctx := context.Background()

	sessionID := uuid.New().String()
	agentID := "supervisor"

	// Save first snapshot
	snapshot1 := createTestSnapshot(sessionID, agentID, domain.AgentContextStatusActive)
	snapshot1.StepNumber = 1
	snapshot1.TokenCount = 100

	err := repo.Save(ctx, snapshot1)
	require.NoError(t, err)

	// Save second snapshot with same agent_id (upsert)
	snapshot2 := createTestSnapshot(sessionID, agentID, domain.AgentContextStatusCompacted)
	snapshot2.StepNumber = 5
	snapshot2.TokenCount = 500

	err = repo.Save(ctx, snapshot2)
	require.NoError(t, err)

	// Load and verify upsert behavior
	loaded, err := repo.Load(ctx, sessionID, agentID)
	require.NoError(t, err)
	require.NotNil(t, loaded)

	// Verify data was updated, not duplicated
	assert.Equal(t, 5, loaded.StepNumber)
	assert.Equal(t, 500, loaded.TokenCount)
	assert.Equal(t, domain.AgentContextStatusCompacted, loaded.Status)

	// Verify no duplicates in DB
	var count int64
	db.Model(&models.AgentContextSnapshotModel{}).Where("agent_id = ?", agentID).Count(&count)
	assert.Equal(t, int64(1), count, "should have exactly 1 record, not duplicated")
}

func TestAgentContextRepository_LoadNotFound(t *testing.T) {
	db := setupTestDB(t)
	repo := NewAgentContextRepository(db)
	ctx := context.Background()

	sessionID := uuid.New().String()
	agentID := "non-existent"

	// Load non-existent snapshot
	loaded, err := repo.Load(ctx, sessionID, agentID)
	require.NoError(t, err, "should return nil, nil (not error)")
	assert.Nil(t, loaded, "should return nil for not found")
}

func TestAgentContextRepository_Delete(t *testing.T) {
	db := setupTestDB(t)
	repo := NewAgentContextRepository(db)
	ctx := context.Background()

	sessionID := uuid.New().String()
	agentID := "supervisor"

	// Save snapshot
	snapshot := createTestSnapshot(sessionID, agentID, domain.AgentContextStatusActive)
	err := repo.Save(ctx, snapshot)
	require.NoError(t, err)

	// Verify saved
	loaded, err := repo.Load(ctx, sessionID, agentID)
	require.NoError(t, err)
	require.NotNil(t, loaded)

	// Delete snapshot
	err = repo.Delete(ctx, sessionID, agentID)
	require.NoError(t, err)

	// Load after delete should return nil
	loaded, err = repo.Load(ctx, sessionID, agentID)
	require.NoError(t, err)
	assert.Nil(t, loaded, "should return nil after delete")
}

func TestAgentContextRepository_FindActive(t *testing.T) {
	db := setupTestDB(t)
	repo := NewAgentContextRepository(db)
	ctx := context.Background()

	sessionID := uuid.New().String()

	// Save 3 snapshots: 2 active, 1 completed
	snapshot1 := createTestSnapshot(sessionID, "agent-1", domain.AgentContextStatusActive)
	err := repo.Save(ctx, snapshot1)
	require.NoError(t, err)

	snapshot2 := createTestSnapshot(sessionID, "agent-2", domain.AgentContextStatusCompacted)
	err = repo.Save(ctx, snapshot2)
	require.NoError(t, err)

	snapshot3 := createTestSnapshot(sessionID, "agent-3", domain.AgentContextStatusActive)
	err = repo.Save(ctx, snapshot3)
	require.NoError(t, err)

	// FindActive should return 2 snapshots
	active, err := repo.FindActive(ctx)
	require.NoError(t, err)
	require.Len(t, active, 2, "should return exactly 2 active snapshots")

	// Verify both are active
	for _, snap := range active {
		assert.Equal(t, domain.AgentContextStatusActive, snap.Status)
	}

	// Verify correct agent IDs (order may vary)
	agentIDs := []string{active[0].AgentID, active[1].AgentID}
	assert.Contains(t, agentIDs, "agent-1")
	assert.Contains(t, agentIDs, "agent-3")
}
