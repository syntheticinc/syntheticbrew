package task

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// TaskListerRich retrieves tasks and allows querying subtasks (used by rich reminder).
type TaskListerRich interface {
	ListTasks(ctx context.Context, sessionID string) ([]domain.EngineTask, error)
	ListSubtasks(ctx context.Context, parentID string) ([]domain.EngineTask, error)
}

// TaskReminderContextProvider provides rich context reminders for the ReAct agent.
// Implements the ContextReminderProvider interface (returns content, priority, ok).
// Injected at priority 90, ensuring the agent always sees its active work
// even after context compression (reminders are regenerated every turn from DB).
type TaskReminderContextProvider struct {
	lister richTaskLister
}

// richTaskLister abstracts the source of tasks + subtasks for the reminder.
// Implemented by the unified EngineTaskManagerAdapter.
type richTaskLister interface {
	ListTasks(ctx context.Context, sessionID string) ([]taskListEntry, error)
	ListSubtasksDetail(ctx context.Context, parentID string) ([]taskListEntry, error)
}

// taskListEntry is a compact view used by the reminder.
type taskListEntry struct {
	ID              string
	Title           string
	Status          string
	Priority        int
	AssignedAgentID string
	CreatedAt       time.Time
}

// NewTaskReminderProviderContext creates a context reminder backed by the task manager.
// Accepts the adapter to avoid importing infrastructure package from service/task.
func NewTaskReminderProviderContext(src ReminderSource) *TaskReminderContextProvider {
	return &TaskReminderContextProvider{lister: &reminderSourceAdapter{src: src}}
}

// ReminderSource is the minimal interface required by the task reminder.
// Implemented by taskrunner.EngineTaskManagerAdapter.
type ReminderSource interface {
	ListTasksDomain(ctx context.Context, sessionID string) ([]domain.EngineTask, error)
	ListSubtasksDomain(ctx context.Context, parentID uuid.UUID) ([]domain.EngineTask, error)
}

type reminderSourceAdapter struct {
	src ReminderSource
}

func (a *reminderSourceAdapter) ListTasks(ctx context.Context, sessionID string) ([]taskListEntry, error) {
	tasks, err := a.src.ListTasksDomain(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return domainToEntries(tasks), nil
}

func (a *reminderSourceAdapter) ListSubtasksDetail(ctx context.Context, parentID string) ([]taskListEntry, error) {
	parsed, err := uuid.Parse(parentID)
	if err != nil {
		return nil, fmt.Errorf("invalid parent id %q: %w", parentID, err)
	}
	subs, err := a.src.ListSubtasksDomain(ctx, parsed)
	if err != nil {
		return nil, err
	}
	return domainToEntries(subs), nil
}

func domainToEntries(tasks []domain.EngineTask) []taskListEntry {
	out := make([]taskListEntry, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, taskListEntry{
			ID:        t.ID.String(),
			Title:     t.Title,
			Status:    string(t.Status),
			Priority:  t.Priority,
			CreatedAt: t.CreatedAt,
		})
	}
	return out
}

// GetContextReminder returns a compact summary of active tasks and subtasks.
// Priority 90 — injected after plan context (80), before security (100).
// Reminders are regenerated every turn, so they survive context compression.
func (r *TaskReminderContextProvider) GetContextReminder(ctx context.Context, sessionID string) (string, int, bool) {
	tasks, err := r.lister.ListTasks(ctx, sessionID)
	if err != nil || len(tasks) == 0 {
		return "", 0, false
	}

	// Focus on top-level non-terminal tasks.
	var active []taskListEntry
	for _, t := range tasks {
		if t.Status == "completed" || t.Status == "failed" || t.Status == "cancelled" {
			continue
		}
		active = append(active, t)
	}
	if len(active) == 0 {
		return "", 0, false
	}

	var sb strings.Builder
	sb.WriteString("**ACTIVE WORK:**\n")

	for _, t := range active {
		subs, _ := r.lister.ListSubtasksDetail(ctx, t.ID)

		completed := 0
		total := len(subs)
		var runningAgents []string
		var readySubtasks []string
		for _, s := range subs {
			if s.Status == "completed" {
				completed++
			}
			if s.Status == "in_progress" && s.AssignedAgentID != "" {
				runningAgents = append(runningAgents, fmt.Sprintf("%s→%s", s.AssignedAgentID, s.Title))
			}
			if s.Status == "pending" || s.Status == "approved" {
				readySubtasks = append(readySubtasks, fmt.Sprintf("[%s] %s", s.ID, s.Title))
			}
		}

		sb.WriteString(fmt.Sprintf("Task [%s] %q (%s, priority=%d, %d/%d subtasks)\n",
			t.ID, t.Title, t.Status, t.Priority, completed, total))

		if t.Status == "draft" {
			age := time.Since(t.CreatedAt)
			if age > 30*time.Minute {
				sb.WriteString(fmt.Sprintf("  ⚠ STALE: pending approval for %s — consider cancelling\n",
					age.Truncate(time.Minute)))
			} else {
				sb.WriteString("  ⏳ Awaiting user approval\n")
			}
		}
		if len(runningAgents) > 0 {
			sb.WriteString(fmt.Sprintf("  Running: %s\n", strings.Join(runningAgents, ", ")))
		}
		if len(readySubtasks) > 0 {
			sb.WriteString(fmt.Sprintf("  Ready to spawn: %s\n", strings.Join(readySubtasks, ", ")))
			sb.WriteString("  → ACTION REQUIRED: Call spawn_agent(action=spawn, subtask_id=<ID>) for each ready subtask.\n")
		}
		if total == 0 && t.Status == "in_progress" {
			sb.WriteString("  ⚠ No subtasks yet. Next: manage_tasks(action=create_subtask, parent_task_id=...).\n")
		}
	}

	return sb.String(), 90, true
}
