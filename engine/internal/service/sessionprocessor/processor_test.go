package sessionprocessor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pb "github.com/syntheticinc/syntheticbrew/api/proto/gen"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools"
	"github.com/syntheticinc/syntheticbrew/internal/service/orchestrator"
)

// --- mocks ---

type mockSessionRegistry struct {
	mu         sync.Mutex
	sessions   map[string]struct{ projectRoot, platform, projectKey, userID string }
	messageCh  map[string]chan string
	events     []*pb.SessionEvent
	turnCancel context.CancelFunc
}

func newMockRegistry(sessionID, projectRoot, platform, projectKey, userID string) *mockSessionRegistry {
	r := &mockSessionRegistry{
		sessions:  make(map[string]struct{ projectRoot, platform, projectKey, userID string }),
		messageCh: make(map[string]chan string),
	}
	r.sessions[sessionID] = struct{ projectRoot, platform, projectKey, userID string }{
		projectRoot: projectRoot,
		platform:    platform,
		projectKey:  projectKey,
		userID:      userID,
	}
	r.messageCh[sessionID] = make(chan string, 32)
	return r
}

func (r *mockSessionRegistry) GetSessionContext(sessionID string) (projectRoot, platform, projectKey, userID, agentName string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, exists := r.sessions[sessionID]
	if !exists {
		return "", "", "", "", "", false
	}
	return s.projectRoot, s.platform, s.projectKey, s.userID, "", true
}

func (r *mockSessionRegistry) MessageChannel(sessionID string) <-chan string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch, ok := r.messageCh[sessionID]
	if !ok {
		closed := make(chan string)
		close(closed)
		return closed
	}
	return ch
}

func (r *mockSessionRegistry) PublishEvent(sessionID string, event *pb.SessionEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *mockSessionRegistry) ResetCancel(_ string) {}

func (r *mockSessionRegistry) StoreTurnCancel(_ string, cancel context.CancelFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.turnCancel = cancel
}

func (r *mockSessionRegistry) HasSession(sessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, exists := r.sessions[sessionID]
	return exists
}

func (r *mockSessionRegistry) RegisterAskUser(_, _ string) <-chan string {
	return make(chan string, 1)
}

func (r *mockSessionRegistry) UnregisterAskUser(_, _ string) {}

func (r *mockSessionRegistry) getEvents() []*pb.SessionEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]*pb.SessionEvent, len(r.events))
	copy(cp, r.events)
	return cp
}

type mockTurnExecutor struct {
	mu            sync.Mutex
	executeCalled chan struct{}
	lastMessage   string
}

func (e *mockTurnExecutor) ExecuteTurn(_ context.Context, _, _, question string,
	_ func(chunk string) error,
	_ func(event *domain.AgentEvent) error,
) error {
	e.mu.Lock()
	e.lastMessage = question
	e.mu.Unlock()

	select {
	case e.executeCalled <- struct{}{}:
	default:
	}
	return nil
}

type mockTurnExecutorFactory struct {
	executor *mockTurnExecutor
}

func (f *mockTurnExecutorFactory) CreateForSession(_ context.Context, _ tools.ClientOperationsProxy, _, _, _, _, _, _ string) orchestrator.TurnExecutor {
	return f.executor
}

// --- tests ---

// TC-SP-01: StartProcessing: EnqueueMessage → processMessage called (via mock TurnExecutor).
func TestProcessMessage(t *testing.T) {
	registry := newMockRegistry("session-1", t.TempDir(), "linux", "key-1", "user-1")
	executor := &mockTurnExecutor{executeCalled: make(chan struct{}, 1)}
	factory := &mockTurnExecutorFactory{executor: executor}

	proc := New(registry, factory, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	proc.StartProcessing(ctx, "session-1")

	// Enqueue a message
	registry.messageCh["session-1"] <- "hello"

	// Wait for processing
	select {
	case <-executor.executeCalled:
		// OK
	case <-time.After(5 * time.Second):
		t.Fatal("processMessage was not called")
	}

	// Verify executor received the message
	executor.mu.Lock()
	assert.Equal(t, "hello", executor.lastMessage)
	executor.mu.Unlock()

	// Wait a moment for events to be published
	time.Sleep(50 * time.Millisecond)

	// Verify events were published (UserMessage + ProcessingStarted + ProcessingStopped)
	events := registry.getEvents()
	require.GreaterOrEqual(t, len(events), 3, "should have at least UserMessage + ProcessingStarted + ProcessingStopped events")

	// First event should be UserMessage (user's message recorded for backfill)
	assert.Equal(t, pb.SessionEventType_SESSION_EVENT_USER_MESSAGE, events[0].Type)
	assert.Equal(t, "hello", events[0].Content)

	// Second event should be ProcessingStarted
	assert.Equal(t, pb.SessionEventType_SESSION_EVENT_PROCESSING_STARTED, events[1].Type)

	// Last event should be ProcessingStopped
	assert.Equal(t, pb.SessionEventType_SESSION_EVENT_PROCESSING_STOPPED, events[len(events)-1].Type)
}

// TC-SP-02: StartProcessing idempotent: repeated call is no-op (verified via IsProcessing).
func TestStartProcessingIdempotent(t *testing.T) {
	registry := newMockRegistry("session-1", t.TempDir(), "linux", "key-1", "user-1")
	executor := &mockTurnExecutor{executeCalled: make(chan struct{}, 10)}
	factory := &mockTurnExecutorFactory{executor: executor}

	proc := New(registry, factory, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	proc.StartProcessing(ctx, "session-1")
	assert.True(t, proc.IsProcessing("session-1"))

	// Second call should be no-op
	proc.StartProcessing(ctx, "session-1")
	assert.True(t, proc.IsProcessing("session-1"))

	// Only one goroutine should be reading from the channel.
	// Send a message and verify it is processed exactly once.
	registry.messageCh["session-1"] <- "msg-1"

	select {
	case <-executor.executeCalled:
		// OK — first call
	case <-time.After(5 * time.Second):
		t.Fatal("message was not processed")
	}

	// No second call expected
	select {
	case <-executor.executeCalled:
		t.Fatal("message processed twice — idempotency broken")
	case <-time.After(200 * time.Millisecond):
		// Expected: no duplicate processing
	}
}

// TC-SP-03: StopProcessing: stops the goroutine.
func TestStopProcessing(t *testing.T) {
	registry := newMockRegistry("session-1", t.TempDir(), "linux", "key-1", "user-1")
	executor := &mockTurnExecutor{executeCalled: make(chan struct{}, 1)}
	factory := &mockTurnExecutorFactory{executor: executor}

	proc := New(registry, factory, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	proc.StartProcessing(ctx, "session-1")
	assert.True(t, proc.IsProcessing("session-1"))

	proc.StopProcessing("session-1")

	// Wait for the goroutine to exit
	time.Sleep(100 * time.Millisecond)
	assert.False(t, proc.IsProcessing("session-1"))

	// Messages sent after stop should NOT be processed
	registry.messageCh["session-1"] <- "should-not-process"

	select {
	case <-executor.executeCalled:
		t.Fatal("message processed after StopProcessing")
	case <-time.After(200 * time.Millisecond):
		// Expected: no processing
	}
}

// TC-SP-04: StopProcessing on non-existent session is no-op.
func TestStopProcessing_NonExistent(t *testing.T) {
	registry := newMockRegistry("session-1", t.TempDir(), "linux", "key-1", "user-1")
	factory := &mockTurnExecutorFactory{}

	proc := New(registry, factory, nil, nil)

	// Should not panic
	proc.StopProcessing("nonexistent")
	assert.False(t, proc.IsProcessing("nonexistent"))
}

// TC-SP-05: processMessage with missing session context logs error and does not invoke executor.
func TestProcessMessage_MissingSession(t *testing.T) {
	registry := newMockRegistry("session-1", t.TempDir(), "linux", "key-1", "user-1")
	executor := &mockTurnExecutor{executeCalled: make(chan struct{}, 1)}
	factory := &mockTurnExecutorFactory{executor: executor}

	proc := New(registry, factory, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a separate channel for a session that has no context
	registry.mu.Lock()
	registry.messageCh["no-context"] = make(chan string, 32)
	registry.mu.Unlock()

	proc.StartProcessing(ctx, "no-context")

	// Send message to session without context
	registry.messageCh["no-context"] <- "hello"

	// Executor should NOT be called
	select {
	case <-executor.executeCalled:
		t.Fatal("executor was called for session without context")
	case <-time.After(200 * time.Millisecond):
		// Expected: no processing (context not found)
	}
}
