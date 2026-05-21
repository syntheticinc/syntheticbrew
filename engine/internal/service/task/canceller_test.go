package task

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

type mockCancelRepo struct {
	tasks    map[uuid.UUID]*domain.EngineTask
	subTasks map[uuid.UUID][]domain.EngineTask // parentID -> sub-tasks
	statuses map[uuid.UUID]domain.EngineTaskStatus
}

func newMockCancelRepo() *mockCancelRepo {
	return &mockCancelRepo{
		tasks:    make(map[uuid.UUID]*domain.EngineTask),
		subTasks: make(map[uuid.UUID][]domain.EngineTask),
		statuses: make(map[uuid.UUID]domain.EngineTaskStatus),
	}
}

func (m *mockCancelRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.EngineTask, error) {
	task, ok := m.tasks[id]
	if !ok {
		return nil, fmt.Errorf("task %s not found", id)
	}
	// Return status from statuses map if it was updated.
	if s, updated := m.statuses[id]; updated {
		task.Status = s
	}
	return task, nil
}

func (m *mockCancelRepo) UpdateStatus(_ context.Context, id uuid.UUID, status domain.EngineTaskStatus) error {
	if _, ok := m.tasks[id]; !ok {
		return fmt.Errorf("task %s not found", id)
	}
	m.statuses[id] = status
	return nil
}

func (m *mockCancelRepo) GetSubTasks(_ context.Context, parentID uuid.UUID) ([]domain.EngineTask, error) {
	return m.subTasks[parentID], nil
}

func TestTaskCanceller_CancelTopLevel(t *testing.T) {
	id1 := uuid.New()
	repo := newMockCancelRepo()
	repo.tasks[id1] = &domain.EngineTask{ID: id1, Status: domain.EngineTaskStatusInProgress}

	c := NewTaskCanceller(repo)
	err := c.Cancel(context.Background(), id1)

	require.NoError(t, err)
	assert.Equal(t, domain.EngineTaskStatusCancelled, repo.statuses[id1])
}

func TestTaskCanceller_CancelWithSubTasks(t *testing.T) {
	id1, id2, id3 := uuid.New(), uuid.New(), uuid.New()
	repo := newMockCancelRepo()
	repo.tasks[id1] = &domain.EngineTask{ID: id1, Status: domain.EngineTaskStatusInProgress}
	repo.tasks[id2] = &domain.EngineTask{ID: id2, Status: domain.EngineTaskStatusPending}
	repo.tasks[id3] = &domain.EngineTask{ID: id3, Status: domain.EngineTaskStatusInProgress}
	repo.subTasks[id1] = []domain.EngineTask{
		{ID: id2, Status: domain.EngineTaskStatusPending},
		{ID: id3, Status: domain.EngineTaskStatusInProgress},
	}

	c := NewTaskCanceller(repo)
	err := c.Cancel(context.Background(), id1)

	require.NoError(t, err)
	assert.Equal(t, domain.EngineTaskStatusCancelled, repo.statuses[id1])
	assert.Equal(t, domain.EngineTaskStatusCancelled, repo.statuses[id2])
	assert.Equal(t, domain.EngineTaskStatusCancelled, repo.statuses[id3])
}

func TestTaskCanceller_CancelTerminalTask_Noop(t *testing.T) {
	id1 := uuid.New()
	repo := newMockCancelRepo()
	repo.tasks[id1] = &domain.EngineTask{ID: id1, Status: domain.EngineTaskStatusCompleted}

	c := NewTaskCanceller(repo)
	err := c.Cancel(context.Background(), id1)

	require.NoError(t, err)
	// No status update should have been made.
	_, updated := repo.statuses[id1]
	assert.False(t, updated)
}

func TestTaskCanceller_CancelDeepHierarchy(t *testing.T) {
	id1, id2, id3 := uuid.New(), uuid.New(), uuid.New()
	repo := newMockCancelRepo()
	repo.tasks[id1] = &domain.EngineTask{ID: id1, Status: domain.EngineTaskStatusInProgress}
	repo.tasks[id2] = &domain.EngineTask{ID: id2, Status: domain.EngineTaskStatusInProgress}
	repo.tasks[id3] = &domain.EngineTask{ID: id3, Status: domain.EngineTaskStatusPending}
	repo.subTasks[id1] = []domain.EngineTask{{ID: id2, Status: domain.EngineTaskStatusInProgress}}
	repo.subTasks[id2] = []domain.EngineTask{{ID: id3, Status: domain.EngineTaskStatusPending}}

	c := NewTaskCanceller(repo)
	err := c.Cancel(context.Background(), id1)

	require.NoError(t, err)
	assert.Equal(t, domain.EngineTaskStatusCancelled, repo.statuses[id1])
	assert.Equal(t, domain.EngineTaskStatusCancelled, repo.statuses[id2])
	assert.Equal(t, domain.EngineTaskStatusCancelled, repo.statuses[id3])
}

func TestTaskCanceller_CancelSkipsTerminalSubTasks(t *testing.T) {
	id1, id2, id3 := uuid.New(), uuid.New(), uuid.New()
	repo := newMockCancelRepo()
	repo.tasks[id1] = &domain.EngineTask{ID: id1, Status: domain.EngineTaskStatusInProgress}
	repo.tasks[id2] = &domain.EngineTask{ID: id2, Status: domain.EngineTaskStatusCompleted}
	repo.tasks[id3] = &domain.EngineTask{ID: id3, Status: domain.EngineTaskStatusPending}
	repo.subTasks[id1] = []domain.EngineTask{
		{ID: id2, Status: domain.EngineTaskStatusCompleted},
		{ID: id3, Status: domain.EngineTaskStatusPending},
	}

	c := NewTaskCanceller(repo)
	err := c.Cancel(context.Background(), id1)

	require.NoError(t, err)
	assert.Equal(t, domain.EngineTaskStatusCancelled, repo.statuses[id1])
	// Completed sub-task should not be touched.
	_, task2Updated := repo.statuses[id2]
	assert.False(t, task2Updated)
	// Pending sub-task should be cancelled.
	assert.Equal(t, domain.EngineTaskStatusCancelled, repo.statuses[id3])
}

func TestTaskCanceller_NotFound(t *testing.T) {
	repo := newMockCancelRepo()
	c := NewTaskCanceller(repo)

	missing := uuid.New()
	err := c.Cancel(context.Background(), missing)
	require.Error(t, err)
	assert.Contains(t, err.Error(), missing.String())
}
