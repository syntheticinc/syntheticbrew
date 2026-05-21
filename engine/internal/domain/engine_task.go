package domain

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// EngineTaskStatus represents the lifecycle stage of an EngineTask.
type EngineTaskStatus string

const (
	EngineTaskStatusDraft      EngineTaskStatus = "draft"       // Waiting for user approval
	EngineTaskStatusApproved   EngineTaskStatus = "approved"    // Approved, ready for execution
	EngineTaskStatusPending    EngineTaskStatus = "pending"     // Auto-approved, waiting in queue
	EngineTaskStatusInProgress EngineTaskStatus = "in_progress"
	EngineTaskStatusCompleted  EngineTaskStatus = "completed"
	EngineTaskStatusFailed     EngineTaskStatus = "failed"
	EngineTaskStatusNeedsInput EngineTaskStatus = "needs_input"
	EngineTaskStatusCancelled  EngineTaskStatus = "cancelled"
)

// TaskMode determines how an EngineTask executes.
type TaskMode string

const (
	TaskModeInteractive TaskMode = "interactive"
	TaskModeBackground  TaskMode = "background"
)

// EngineTask is the universal unit of work in SyntheticBrew Engine.
// Created by agents, cron triggers, webhooks, API, or dashboard.
// Subtasks are EngineTask with ParentTaskID set (no separate entity).
//
// Q.5: dropped agent_name, source, source_id, assigned_agent_id, depth
// from DB. These are derived at runtime:
//   - agent info: derived from session's schema
//   - depth: computed from parent_task_id chain
//
// ID types:
//   - ID, ParentTaskID, BlockedBy — uuid.UUID (DB-generated UUIDs)
//   - SessionID — string (opaque identifier; stored as-is across subsystems)
type EngineTask struct {
	ID                 uuid.UUID
	TenantID           string
	Title              string
	Description        string
	AcceptanceCriteria []string
	SessionID          string
	ParentTaskID       *uuid.UUID
	Status             EngineTaskStatus
	Mode               TaskMode
	Priority           int // 0 = normal, 1 = high, 2 = critical
	BlockedBy          []uuid.UUID // Task IDs that block this task
	Result             string
	Error              string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	ApprovedAt         *time.Time
	StartedAt          *time.Time
	CompletedAt        *time.Time
}

// IsTopLevel returns true if the task has no parent.
func (t *EngineTask) IsTopLevel() bool {
	return t.ParentTaskID == nil
}

// IsTerminal returns true if the task is in a terminal state.
func (t *EngineTask) IsTerminal() bool {
	return t.Status == EngineTaskStatusCompleted ||
		t.Status == EngineTaskStatusFailed ||
		t.Status == EngineTaskStatusCancelled
}

// engineTaskValidTransitions defines the state machine for EngineTask status.
// NeedsInput can transition to Failed so that autonomous task paths (cron,
// webhook, API) can fail a task that asked for user input without a human in
// the loop instead of leaving it stuck forever.
var engineTaskValidTransitions = map[EngineTaskStatus][]EngineTaskStatus{
	EngineTaskStatusDraft:      {EngineTaskStatusApproved, EngineTaskStatusCancelled},
	EngineTaskStatusApproved:   {EngineTaskStatusInProgress, EngineTaskStatusCancelled},
	EngineTaskStatusPending:    {EngineTaskStatusInProgress, EngineTaskStatusCancelled},
	EngineTaskStatusInProgress: {EngineTaskStatusCompleted, EngineTaskStatusFailed, EngineTaskStatusNeedsInput, EngineTaskStatusCancelled},
	EngineTaskStatusNeedsInput: {EngineTaskStatusInProgress, EngineTaskStatusFailed, EngineTaskStatusCancelled},
	EngineTaskStatusCompleted:  {},
	EngineTaskStatusFailed:     {},
	EngineTaskStatusCancelled:  {},
}

// CanTransitionTo checks whether a transition to the target status is allowed.
func (t *EngineTask) CanTransitionTo(target EngineTaskStatus) bool {
	allowed, ok := engineTaskValidTransitions[t.Status]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == target {
			return true
		}
	}
	return false
}

// Transition attempts to change the task status with validation and timestamp updates.
func (t *EngineTask) Transition(target EngineTaskStatus) error {
	if !t.CanTransitionTo(target) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, t.Status, target)
	}

	now := time.Now()
	t.Status = target
	t.UpdatedAt = now

	switch target {
	case EngineTaskStatusApproved:
		t.ApprovedAt = &now
	case EngineTaskStatusInProgress:
		if t.StartedAt == nil {
			t.StartedAt = &now
		}
	case EngineTaskStatusCompleted, EngineTaskStatusFailed, EngineTaskStatusCancelled:
		t.CompletedAt = &now
	}

	return nil
}

// Approve transitions from draft to approved.
func (t *EngineTask) Approve() error {
	return t.Transition(EngineTaskStatusApproved)
}

// Start transitions from approved/pending to in_progress.
func (t *EngineTask) Start() error {
	return t.Transition(EngineTaskStatusInProgress)
}

// Complete transitions from in_progress to completed.
func (t *EngineTask) Complete(result string) error {
	if err := t.Transition(EngineTaskStatusCompleted); err != nil {
		return err
	}
	t.Result = result
	return nil
}

// Fail transitions from in_progress to failed.
func (t *EngineTask) Fail(reason string) error {
	if err := t.Transition(EngineTaskStatusFailed); err != nil {
		return err
	}
	t.Error = reason
	return nil
}

// Cancel transitions any non-terminal status to cancelled.
func (t *EngineTask) Cancel() error {
	return t.Transition(EngineTaskStatusCancelled)
}

// SetPriority sets the task priority with validation.
func (t *EngineTask) SetPriority(priority int) error {
	if priority < 0 || priority > 2 {
		return fmt.Errorf("invalid priority: %d (must be 0-2)", priority)
	}
	t.Priority = priority
	t.UpdatedAt = time.Now()
	return nil
}

// HasBlockers returns true if this task declares any blockers.
// NOTE: this does NOT check whether the blocker tasks are actually non-terminal —
// readiness resolution lives in GORMTaskRepository.GetReadySubtasks, where the
// blocker statuses are joined from the DB. Use this only to detect declarations.
func (t *EngineTask) HasBlockers() bool {
	return len(t.BlockedBy) > 0
}

// Validate validates the EngineTask.
func (t *EngineTask) Validate() error {
	if t.Title == "" {
		return fmt.Errorf("title is required")
	}
	if t.Priority < 0 || t.Priority > 2 {
		return fmt.Errorf("invalid priority: %d (must be 0-2)", t.Priority)
	}
	return nil
}

// Domain errors for EngineTask.
var (
	ErrInvalidTransition  = fmt.Errorf("invalid status transition")
	ErrEngineTaskNotFound = fmt.Errorf("engine task not found")
	ErrTaskTerminal       = fmt.Errorf("cannot modify terminal task")
	ErrMaxDepthExceeded   = fmt.Errorf("subtask depth exceeds maximum")
	ErrCyclicDependency   = fmt.Errorf("cyclic dependency detected")
)
