package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// SupervisorState represents the current state of the Supervisor agent
type SupervisorState string

const (
	StateIdle           SupervisorState = "idle"
	StateThinking       SupervisorState = "thinking"
	StateWaitingForUser SupervisorState = "waiting_for_user"
)

// TurnExecutor executes a single REACT "turn" through the usecase layer.
// Each call loads history, runs one REACT loop, and persists the result.
type TurnExecutor interface {
	ExecuteTurn(ctx context.Context, sessionID, projectKey, question string,
		chunkCallback func(chunk string) error,
		eventCallback func(event *domain.AgentEvent) error) error
}

// ActiveWorkChecker checks for active (non-terminal) work items in a session.
type ActiveWorkChecker interface {
	HasActiveWork(ctx context.Context) bool
	ActiveWorkSummary(ctx context.Context) string
	IsWaitingForUser(ctx context.Context) bool
}

// Config holds configuration for creating an Orchestrator.
type Config struct {
	SessionID        string
	ProjectKey       string
	EventBus         *SessionEventBus
	TurnExecutor     TurnExecutor
	WorkChecker      ActiveWorkChecker
	ChunkCallback    func(chunk string) error
	EventCallback    func(event *domain.AgentEvent) error
	ReminderInterval time.Duration // default 30s
}

const (
	maxEventsPerSession     = 200
	maxConsecutiveReminders = 10
	defaultReminderInterval = 30 * time.Second
)

// Orchestrator drives the Supervisor agent via an event loop.
// It lives for the entire session and replaces the old outer continuation loop.
type Orchestrator struct {
	cfg                  Config
	reminderTimer        *time.Timer
	eventCount           int
	consecutiveReminders int
	state                SupervisorState
}

// New creates a new Orchestrator.
func New(cfg Config) *Orchestrator {
	if cfg.ReminderInterval <= 0 {
		cfg.ReminderInterval = defaultReminderInterval
	}
	return &Orchestrator{cfg: cfg}
}

// Run is the main event loop. It blocks until ctx is cancelled or the bus is closed.
func (o *Orchestrator) Run(ctx context.Context) error {
	defer o.cancelReminder()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case event, ok := <-o.cfg.EventBus.Events():
			if !ok {
				slog.InfoContext(ctx, "[Orchestrator] event bus closed, exiting")
				return nil
			}

			// Drain any pending events for batching
			events := o.drainPendingEvents(event)

			o.eventCount += len(events)
			if o.eventCount > maxEventsPerSession {
				slog.WarnContext(ctx, "[Orchestrator] max events exceeded, exiting",
					"count", o.eventCount)
				return fmt.Errorf("max events per session exceeded (%d)", maxEventsPerSession)
			}

			// Track consecutive reminders (stall detection)
			if o.trackReminders(events) {
				slog.WarnContext(ctx, "[Orchestrator] stalled — too many consecutive reminders, exiting",
					"count", o.consecutiveReminders)
				return nil
			}

			o.cancelReminder()
			// Clear stale interrupt signals before starting the turn
			o.cfg.EventBus.DrainInterrupts()
			o.runTurnWithInterrupt(ctx, events)
			o.scheduleReminderIfNeeded(ctx)
		}
	}
}

// trackReminders returns true if the orchestrator is stalled
// (too many consecutive reminder events without real work).
func (o *Orchestrator) trackReminders(events []OrchestratorEvent) bool {
	allReminders := true
	for _, e := range events {
		if e.Type != EventWorkReminder {
			allReminders = false
			break
		}
	}

	if allReminders {
		o.consecutiveReminders += len(events)
		return o.consecutiveReminders > maxConsecutiveReminders
	}

	o.consecutiveReminders = 0
	return false
}

// drainPendingEvents collects all pending events into a batch.
func (o *Orchestrator) drainPendingEvents(initial OrchestratorEvent) []OrchestratorEvent {
	events := []OrchestratorEvent{initial}
	for {
		select {
		case e := <-o.cfg.EventBus.Events():
			events = append(events, e)
		default:
			return events
		}
	}
}

// runTurnWithInterrupt runs ExecuteTurn in a goroutine and listens for
// interrupt signals from the EventBus. If a user sends a new message
// while a turn is in progress, the turn's context is cancelled.
func (o *Orchestrator) runTurnWithInterrupt(ctx context.Context, events []OrchestratorEvent) {
	// Check for user responded events — reset state
	for _, e := range events {
		if e.Type == EventUserResponded {
			o.state = StateIdle
			o.consecutiveReminders = 0
			slog.InfoContext(ctx, "[Orchestrator] user responded, state → idle")
			break
		}
	}

	question := o.eventsToMessage(events)

	slog.InfoContext(ctx, "[Orchestrator] executing turn",
		"event_count", len(events),
		"first_type", string(events[0].Type))

	// Create cancellable child context for this turn
	turnCtx, turnCancel := context.WithCancel(ctx)
	defer turnCancel()

	o.state = StateThinking

	// Run turn in goroutine
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- o.cfg.TurnExecutor.ExecuteTurn(
			turnCtx,
			o.cfg.SessionID,
			o.cfg.ProjectKey,
			question,
			o.cfg.ChunkCallback,
			o.cfg.EventCallback,
		)
	}()

	// Wait for: turn completes, parent ctx cancelled, or user interrupt
	select {
	case err := <-doneCh:
		// Turn completed normally — handle result
		o.handleTurnResult(ctx, err)

	case <-ctx.Done():
		// Parent context cancelled (disconnect/ESC)
		turnCancel()
		<-doneCh // wait for cleanup
		o.state = StateIdle

	case <-o.cfg.EventBus.Interrupts():
		// User sent message during turn — cancel current turn
		slog.InfoContext(ctx, "[Orchestrator] user interrupt received, cancelling current turn")
		turnCancel()
		err := <-doneCh // wait for cleanup
		if err != nil {
			slog.InfoContext(ctx, "[Orchestrator] interrupted turn finished", "error", err)
		}
		o.state = StateIdle
		// Return to Run() loop — it will read the interrupt message from EventBus
	}
}

// handleTurnResult processes the result of a completed REACT turn.
func (o *Orchestrator) handleTurnResult(ctx context.Context, err error) {
	if err != nil {
		// Context canceled = client-initiated cancel, NOT an error
		if ctx.Err() != nil {
			slog.InfoContext(ctx, "[Orchestrator] turn cancelled by client, not reporting error")
			o.state = StateIdle
			return
		}

		slog.ErrorContext(ctx, "[Orchestrator] REACT turn failed", "error", err)
		o.state = StateIdle
		// Send error event to client so it doesn't hang
		if o.cfg.EventCallback != nil {
			_ = o.cfg.EventCallback(&domain.AgentEvent{
				Type:       domain.EventTypeError,
				Content:    err.Error(),
				IsComplete: true,
				AgentID:    "supervisor",
				Error: &domain.AgentError{
					Code:    "TURN_FAILED",
					Message: err.Error(),
				},
			})
		}
		return
	}

	// After turn: check if we're now waiting for user
	if o.cfg.WorkChecker != nil && o.cfg.WorkChecker.IsWaitingForUser(ctx) {
		o.state = StateWaitingForUser
		slog.InfoContext(ctx, "[Orchestrator] state → waiting_for_user")
	} else {
		o.state = StateIdle
	}
}

// eventsToMessage formats events into a text message for the LLM.
func (o *Orchestrator) eventsToMessage(events []OrchestratorEvent) string {
	if len(events) == 1 {
		return o.singleEventToMessage(events[0])
	}

	// Multiple events — batch into one message
	var parts []string
	for _, e := range events {
		parts = append(parts, o.singleEventToMessage(e))
	}
	return "[SYSTEM] Multiple events:\n" + strings.Join(parts, "\n")
}

// singleEventToMessage formats a single event.
func (o *Orchestrator) singleEventToMessage(e OrchestratorEvent) string {
	switch e.Type {
	case EventUserMessage:
		return e.Content
	case EventAgentCompleted:
		return fmt.Sprintf("[SYSTEM] Code Agent %s completed subtask %s: %s",
			e.AgentID, e.SubtaskID, e.Content)
	case EventAgentFailed:
		return fmt.Sprintf("[SYSTEM] Code Agent %s failed subtask %s: %s",
			e.AgentID, e.SubtaskID, e.Content)
	case EventWorkReminder:
		return fmt.Sprintf("[SYSTEM] Reminder: you have active work. %s", e.Content)
	case EventUserResponded:
		return fmt.Sprintf("[SYSTEM] User responded to the pending question: %s", e.Content)
	default:
		return fmt.Sprintf("[SYSTEM] Unknown event: %s", e.Content)
	}
}

// scheduleReminderIfNeeded starts a reminder timer if there is active work.
func (o *Orchestrator) scheduleReminderIfNeeded(ctx context.Context) {
	if o.cfg.WorkChecker == nil {
		return
	}
	// Skip reminders while waiting for user — saves expensive LLM turns
	if o.state == StateWaitingForUser {
		slog.DebugContext(ctx, "[Orchestrator] skipping reminder — waiting for user")
		return
	}
	if !o.cfg.WorkChecker.HasActiveWork(ctx) {
		return
	}

	summary := o.cfg.WorkChecker.ActiveWorkSummary(ctx)
	o.reminderTimer = time.AfterFunc(o.cfg.ReminderInterval, func() {
		_ = o.cfg.EventBus.Publish(OrchestratorEvent{
			Type:    EventWorkReminder,
			Content: summary,
		})
	})
}

// cancelReminder stops the reminder timer if running.
func (o *Orchestrator) cancelReminder() {
	if o.reminderTimer != nil {
		o.reminderTimer.Stop()
		o.reminderTimer = nil
	}
}
