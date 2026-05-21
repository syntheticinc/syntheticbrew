package taskrunner

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "github.com/syntheticinc/syntheticbrew/api/proto/gen"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools"
)

// --- Mock session registry + processor ---

type mockRegistry struct {
	mu                sync.Mutex
	createdSession    string
	createdAgentName  string
	enqueuedMessages  []string
	subscribeCh       chan *pb.SessionEvent
	removedSessionIDs []string
}

func newMockRegistry() *mockRegistry {
	return &mockRegistry{
		subscribeCh: make(chan *pb.SessionEvent, 8),
	}
}

func (m *mockRegistry) CreateSession(sessionID, _, _, _, _, agentName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createdSession = sessionID
	m.createdAgentName = agentName
}

func (m *mockRegistry) Subscribe(_ string) (<-chan *pb.SessionEvent, func()) {
	return m.subscribeCh, func() {}
}

func (m *mockRegistry) EnqueueMessage(_ string, content string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enqueuedMessages = append(m.enqueuedMessages, content)
	return nil
}

func (m *mockRegistry) RemoveSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removedSessionIDs = append(m.removedSessionIDs, sessionID)
}

type mockProcessor struct{}

func (m *mockProcessor) StartProcessing(_ context.Context, _ string) {}
func (m *mockProcessor) StopProcessing(_ string)                      {}

// --- Tests for pure logic (no DB / no full stack) ---

func TestBuildInitialMessage_IncludesAllFields(t *testing.T) {
	task := &domain.EngineTask{
		Title:              "Analyse sensor readings",
		Description:        "Look for anomalies in the last 24h of data.",
		AcceptanceCriteria: []string{"Produce a report", "Flag >3σ deviations"},
	}
	msg := buildInitialMessage(task)

	assert.Contains(t, msg, "# Task: Analyse sensor readings")
	assert.Contains(t, msg, "Look for anomalies")
	assert.Contains(t, msg, "- Produce a report")
	assert.Contains(t, msg, "- Flag >3σ deviations")
	assert.Contains(t, msg, "Respond with the final result when done")
}

func TestBuildInitialMessage_DescriptionOptional(t *testing.T) {
	task := &domain.EngineTask{Title: "Quick check"}
	msg := buildInitialMessage(task)
	assert.True(t, strings.HasPrefix(msg, "# Task: Quick check\n"))
	assert.NotContains(t, msg, "Acceptance criteria:")
}

// --- End-to-end executor flow with mocks ---

// A thin fake task manager implementing the methods the executor calls on it.
// The real EngineTaskManagerAdapter has many more methods; we shadow just the ones
// exercised by Execute.
type fakeAdapter struct {
	*EngineTaskManagerAdapter
	task             *domain.EngineTask
	statusUpdates    []string
	completedWith    string
	failedWithReason string
	completeCalls    int
	failCalls        int
}

// Note: we don't actually call taskManager.GetTask / SetTaskStatus / CompleteTask / FailTask
// here because those need a real DB. Instead, the tests below validate the purely-functional
// parts of the executor (message building, event translation) that don't need the manager.

func TestWaitForCompletion_AnswerThenStopped(t *testing.T) {
	t.Parallel()
	reg := newMockRegistry()
	e := &TaskExecutor{sessionRegistry: reg, processor: &mockProcessor{}, timeout: 0}

	go func() {
		reg.subscribeCh <- &pb.SessionEvent{Type: pb.SessionEventType_SESSION_EVENT_ANSWER, Content: "hello world"}
		reg.subscribeCh <- &pb.SessionEvent{Type: pb.SessionEventType_SESSION_EVENT_PROCESSING_STOPPED}
	}()

	events, _ := reg.Subscribe("s1")
	result, err := e.waitForCompletion(context.Background(), events)
	require.NoError(t, err)
	assert.Equal(t, "hello world", result)
}

func TestWaitForCompletion_StoppedWithoutAnswer_Fails(t *testing.T) {
	t.Parallel()
	reg := newMockRegistry()
	e := &TaskExecutor{sessionRegistry: reg, processor: &mockProcessor{}}

	go func() {
		reg.subscribeCh <- &pb.SessionEvent{Type: pb.SessionEventType_SESSION_EVENT_PROCESSING_STOPPED}
	}()

	events, _ := reg.Subscribe("s1")
	_, err := e.waitForCompletion(context.Background(), events)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "without producing a final answer")
}

func TestWaitForCompletion_ErrorEvent_Fails(t *testing.T) {
	t.Parallel()
	reg := newMockRegistry()
	e := &TaskExecutor{sessionRegistry: reg, processor: &mockProcessor{}}

	go func() {
		reg.subscribeCh <- &pb.SessionEvent{Type: pb.SessionEventType_SESSION_EVENT_ERROR, Content: "model refused"}
	}()

	events, _ := reg.Subscribe("s1")
	_, err := e.waitForCompletion(context.Background(), events)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model refused")
}

func TestWaitForCompletion_ContextCancel_NoAnswer(t *testing.T) {
	t.Parallel()
	reg := newMockRegistry()
	e := &TaskExecutor{sessionRegistry: reg, processor: &mockProcessor{}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	events, _ := reg.Subscribe("s1")
	_, err := e.waitForCompletion(ctx, events)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cancelled")
}

func TestWaitForCompletion_ContextCancelAfterAnswer_ReturnsAnswer(t *testing.T) {
	// If the agent already produced the final answer but the timeout fires before
	// PROCESSING_STOPPED arrives, we still want to use the answer (don't waste the work).
	t.Parallel()
	reg := newMockRegistry()
	e := &TaskExecutor{sessionRegistry: reg, processor: &mockProcessor{}}

	ctx, cancel := context.WithCancel(context.Background())
	reg.subscribeCh <- &pb.SessionEvent{Type: pb.SessionEventType_SESSION_EVENT_ANSWER, Content: "partial-but-useful"}
	go cancel()

	events, _ := reg.Subscribe("s1")
	result, err := e.waitForCompletion(ctx, events)
	require.NoError(t, err)
	assert.Equal(t, "partial-but-useful", result)
}

func TestNewTaskExecutor_DefaultsTimeout(t *testing.T) {
	e := NewTaskExecutor(nil, newMockRegistry(), &mockProcessor{}, 0)
	assert.Equal(t, DefaultTaskTimeout, e.timeout)
}

// --- Integration guard: ensure the CreateEngineTaskParams schema hasn't drifted ---

func TestCreateEngineTaskParams_MirrorsTaskFields(t *testing.T) {
	// This is a smoke test to make sure the struct used by the manager covers
	// every field the executor assumes the stored task will carry.
	p := tools.CreateEngineTaskParams{
		Title:              "x",
		Description:        "y",
		AcceptanceCriteria: []string{"a"},
		SessionID:          "sess-1",
		Priority:           1,
		BlockedBy:          []uuid.UUID{uuid.New()},
		RequireApproval:    false,
	}
	// Compile-time check: all fields present.
	_ = p
}
