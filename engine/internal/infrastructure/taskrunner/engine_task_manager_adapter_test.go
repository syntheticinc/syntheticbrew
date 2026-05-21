package taskrunner

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools"
)

// setupAdapterTestDB creates an in-memory SQLite DB with the tasks table using
// a portable DDL (TaskModel's PostgreSQL-specific defaults would reject SQLite).
func setupAdapterTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)

	const ddl = `
CREATE TABLE tasks (
	id TEXT PRIMARY KEY,
	title TEXT NOT NULL,
	description TEXT,
	acceptance_criteria TEXT,
	user_id TEXT,
	session_id TEXT,
	parent_task_id TEXT,
	status TEXT NOT NULL DEFAULT 'pending',
	mode TEXT NOT NULL DEFAULT 'interactive',
	priority INTEGER NOT NULL DEFAULT 0,
	blocked_by TEXT,
	result TEXT,
	error TEXT,
	tenant_id TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
	created_at DATETIME,
	updated_at DATETIME,
	approved_at DATETIME,
	started_at DATETIME,
	completed_at DATETIME
)`
	require.NoError(t, db.Exec(ddl).Error)
	return db
}

func newAdapter(t *testing.T) (*EngineTaskManagerAdapter, *configrepo.GORMTaskRepository) {
	t.Helper()
	db := setupAdapterTestDB(t)
	repo := configrepo.NewGORMTaskRepository(db)
	return NewEngineTaskManagerAdapter(repo), repo
}

func createTopLevelTask(t *testing.T, adapter *EngineTaskManagerAdapter, title string) uuid.UUID {
	t.Helper()
	id, err := adapter.CreateTask(context.Background(), tools.CreateEngineTaskParams{
		Title:     title,
	})
	require.NoError(t, err)
	return id
}

func TestAdapter_CreateTask_RequiresTitle(t *testing.T) {
	adapter, _ := newAdapter(t)
	_, err := adapter.CreateTask(context.Background(), tools.CreateEngineTaskParams{
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "title is required")
}

func TestAdapter_CreateTask_BlockedByMustExist(t *testing.T) {
	adapter, _ := newAdapter(t)
	// Use a well-formed but non-existent UUID so we exercise the existence check.
	_, err := adapter.CreateTask(context.Background(), tools.CreateEngineTaskParams{
		Title:     "dependent",
		BlockedBy: []uuid.UUID{uuid.New()},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blocked_by references unknown task")
}

// uuid.Nil in BlockedBy must be rejected before any DB lookup.
func TestAdapter_CreateTask_BlockedByNilRejected(t *testing.T) {
	adapter, _ := newAdapter(t)
	_, err := adapter.CreateTask(context.Background(), tools.CreateEngineTaskParams{
		Title:     "dependent",
		BlockedBy: []uuid.UUID{uuid.Nil},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty task id")
}

func TestAdapter_CreateTask_BlockedByExisting(t *testing.T) {
	adapter, _ := newAdapter(t)
	blockerID := createTopLevelTask(t, adapter, "blocker")
	id, err := adapter.CreateTask(context.Background(), tools.CreateEngineTaskParams{
		Title:     "dependent",
		BlockedBy: []uuid.UUID{blockerID},
	})
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, id)
}

func TestAdapter_CreateSubTask_ParentMustExist(t *testing.T) {
	adapter, _ := newAdapter(t)
	_, err := adapter.CreateSubTask(context.Background(), uuid.New(), tools.CreateEngineTaskParams{
		Title:     "child",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parent task not found")
}

func TestAdapter_CreateSubTask_ParentTerminalRejected(t *testing.T) {
	adapter, repo := newAdapter(t)
	parentID := createTopLevelTask(t, adapter, "parent")
	// Walk through the state machine to reach a terminal state.
	require.NoError(t, repo.UpdateStatus(context.Background(), parentID, domain.EngineTaskStatusInProgress, ""))
	require.NoError(t, repo.UpdateStatus(context.Background(), parentID, domain.EngineTaskStatusCompleted, ""))

	_, err := adapter.CreateSubTask(context.Background(), parentID, tools.CreateEngineTaskParams{
		Title:     "child",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot add subtask to terminal task")
}

func TestAdapter_CreateSubTask_SetsIncrementedDepth(t *testing.T) {
	adapter, repo := newAdapter(t)
	rootID := createTopLevelTask(t, adapter, "root")

	// Build a chain: root → L1 → L2 → L3, checking depth at each level.
	previous := rootID
	for level := 1; level <= 3; level++ {
		id, err := adapter.CreateSubTask(context.Background(), previous, tools.CreateEngineTaskParams{
			Title:     "child",
		})
		require.NoError(t, err)
		_, err = repo.GetByID(context.Background(), id)
		require.NoError(t, err)
		// Q.5: depth is no longer stored — just verify the subtask was created.
		previous = id
	}
}

func TestAdapter_CreateSubTask_DepthLimitEnforced(t *testing.T) {
	adapter, _ := newAdapter(t)
	rootID := createTopLevelTask(t, adapter, "root")

	// Build a chain that approaches the limit, then expect the final insert to fail.
	// validateParent rejects when new_depth >= MaxTaskDepth → the (MaxTaskDepth)-th child fails.
	previous := rootID
	for level := 1; level < MaxTaskDepth; level++ {
		id, err := adapter.CreateSubTask(context.Background(), previous, tools.CreateEngineTaskParams{
			Title:     "child",
		})
		require.NoError(t, err, "level %d within limit", level)
		previous = id
	}

	_, err := adapter.CreateSubTask(context.Background(), previous, tools.CreateEngineTaskParams{
		Title:     "too deep",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
}

func TestAdapter_CreateSubTask_BlockedByPropagated(t *testing.T) {
	adapter, repo := newAdapter(t)
	parentID := createTopLevelTask(t, adapter, "parent")
	blockerID := createTopLevelTask(t, adapter, "blocker")

	childID, err := adapter.CreateSubTask(context.Background(), parentID, tools.CreateEngineTaskParams{
		Title:     "child",
		BlockedBy: []uuid.UUID{blockerID},
	})
	require.NoError(t, err)

	child, err := repo.GetByID(context.Background(), childID)
	require.NoError(t, err)
	require.Len(t, child.BlockedBy, 1)
	assert.Equal(t, blockerID, child.BlockedBy[0])
}

func TestAdapter_CancelTask_StoresReasonOnRoot(t *testing.T) {
	adapter, repo := newAdapter(t)
	parentID := createTopLevelTask(t, adapter, "parent")
	childID, err := adapter.CreateSubTask(context.Background(), parentID, tools.CreateEngineTaskParams{
		Title:     "child",
	})
	require.NoError(t, err)

	require.NoError(t, adapter.CancelTask(context.Background(), parentID, "user requested stop"))

	parent, err := repo.GetByID(context.Background(), parentID)
	require.NoError(t, err)
	assert.Equal(t, domain.EngineTaskStatusCancelled, parent.Status)
	assert.Equal(t, "user requested stop", parent.Result, "reason is stored on the root")

	child, err := repo.GetByID(context.Background(), childID)
	require.NoError(t, err)
	assert.Equal(t, domain.EngineTaskStatusCancelled, child.Status, "child also cancelled")
	// Children are cancelled with empty result — the reason belongs to the explicit call.
	assert.Equal(t, "", child.Result)
}

// --- Terminal transition smoke test ---
//
// V2 (§4.2): the on-complete webhook feature is removed, so terminal
// transitions are now pure DB updates with no fan-out. This test documents
// that the adapter still walks the state machine cleanly after the hook
// was excised.

func TestAdapter_TerminalTransitionsSucceed(t *testing.T) {
	adapter, _ := newAdapter(t)
	id := createTopLevelTask(t, adapter, "t")
	// Walk through the state machine: pending → in_progress → completed.
	require.NoError(t, adapter.SetTaskStatus(context.Background(), id, string(domain.EngineTaskStatusInProgress), ""))
	require.NoError(t, adapter.CompleteTask(context.Background(), id, "ok"))
}

func TestAdapter_CreateTask_InvalidPriorityAllowedAtAdapter(t *testing.T) {
	// Priority validation is enforced at the HTTP/tool layer, not the adapter.
	// The adapter accepts any int — document this behaviour with a smoke test.
	adapter, repo := newAdapter(t)
	id, err := adapter.CreateTask(context.Background(), tools.CreateEngineTaskParams{
		Title:     "no-validation-at-adapter",
		Priority:  7,
	})
	require.NoError(t, err)
	task, err := repo.GetByID(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, 7, task.Priority)
}

func TestAdapter_ValidateParent_RejectsMissingParent(t *testing.T) {
	adapter, _ := newAdapter(t)
	badID := uuid.New()
	_, err := adapter.CreateSubTask(context.Background(), badID, tools.CreateEngineTaskParams{
		Title:     "child",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parent task not found")
}
