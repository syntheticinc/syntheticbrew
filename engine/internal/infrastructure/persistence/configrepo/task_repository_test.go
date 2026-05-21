package configrepo

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// setupTaskTestDB creates an in-memory SQLite DB with the TaskModel table.
// The TaskModel's PostgreSQL-specific defaults (gen_random_uuid) are not
// compatible with SQLite, so we create the schema manually for tests.
func setupTaskTestDB(t *testing.T) *gorm.DB {
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

func mustCreateTask(t *testing.T, repo *GORMTaskRepository, task *domain.EngineTask) uuid.UUID {
	t.Helper()
	require.NoError(t, repo.Create(context.Background(), task))
	return task.ID
}

func newPendingSubtask(parentID uuid.UUID, priority int) *domain.EngineTask {
	return &domain.EngineTask{
		ID:           uuid.New(),
		Title:        "Subtask",
		Status:       domain.EngineTaskStatusPending,
		Mode:         domain.TaskModeInteractive,
		ParentTaskID: &parentID,
		Priority:     priority,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
}

func newParent(t *testing.T, repo *GORMTaskRepository) *domain.EngineTask {
	t.Helper()
	parent := &domain.EngineTask{
		ID: uuid.New(), Title: "Parent", Status: domain.EngineTaskStatusInProgress,
		Mode: domain.TaskModeInteractive,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	mustCreateTask(t, repo, parent)
	return parent
}

func TestGORMTaskRepository_GetReadySubtasks_NoBlockers(t *testing.T) {
	repo := NewGORMTaskRepository(setupTaskTestDB(t))
	ctx := context.Background()

	parent := newParent(t, repo)

	sub1 := newPendingSubtask(parent.ID, 0)
	sub2 := newPendingSubtask(parent.ID, 0)
	mustCreateTask(t, repo, sub1)
	mustCreateTask(t, repo, sub2)

	ready, err := repo.GetReadySubtasks(ctx, parent.ID)
	require.NoError(t, err)
	assert.Len(t, ready, 2, "both subtasks without blockers are ready")
}

func TestGORMTaskRepository_GetReadySubtasks_BlockedUntilTerminal(t *testing.T) {
	repo := NewGORMTaskRepository(setupTaskTestDB(t))
	ctx := context.Background()

	parent := newParent(t, repo)

	// Blocker in progress — lives OUTSIDE parent's subtree so it doesn't
	// compete for the "ready subtask" slot of our dependent task.
	blocker := &domain.EngineTask{
		ID: uuid.New(), Title: "Blocker", Status: domain.EngineTaskStatusInProgress,
		Mode: domain.TaskModeInteractive,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	mustCreateTask(t, repo, blocker)

	// Dependent subtask: blocked_by blocker
	dep := newPendingSubtask(parent.ID, 0)
	dep.BlockedBy = []uuid.UUID{blocker.ID}
	mustCreateTask(t, repo, dep)

	// Dependent should NOT be ready while blocker is in_progress
	ready, err := repo.GetReadySubtasks(ctx, parent.ID)
	require.NoError(t, err)
	assert.Empty(t, ready, "dependent task should not be ready while blocker is in_progress")

	// Mark blocker as completed
	blocker.Status = domain.EngineTaskStatusCompleted
	now := time.Now()
	blocker.CompletedAt = &now
	require.NoError(t, repo.Update(ctx, blocker))

	// Now dependent should be ready
	ready, err = repo.GetReadySubtasks(ctx, parent.ID)
	require.NoError(t, err)
	require.Len(t, ready, 1)
	assert.Equal(t, dep.ID, ready[0].ID)
}

func TestGORMTaskRepository_GetReadySubtasks_PartialBlockerResolution(t *testing.T) {
	repo := NewGORMTaskRepository(setupTaskTestDB(t))
	ctx := context.Background()

	parent := newParent(t, repo)

	// Blockers live OUTSIDE parent's subtree (another branch of the task graph).
	doneBlk := &domain.EngineTask{
		ID: uuid.New(), Title: "Done blocker", Status: domain.EngineTaskStatusCompleted,
		Mode: domain.TaskModeInteractive,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	openBlk := &domain.EngineTask{
		ID: uuid.New(), Title: "Open blocker", Status: domain.EngineTaskStatusPending,
		Mode: domain.TaskModeInteractive,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	mustCreateTask(t, repo, doneBlk)
	mustCreateTask(t, repo, openBlk)

	dep := newPendingSubtask(parent.ID, 0)
	dep.BlockedBy = []uuid.UUID{doneBlk.ID, openBlk.ID}
	mustCreateTask(t, repo, dep)

	ready, err := repo.GetReadySubtasks(ctx, parent.ID)
	require.NoError(t, err)
	assert.Empty(t, ready, "task with any non-terminal blocker is not ready")

	// Cancel the open blocker (cancelled is also terminal)
	openBlk.Status = domain.EngineTaskStatusCancelled
	require.NoError(t, repo.Update(ctx, openBlk))

	ready, err = repo.GetReadySubtasks(ctx, parent.ID)
	require.NoError(t, err)
	require.Len(t, ready, 1)
	assert.Equal(t, dep.ID, ready[0].ID)
}

func TestGORMTaskRepository_GetReadySubtasks_PriorityOrdering(t *testing.T) {
	repo := NewGORMTaskRepository(setupTaskTestDB(t))
	ctx := context.Background()

	parent := newParent(t, repo)

	// Create 3 subtasks with ascending priority — expect result in descending order.
	low := newPendingSubtask(parent.ID, 0)
	low.CreatedAt = time.Now()
	mid := newPendingSubtask(parent.ID, 1)
	mid.CreatedAt = time.Now().Add(time.Millisecond)
	hi := newPendingSubtask(parent.ID, 2)
	hi.CreatedAt = time.Now().Add(2 * time.Millisecond)
	mustCreateTask(t, repo, low)
	mustCreateTask(t, repo, mid)
	mustCreateTask(t, repo, hi)

	ready, err := repo.GetReadySubtasks(ctx, parent.ID)
	require.NoError(t, err)
	require.Len(t, ready, 3)
	// Expect: critical (2), high (1), normal (0)
	assert.Equal(t, hi.ID, ready[0].ID, "critical priority first")
	assert.Equal(t, mid.ID, ready[1].ID, "high second")
	assert.Equal(t, low.ID, ready[2].ID, "normal last")
}

func TestGORMTaskRepository_GetReadySubtasks_MissingBlockerTreatedAsOpen(t *testing.T) {
	repo := NewGORMTaskRepository(setupTaskTestDB(t))
	ctx := context.Background()

	parent := newParent(t, repo)

	// Dependent references a blocker that does not exist → must not be reported ready
	// (safer default: if we cannot prove terminal, treat as not ready).
	dep := newPendingSubtask(parent.ID, 0)
	dep.BlockedBy = []uuid.UUID{uuid.New()}
	mustCreateTask(t, repo, dep)

	ready, err := repo.GetReadySubtasks(ctx, parent.ID)
	require.NoError(t, err)
	assert.Empty(t, ready, "missing blocker must not be treated as terminal")
}
