package task

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// CancelTaskRepository defines the data access needed for task cancellation (consumer-side).
type CancelTaskRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.EngineTask, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status domain.EngineTaskStatus) error
	GetSubTasks(ctx context.Context, parentID uuid.UUID) ([]domain.EngineTask, error)
}

// TaskCanceller cancels a task and its sub-tasks recursively.
type TaskCanceller struct {
	repo CancelTaskRepository
}

// NewTaskCanceller creates a new TaskCanceller.
func NewTaskCanceller(repo CancelTaskRepository) *TaskCanceller {
	return &TaskCanceller{repo: repo}
}

// Cancel cancels a task and all its non-terminal sub-tasks.
// Terminal tasks are left unchanged (idempotent).
func (c *TaskCanceller) Cancel(ctx context.Context, taskID uuid.UUID) error {
	task, err := c.repo.GetByID(ctx, taskID)
	if err != nil {
		return fmt.Errorf("get task %s: %w", taskID, err)
	}

	if task.IsTerminal() {
		return nil
	}

	subs, err := c.repo.GetSubTasks(ctx, taskID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get sub-tasks for cancellation", "task_id", taskID, "error", err)
	}
	for _, sub := range subs {
		if cancelErr := c.Cancel(ctx, sub.ID); cancelErr != nil {
			slog.ErrorContext(ctx, "failed to cancel sub-task", "sub_task_id", sub.ID, "error", cancelErr)
		}
	}

	if err := c.repo.UpdateStatus(ctx, taskID, domain.EngineTaskStatusCancelled); err != nil {
		return fmt.Errorf("cancel task %s: %w", taskID, err)
	}

	slog.InfoContext(ctx, "task cancelled", "task_id", taskID)
	return nil
}
