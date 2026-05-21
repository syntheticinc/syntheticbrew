package taskrunner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	pb "github.com/syntheticinc/syntheticbrew/api/proto/gen"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// DefaultTaskTimeout caps how long an autonomous cron/webhook task may run.
// Interactive tasks are driven by the human user and don't use this path.
const DefaultTaskTimeout = 30 * time.Minute

// sessionRegistryForExecutor is the consumer-side view of the session registry
// that TaskExecutor needs. Defined here so the executor doesn't depend on the
// full flowregistry.SessionRegistry surface area.
type sessionRegistryForExecutor interface {
	CreateSession(sessionID, projectKey, userID, projectRoot, platform, agentName string)
	Subscribe(sessionID string) (<-chan *pb.SessionEvent, func())
	EnqueueMessage(sessionID, content string) error
	RemoveSession(sessionID string)
}

// sessionProcessorForExecutor is the consumer-side interface for starting/stopping
// the background message-processing loop of a session.
type sessionProcessorForExecutor interface {
	StartProcessing(ctx context.Context, sessionID string)
	StopProcessing(sessionID string)
}

// TaskExecutor runs a single EngineTask autonomously: it takes a task id off the
// background worker queue, opens (or reuses) a session for the task's agent,
// streams the task as the first user message into the agent, waits for the final
// answer, and writes the result back onto the task.
//
// Schema scope is NOT held here — it's resolved per-turn by AgentSchemaResolver
// from agent_name, which the session already carries.
//
// This is what makes cron/webhook-created tasks actually do work — without it the
// row would just sit in the DB as "pending" forever.
type TaskExecutor struct {
	taskManager     *EngineTaskManagerAdapter
	sessionRegistry sessionRegistryForExecutor
	processor       sessionProcessorForExecutor
	timeout         time.Duration
}

// NewTaskExecutor constructs an executor. A zero timeout falls back to DefaultTaskTimeout.
func NewTaskExecutor(
	taskManager *EngineTaskManagerAdapter,
	sessionRegistry sessionRegistryForExecutor,
	processor sessionProcessorForExecutor,
	timeout time.Duration,
) *TaskExecutor {
	if timeout <= 0 {
		timeout = DefaultTaskTimeout
	}
	return &TaskExecutor{
		taskManager:     taskManager,
		sessionRegistry: sessionRegistry,
		processor:       processor,
		timeout:         timeout,
	}
}

// Execute implements task.TaskExecutor. Called by TaskWorker when a new task id is dequeued.
// The id is propagated as uuid.UUID through the full adapter API — no string parsing here.
func (e *TaskExecutor) Execute(ctx context.Context, taskID uuid.UUID) error {
	t, err := e.taskManager.GetTask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("get task %s: %w", taskID, err)
	}
	if t == nil {
		return fmt.Errorf("task %s not found", taskID)
	}
	if t.IsTerminal() {
		slog.DebugContext(ctx, "task already terminal, skipping", "task_id", taskID, "status", t.Status)
		return nil
	}

	// Transition to in_progress so the admin UI and context reminder see progress.
	// If already in_progress (e.g. task picked up twice), SetTaskStatus returns nil.
	if t.Status != domain.EngineTaskStatusInProgress {
		if setErr := e.taskManager.SetTaskStatus(ctx, taskID, string(domain.EngineTaskStatusInProgress), ""); setErr != nil {
			slog.WarnContext(ctx, "task executor: in_progress transition failed", "task_id", taskID, "error", setErr)
		}
	}

	sessionID := t.SessionID
	ownSession := false
	if sessionID == "" {
		sessionID = uuid.NewString()
		// Autonomous session: no user, no project, platform="cron". Agent name is
		// no longer stored on the task (Q.5) — pass empty; schema scope is resolved
		// per-tool-call from the session's schema downstream.
		e.sessionRegistry.CreateSession(sessionID, "", "", "", "cron", "")
		ownSession = true
		// Persist the session id onto the task so admin / Inspect UI can trace
		// which session produced the events for this cron-run. Best-effort — if
		// the DB write fails we still run the task, but the audit link is lost.
		if err := e.taskManager.AttachSession(ctx, taskID, sessionID); err != nil {
			slog.WarnContext(ctx, "task executor: could not persist session_id on task", "task_id", taskID, "error", err)
		}
	}

	// Subscribe BEFORE enqueuing the message — otherwise early events may be missed.
	events, unsubscribe := e.sessionRegistry.Subscribe(sessionID)
	defer unsubscribe()

	taskCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	e.processor.StartProcessing(taskCtx, sessionID)

	if err := e.sessionRegistry.EnqueueMessage(sessionID, buildInitialMessage(t)); err != nil {
		e.finalizeSession(sessionID, ownSession)
		return e.markFailed(ctx, taskID, fmt.Errorf("enqueue initial message: %w", err))
	}

	result, waitErr := e.waitForCompletion(taskCtx, events)
	e.finalizeSession(sessionID, ownSession)

	if waitErr != nil {
		return e.markFailed(ctx, taskID, waitErr)
	}

	// Autonomous tasks (cron/webhook/API/background) cannot interact with a human,
	// so a task that ended up in needs_input after the agent yielded is a dead end.
	// Re-check the current status and auto-fail with a clear reason so operators can
	// see in the admin UI why the cron run did not complete.
	if isAutonomous(t) {
		if cur, getErr := e.taskManager.GetTask(ctx, taskID); getErr == nil && cur != nil {
			if cur.Status == domain.EngineTaskStatusNeedsInput {
				return e.markFailed(ctx, taskID, fmt.Errorf("task requires user input but is running in autonomous mode (status=%s)", cur.Status))
			}
		}
	}

	if completeErr := e.taskManager.CompleteTask(ctx, taskID, result); completeErr != nil {
		slog.ErrorContext(ctx, "task executor: complete failed", "task_id", taskID, "error", completeErr)
		return completeErr
	}
	return nil
}

// isAutonomous returns true when a task cannot have a human in the loop.
// Q.5: Source field is dropped. Background mode is the only remaining signal.
func isAutonomous(t *domain.EngineTask) bool {
	if t == nil {
		return false
	}
	return t.Mode == domain.TaskModeBackground
}

// finalizeSession stops the processor and removes the session iff the executor
// created it (a supervisor-owned session must outlive the executor call).
func (e *TaskExecutor) finalizeSession(sessionID string, owned bool) {
	e.processor.StopProcessing(sessionID)
	if owned {
		e.sessionRegistry.RemoveSession(sessionID)
	}
}

// waitForCompletion blocks until the agent emits a final answer (with a follow-up
// PROCESSING_STOPPED) OR the context is cancelled (timeout / shutdown).
func (e *TaskExecutor) waitForCompletion(ctx context.Context, events <-chan *pb.SessionEvent) (string, error) {
	var finalAnswer string
	for {
		select {
		case <-ctx.Done():
			if finalAnswer != "" {
				// Timed out after the agent already answered — use the answer but flag the
				// premature end so operators can see it in the result.
				return finalAnswer, nil
			}
			return "", fmt.Errorf("task wait cancelled: %w", ctx.Err())
		case ev, ok := <-events:
			if !ok {
				if finalAnswer == "" {
					return "", errors.New("session channel closed before final answer")
				}
				return finalAnswer, nil
			}
			if ev == nil {
				continue
			}
			switch ev.Type {
			case pb.SessionEventType_SESSION_EVENT_ANSWER:
				finalAnswer = ev.Content
			case pb.SessionEventType_SESSION_EVENT_PROCESSING_STOPPED:
				if finalAnswer == "" {
					// Stopped without an answer — something went wrong (model refused, empty
					// output, tool loop). Treat as failure.
					return "", errors.New("agent stopped without producing a final answer")
				}
				return finalAnswer, nil
			case pb.SessionEventType_SESSION_EVENT_ERROR:
				msg := "agent error"
				if ev.ErrorDetail != nil && ev.ErrorDetail.String() != "" {
					msg = fmt.Sprintf("agent error: %s", ev.ErrorDetail.String())
				} else if ev.Content != "" {
					msg = fmt.Sprintf("agent error: %s", ev.Content)
				}
				return finalAnswer, errors.New(msg)
			}
		}
	}
}

// markFailed records a task failure with a bounded reason (the DB column is size-limited).
func (e *TaskExecutor) markFailed(ctx context.Context, taskID uuid.UUID, cause error) error {
	reason := cause.Error()
	if len(reason) > 4000 {
		reason = reason[:4000]
	}
	if err := e.taskManager.FailTask(ctx, taskID, reason); err != nil {
		slog.ErrorContext(ctx, "task executor: fail transition errored", "task_id", taskID, "error", err, "cause", cause)
	}
	return cause
}

// buildInitialMessage packages the task into the first user message for the agent.
// Keeps title / description / acceptance criteria in a stable, parseable shape so
// the agent sees exactly what the admin UI and context reminder show.
func buildInitialMessage(t *domain.EngineTask) string {
	var b strings.Builder
	b.WriteString("# Task: ")
	b.WriteString(t.Title)
	b.WriteString("\n\n")
	if t.Description != "" {
		b.WriteString(t.Description)
		b.WriteString("\n\n")
	}
	if len(t.AcceptanceCriteria) > 0 {
		b.WriteString("Acceptance criteria:\n")
		for _, ac := range t.AcceptanceCriteria {
			b.WriteString("- ")
			b.WriteString(ac)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("Complete this task now. Respond with the final result when done.")
	return b.String()
}
