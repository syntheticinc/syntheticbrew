package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// --- Test doubles ---

// mockTurnExecutor records calls to ExecuteTurn.
type mockTurnExecutor struct {
	mu          sync.Mutex
	calls       []turnCall
	returnFn    func(question string) error          // optional per-call logic (no ctx)
	returnFnCtx func(ctx context.Context, question string) error // optional per-call logic (with ctx)
}

type turnCall struct {
	SessionID  string
	ProjectKey string
	Question   string
}

func (m *mockTurnExecutor) ExecuteTurn(
	ctx context.Context,
	sessionID, projectKey, question string,
	chunkCb func(chunk string) error,
	eventCb func(event *domain.AgentEvent) error,
) error {
	m.mu.Lock()
	m.calls = append(m.calls, turnCall{sessionID, projectKey, question})
	fn := m.returnFn
	fnCtx := m.returnFnCtx
	m.mu.Unlock()

	if fnCtx != nil {
		return fnCtx(ctx, question)
	}
	if fn != nil {
		return fn(question)
	}
	return nil
}

func (m *mockTurnExecutor) getCalls() []turnCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]turnCall, len(m.calls))
	copy(cp, m.calls)
	return cp
}

// mockWorkChecker implements ActiveWorkChecker for tests.
type mockWorkChecker struct {
	mu               sync.Mutex
	hasActive        bool
	summary          string
	isWaitingForUser bool
}

func (c *mockWorkChecker) HasActiveWork(_ context.Context) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hasActive
}

func (c *mockWorkChecker) ActiveWorkSummary(_ context.Context) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.summary
}

func (c *mockWorkChecker) IsWaitingForUser(_ context.Context) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.isWaitingForUser
}

// --- Tests ---

func TestOrchestrator_SingleUserMessage(t *testing.T) {
	bus := NewSessionEventBus(8)
	executor := &mockTurnExecutor{}

	orch := New(Config{
		SessionID:    "s1",
		ProjectKey:   "p1",
		EventBus:     bus,
		TurnExecutor: executor,
	})

	ctx, cancel := context.WithCancel(context.Background())

	// Publish user message then close bus to end loop
	bus.Publish(OrchestratorEvent{Type: EventUserMessage, Content: "hello"})
	go func() {
		time.Sleep(50 * time.Millisecond)
		bus.Close()
	}()

	err := orch.Run(ctx)
	cancel()

	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	calls := executor.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].SessionID != "s1" {
		t.Errorf("sessionID = %q, want %q", calls[0].SessionID, "s1")
	}
	if calls[0].Question != "hello" {
		t.Errorf("question = %q, want %q", calls[0].Question, "hello")
	}
}

func TestOrchestrator_MultipleEvents(t *testing.T) {
	bus := NewSessionEventBus(8)
	executor := &mockTurnExecutor{}

	orch := New(Config{
		SessionID:    "s1",
		ProjectKey:   "p1",
		EventBus:     bus,
		TurnExecutor: executor,
	})

	ctx, cancel := context.WithCancel(context.Background())

	bus.Publish(OrchestratorEvent{Type: EventUserMessage, Content: "first"})
	bus.Publish(OrchestratorEvent{Type: EventUserMessage, Content: "second"})

	go func() {
		time.Sleep(50 * time.Millisecond)
		bus.Close()
	}()

	err := orch.Run(ctx)
	cancel()

	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	calls := executor.getCalls()
	if len(calls) < 1 {
		t.Fatal("expected at least 1 call")
	}
}

func TestOrchestrator_AgentCompletedEvent(t *testing.T) {
	bus := NewSessionEventBus(8)
	executor := &mockTurnExecutor{}

	orch := New(Config{
		SessionID:    "s1",
		ProjectKey:   "p1",
		EventBus:     bus,
		TurnExecutor: executor,
	})

	ctx, cancel := context.WithCancel(context.Background())

	bus.Publish(OrchestratorEvent{
		Type:      EventAgentCompleted,
		AgentID:   "agent-1",
		SubtaskID: "st-1",
		Content:   "done",
	})

	go func() {
		time.Sleep(50 * time.Millisecond)
		bus.Close()
	}()

	err := orch.Run(ctx)
	cancel()

	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	calls := executor.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if !strings.Contains(calls[0].Question, "agent-1") {
		t.Errorf("question should contain agent ID, got %q", calls[0].Question)
	}
	if !strings.Contains(calls[0].Question, "completed") {
		t.Errorf("question should contain 'completed', got %q", calls[0].Question)
	}
}

func TestOrchestrator_AgentFailedEvent(t *testing.T) {
	bus := NewSessionEventBus(8)
	executor := &mockTurnExecutor{}

	orch := New(Config{
		SessionID:    "s1",
		ProjectKey:   "p1",
		EventBus:     bus,
		TurnExecutor: executor,
	})

	ctx, cancel := context.WithCancel(context.Background())

	bus.Publish(OrchestratorEvent{
		Type:      EventAgentFailed,
		AgentID:   "agent-2",
		SubtaskID: "st-2",
		Content:   "compilation error",
	})

	go func() {
		time.Sleep(50 * time.Millisecond)
		bus.Close()
	}()

	err := orch.Run(ctx)
	cancel()

	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	calls := executor.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if !strings.Contains(calls[0].Question, "failed") {
		t.Errorf("question should contain 'failed', got %q", calls[0].Question)
	}
}

func TestOrchestrator_ContextCancelled(t *testing.T) {
	bus := NewSessionEventBus(8)
	executor := &mockTurnExecutor{}

	orch := New(Config{
		SessionID:    "s1",
		ProjectKey:   "p1",
		EventBus:     bus,
		TurnExecutor: executor,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := orch.Run(ctx)
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestOrchestrator_MaxEventsExceeded(t *testing.T) {
	bus := NewSessionEventBus(256)
	executor := &mockTurnExecutor{}

	orch := New(Config{
		SessionID:    "s1",
		ProjectKey:   "p1",
		EventBus:     bus,
		TurnExecutor: executor,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Publish more events than maxEventsPerSession (200)
	go func() {
		for i := 0; i < 210; i++ {
			bus.Publish(OrchestratorEvent{
				Type:    EventUserMessage,
				Content: fmt.Sprintf("msg %d", i),
			})
			time.Sleep(time.Millisecond) // let orchestrator process some
		}
	}()

	err := orch.Run(ctx)
	if err == nil {
		t.Fatal("expected error for max events exceeded")
	}
	if !strings.Contains(err.Error(), "max events") {
		t.Errorf("expected max events error, got: %v", err)
	}
}

func TestOrchestrator_ReminderStallDetection(t *testing.T) {
	bus := NewSessionEventBus(64)
	executor := &mockTurnExecutor{}

	orch := New(Config{
		SessionID:    "s1",
		ProjectKey:   "p1",
		EventBus:     bus,
		TurnExecutor: executor,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Send 11+ consecutive reminders to trigger stall detection
	go func() {
		for i := 0; i < 15; i++ {
			bus.Publish(OrchestratorEvent{
				Type:    EventWorkReminder,
				Content: "active work",
			})
			time.Sleep(time.Millisecond)
		}
	}()

	err := orch.Run(ctx)
	// Stall detection returns nil (graceful exit)
	if err != nil {
		t.Errorf("expected nil on stall, got: %v", err)
	}
}

func TestOrchestrator_ReminderResetByRealEvent(t *testing.T) {
	bus := NewSessionEventBus(64)
	executor := &mockTurnExecutor{}

	orch := New(Config{
		SessionID:    "s1",
		ProjectKey:   "p1",
		EventBus:     bus,
		TurnExecutor: executor,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Send some reminders, then a real event, then more reminders, then close
	go func() {
		for i := 0; i < 5; i++ {
			bus.Publish(OrchestratorEvent{Type: EventWorkReminder, Content: "work"})
			time.Sleep(time.Millisecond)
		}
		time.Sleep(20 * time.Millisecond)

		bus.Publish(OrchestratorEvent{Type: EventUserMessage, Content: "hello"})
		time.Sleep(20 * time.Millisecond)

		for i := 0; i < 5; i++ {
			bus.Publish(OrchestratorEvent{Type: EventWorkReminder, Content: "work"})
			time.Sleep(time.Millisecond)
		}
		time.Sleep(20 * time.Millisecond)

		bus.Close()
	}()

	err := orch.Run(ctx)
	if err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}

	// Should have processed events without stalling (counter reset by real event)
	calls := executor.getCalls()
	if len(calls) == 0 {
		t.Fatal("expected at least 1 executor call")
	}
}

func TestOrchestrator_ReminderScheduled(t *testing.T) {
	bus := NewSessionEventBus(8)
	executor := &mockTurnExecutor{}
	checker := &mockWorkChecker{hasActive: true, summary: "task-1 running"}

	orch := New(Config{
		SessionID:        "s1",
		ProjectKey:       "p1",
		EventBus:         bus,
		TurnExecutor:     executor,
		WorkChecker:      checker,
		ReminderInterval: 50 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Send one event, wait for reminder to be scheduled, then close
	bus.Publish(OrchestratorEvent{Type: EventUserMessage, Content: "hello"})

	go func() {
		// Wait for reminder to fire
		time.Sleep(150 * time.Millisecond)
		bus.Close()
	}()

	err := orch.Run(ctx)
	if err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}

	calls := executor.getCalls()
	// At least 2 calls: one for user message, one for reminder
	if len(calls) < 2 {
		t.Errorf("expected at least 2 calls (user msg + reminder), got %d", len(calls))
	}
}

func TestOrchestrator_NoReminderWithoutWorkChecker(t *testing.T) {
	bus := NewSessionEventBus(8)
	executor := &mockTurnExecutor{}

	orch := New(Config{
		SessionID:        "s1",
		ProjectKey:       "p1",
		EventBus:         bus,
		TurnExecutor:     executor,
		WorkChecker:      nil, // No work checker
		ReminderInterval: 10 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus.Publish(OrchestratorEvent{Type: EventUserMessage, Content: "hello"})

	go func() {
		time.Sleep(100 * time.Millisecond)
		bus.Close()
	}()

	err := orch.Run(ctx)
	if err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}

	// Only 1 call — no reminder without work checker
	calls := executor.getCalls()
	if len(calls) != 1 {
		t.Errorf("expected 1 call, got %d", len(calls))
	}
}

func TestOrchestrator_NoReminderWhenNoActiveWork(t *testing.T) {
	bus := NewSessionEventBus(8)
	executor := &mockTurnExecutor{}
	checker := &mockWorkChecker{hasActive: false, summary: "none"}

	orch := New(Config{
		SessionID:        "s1",
		ProjectKey:       "p1",
		EventBus:         bus,
		TurnExecutor:     executor,
		WorkChecker:      checker,
		ReminderInterval: 10 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus.Publish(OrchestratorEvent{Type: EventUserMessage, Content: "hello"})

	go func() {
		time.Sleep(100 * time.Millisecond)
		bus.Close()
	}()

	err := orch.Run(ctx)
	if err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}

	calls := executor.getCalls()
	if len(calls) != 1 {
		t.Errorf("expected 1 call (no reminder), got %d", len(calls))
	}
}

func TestOrchestrator_DefaultReminderInterval(t *testing.T) {
	orch := New(Config{
		ReminderInterval: 0, // Should default to 30s
	})
	if orch.cfg.ReminderInterval != defaultReminderInterval {
		t.Errorf("expected %v, got %v", defaultReminderInterval, orch.cfg.ReminderInterval)
	}
}

func TestOrchestrator_EventsToMessage_Single(t *testing.T) {
	orch := &Orchestrator{}

	msg := orch.eventsToMessage([]OrchestratorEvent{
		{Type: EventUserMessage, Content: "hello"},
	})
	if msg != "hello" {
		t.Errorf("expected 'hello', got %q", msg)
	}
}

func TestOrchestrator_EventsToMessage_Batch(t *testing.T) {
	orch := &Orchestrator{}

	msg := orch.eventsToMessage([]OrchestratorEvent{
		{Type: EventAgentCompleted, AgentID: "a1", SubtaskID: "s1", Content: "done"},
		{Type: EventUserMessage, Content: "next"},
	})

	if !strings.HasPrefix(msg, "[SYSTEM] Multiple events:") {
		t.Errorf("expected batch prefix, got %q", msg)
	}
	if !strings.Contains(msg, "a1") {
		t.Errorf("should contain agent ID, got %q", msg)
	}
	if !strings.Contains(msg, "next") {
		t.Errorf("should contain user message, got %q", msg)
	}
}

func TestOrchestrator_SingleEventToMessage_AllTypes(t *testing.T) {
	orch := &Orchestrator{}

	tests := []struct {
		event    OrchestratorEvent
		contains string
	}{
		{
			event:    OrchestratorEvent{Type: EventUserMessage, Content: "question"},
			contains: "question",
		},
		{
			event:    OrchestratorEvent{Type: EventAgentCompleted, AgentID: "a1", SubtaskID: "s1", Content: "result"},
			contains: "completed",
		},
		{
			event:    OrchestratorEvent{Type: EventAgentFailed, AgentID: "a2", SubtaskID: "s2", Content: "error"},
			contains: "failed",
		},
		{
			event:    OrchestratorEvent{Type: EventWorkReminder, Content: "check tasks"},
			contains: "Reminder",
		},
		{
			event:    OrchestratorEvent{Type: "unknown_type", Content: "data"},
			contains: "Unknown event",
		},
	}

	for _, tt := range tests {
		msg := orch.singleEventToMessage(tt.event)
		if !strings.Contains(msg, tt.contains) {
			t.Errorf("event %v: expected %q in message, got %q", tt.event.Type, tt.contains, msg)
		}
	}
}

func TestOrchestrator_TrackReminders_AllReminders(t *testing.T) {
	orch := &Orchestrator{}

	events := []OrchestratorEvent{
		{Type: EventWorkReminder},
		{Type: EventWorkReminder},
	}

	stalled := orch.trackReminders(events)
	if stalled {
		t.Error("should not be stalled yet (only 2 reminders)")
	}
	if orch.consecutiveReminders != 2 {
		t.Errorf("consecutive = %d, want 2", orch.consecutiveReminders)
	}
}

func TestOrchestrator_TrackReminders_MixedEvents(t *testing.T) {
	orch := &Orchestrator{consecutiveReminders: 5}

	events := []OrchestratorEvent{
		{Type: EventWorkReminder},
		{Type: EventUserMessage, Content: "hi"},
	}

	stalled := orch.trackReminders(events)
	if stalled {
		t.Error("should not be stalled with mixed events")
	}
	if orch.consecutiveReminders != 0 {
		t.Errorf("consecutive should reset to 0, got %d", orch.consecutiveReminders)
	}
}

func TestOrchestrator_TurnExecutorError(t *testing.T) {
	bus := NewSessionEventBus(8)
	executor := &mockTurnExecutor{
		returnFn: func(q string) error {
			return fmt.Errorf("LLM error")
		},
	}

	orch := New(Config{
		SessionID:    "s1",
		ProjectKey:   "p1",
		EventBus:     bus,
		TurnExecutor: executor,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus.Publish(OrchestratorEvent{Type: EventUserMessage, Content: "hello"})

	go func() {
		time.Sleep(50 * time.Millisecond)
		bus.Close()
	}()

	// Should not crash — error is logged, loop continues
	err := orch.Run(ctx)
	if err != nil {
		t.Fatalf("expected nil (bus closed), got: %v", err)
	}
}

func TestOrchestrator_WaitingForUser_SkipsReminders(t *testing.T) {
	bus := NewSessionEventBus(8)
	executor := &mockTurnExecutor{}
	checker := &mockWorkChecker{hasActive: true, summary: "task-1 running", isWaitingForUser: true}

	orch := New(Config{
		SessionID:        "s1",
		ProjectKey:       "p1",
		EventBus:         bus,
		TurnExecutor:     executor,
		WorkChecker:      checker,
		ReminderInterval: 50 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus.Publish(OrchestratorEvent{Type: EventUserMessage, Content: "hello"})

	go func() {
		time.Sleep(200 * time.Millisecond)
		bus.Close()
	}()

	err := orch.Run(ctx)
	if err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}

	calls := executor.getCalls()
	// Only 1 call — user message, NO reminders despite hasActive=true
	if len(calls) != 1 {
		t.Errorf("expected 1 call (no reminders while waiting), got %d", len(calls))
	}
}

func TestOrchestrator_EventUserResponded_TriggersTurn(t *testing.T) {
	bus := NewSessionEventBus(8)
	executor := &mockTurnExecutor{}

	orch := New(Config{
		SessionID:    "s1",
		ProjectKey:   "p1",
		EventBus:     bus,
		TurnExecutor: executor,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus.Publish(OrchestratorEvent{
		Type:    EventUserResponded,
		Content: "approved",
	})

	go func() {
		time.Sleep(50 * time.Millisecond)
		bus.Close()
	}()

	err := orch.Run(ctx)
	if err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}

	calls := executor.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if !strings.Contains(calls[0].Question, "User responded") {
		t.Errorf("expected 'User responded' in question, got: %s", calls[0].Question)
	}
}

func TestOrchestrator_EventUserResponded_Message(t *testing.T) {
	orch := &Orchestrator{}
	msg := orch.singleEventToMessage(OrchestratorEvent{
		Type:    EventUserResponded,
		Content: "yes please",
	})
	if !strings.Contains(msg, "User responded") {
		t.Errorf("expected 'User responded' in message, got %q", msg)
	}
	if !strings.Contains(msg, "yes please") {
		t.Errorf("expected content in message, got %q", msg)
	}
}

func TestOrchestrator_InterruptCancelsTurn(t *testing.T) {
	bus := NewSessionEventBus(8)

	callCount := 0
	var callCountMu sync.Mutex
	turnStarted := make(chan struct{}, 2)

	executor := &mockTurnExecutor{
		returnFnCtx: func(ctx context.Context, question string) error {
			callCountMu.Lock()
			callCount++
			n := callCount
			callCountMu.Unlock()

			select {
			case turnStarted <- struct{}{}:
			default:
			}

			if n == 1 {
				// First call: block until context cancelled
				<-ctx.Done()
				return ctx.Err()
			}
			// Second call: return immediately
			return nil
		},
	}

	orch := New(Config{
		SessionID:    "s1",
		ProjectKey:   "p1",
		EventBus:     bus,
		TurnExecutor: executor,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Publish initial message to start a turn
	bus.Publish(OrchestratorEvent{Type: EventUserMessage, Content: "start long task"})

	orchDone := make(chan error, 1)
	go func() {
		orchDone <- orch.Run(ctx)
	}()

	// Wait for the turn to start
	select {
	case <-turnStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for turn to start")
	}

	// Now simulate user interrupt — this should cancel the running turn
	bus.PublishInterrupt(OrchestratorEvent{
		Type:    EventUserMessage,
		Content: "new question",
	})

	// Wait for second turn to start (proves interrupt was processed)
	select {
	case <-turnStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for second turn after interrupt")
	}

	// Close bus to end orchestrator
	time.Sleep(50 * time.Millisecond)
	bus.Close()

	select {
	case err := <-orchDone:
		if err != nil {
			t.Fatalf("expected nil (bus closed), got: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("orchestrator did not exit after interrupt + close")
	}

	// Should have had 2 calls: original + interrupted message
	calls := executor.getCalls()
	if len(calls) < 2 {
		t.Errorf("expected at least 2 calls (original + interrupt), got %d", len(calls))
	}
	if len(calls) >= 2 && calls[1].Question != "new question" {
		t.Errorf("second call question = %q, want %q", calls[1].Question, "new question")
	}
}

func TestOrchestrator_InterruptMidTurn_ContextCancelled(t *testing.T) {
	bus := NewSessionEventBus(8)

	// Track whether the turn's context was cancelled
	var turnCtxCancelled bool
	var turnCtxMu sync.Mutex

	turnStarted := make(chan struct{}, 1)

	executor := &mockTurnExecutor{}
	// Override ExecuteTurn to capture context cancellation
	origExec := executor
	blockingExecutor := &contextTrackingExecutor{
		inner:            origExec,
		turnStarted:      turnStarted,
		ctxCancelledFlag: &turnCtxCancelled,
		ctxCancelledMu:   &turnCtxMu,
	}

	orch := New(Config{
		SessionID:    "s1",
		ProjectKey:   "p1",
		EventBus:     bus,
		TurnExecutor: blockingExecutor,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bus.Publish(OrchestratorEvent{Type: EventUserMessage, Content: "hello"})

	orchDone := make(chan error, 1)
	go func() {
		orchDone <- orch.Run(ctx)
	}()

	// Wait for turn to start
	select {
	case <-turnStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for turn to start")
	}

	// Send interrupt
	bus.PublishInterrupt(OrchestratorEvent{Type: EventUserMessage, Content: "interrupt"})

	// Wait for processing, then close
	time.Sleep(200 * time.Millisecond)
	bus.Close()

	<-orchDone

	turnCtxMu.Lock()
	cancelled := turnCtxCancelled
	turnCtxMu.Unlock()

	if !cancelled {
		t.Error("turn context should have been cancelled by interrupt")
	}
}

// contextTrackingExecutor wraps a TurnExecutor and tracks context cancellation.
type contextTrackingExecutor struct {
	inner            *mockTurnExecutor
	turnStarted      chan struct{}
	ctxCancelledFlag *bool
	ctxCancelledMu   *sync.Mutex
}

func (e *contextTrackingExecutor) ExecuteTurn(
	ctx context.Context,
	sessionID, projectKey, question string,
	chunkCb func(chunk string) error,
	eventCb func(event *domain.AgentEvent) error,
) error {
	e.inner.mu.Lock()
	e.inner.calls = append(e.inner.calls, turnCall{sessionID, projectKey, question})
	e.inner.mu.Unlock()

	// Signal that turn started
	select {
	case e.turnStarted <- struct{}{}:
	default:
	}

	// Wait for context cancellation
	<-ctx.Done()

	e.ctxCancelledMu.Lock()
	*e.ctxCancelledFlag = true
	e.ctxCancelledMu.Unlock()

	return ctx.Err()
}

func TestOrchestrator_InterruptProcessesNewMessage(t *testing.T) {
	bus := NewSessionEventBus(8)

	callCount := 0
	var callCountMu sync.Mutex
	turnStarted := make(chan struct{}, 2)

	executor := &mockTurnExecutor{
		returnFnCtx: func(ctx context.Context, question string) error {
			callCountMu.Lock()
			callCount++
			n := callCount
			callCountMu.Unlock()

			select {
			case turnStarted <- struct{}{}:
			default:
			}

			if n == 1 {
				// First call: block until context cancelled
				<-ctx.Done()
				return ctx.Err()
			}
			// Second call: return immediately
			return nil
		},
	}

	orch := New(Config{
		SessionID:    "s1",
		ProjectKey:   "p1",
		EventBus:     bus,
		TurnExecutor: executor,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start first turn
	bus.Publish(OrchestratorEvent{Type: EventUserMessage, Content: "first"})

	orchDone := make(chan error, 1)
	go func() {
		orchDone <- orch.Run(ctx)
	}()

	// Wait for first turn to start
	select {
	case <-turnStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first turn")
	}

	// Send interrupt
	bus.PublishInterrupt(OrchestratorEvent{Type: EventUserMessage, Content: "second"})

	// Wait for second turn to start
	select {
	case <-turnStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for second turn after interrupt")
	}

	// Close bus to end orchestrator
	time.Sleep(50 * time.Millisecond)
	bus.Close()

	select {
	case err := <-orchDone:
		if err != nil {
			t.Fatalf("expected nil, got: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("orchestrator did not exit")
	}

	calls := executor.getCalls()
	if len(calls) < 2 {
		t.Fatalf("expected at least 2 calls, got %d", len(calls))
	}
	if calls[0].Question != "first" {
		t.Errorf("first call = %q, want %q", calls[0].Question, "first")
	}
	if calls[1].Question != "second" {
		t.Errorf("second call = %q, want %q", calls[1].Question, "second")
	}
}
