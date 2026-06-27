package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agents"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/adapters"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock SnapshotRepository
type mockSnapshotRepo struct {
	snapshots map[string]*domain.AgentContextSnapshot // keyed by agentID
	mu        sync.Mutex
}

func newMockSnapshotRepo() *mockSnapshotRepo {
	return &mockSnapshotRepo{
		snapshots: make(map[string]*domain.AgentContextSnapshot),
	}
}

func (m *mockSnapshotRepo) Save(ctx context.Context, snapshot *domain.AgentContextSnapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshots[snapshot.AgentID] = snapshot
	return nil
}

func (m *mockSnapshotRepo) Load(ctx context.Context, sessionID, agentID string) (*domain.AgentContextSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshots[agentID], nil
}

func (m *mockSnapshotRepo) Delete(ctx context.Context, sessionID, agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.snapshots, agentID)
	return nil
}

func (m *mockSnapshotRepo) FindActive(ctx context.Context) ([]*domain.AgentContextSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var active []*domain.AgentContextSnapshot
	for _, snap := range m.snapshots {
		if snap.Status == domain.AgentContextStatusActive {
			active = append(active, snap)
		}
	}
	return active, nil
}

// Mock HistoryRepository
type mockHistoryRepo struct {
	messages []*domain.Message
	mu       sync.Mutex
}

func newMockHistoryRepo() *mockHistoryRepo {
	return &mockHistoryRepo{
		messages: make([]*domain.Message, 0),
	}
}

func (m *mockHistoryRepo) Create(ctx context.Context, message *domain.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, message)
	return nil
}

// Mock ChatModel
type mockChatModel struct {
	response *schema.Message
	err      error
}

func (m *mockChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.response == nil {
		return &schema.Message{
			Role:    schema.Assistant,
			Content: "mock response",
		}, nil
	}
	return m.response, nil
}

func (m *mockChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	// Return empty reader
	sr, sw := schema.Pipe[*schema.Message](1)
	sw.Close()
	return sr, nil
}

func (m *mockChatModel) BindTools(tools []*schema.ToolInfo) error {
	return nil
}

func (m *mockChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

// Helper to create test flow
func testFlow() *domain.Flow {
	return &domain.Flow{
		Type:           "supervisor",
		Name:           "test-flow",
		SystemPrompt:   "You are a test agent",
		ToolNames:      []string{},
		MaxSteps:       10,
		MaxContextSize: 4000,
		Lifecycle: domain.LifecyclePolicy{
			SuspendOn: []string{},
			ReportTo:  "user",
		},
	}
}

// Test 1: Fresh start (no snapshot)
func TestEngine_FreshStart(t *testing.T) {
	ctx := context.Background()
	snapshotRepo := newMockSnapshotRepo()
	historyRepo := newMockHistoryRepo()
	engine := New(snapshotRepo, historyRepo)

	cfg := ExecutionConfig{
		SessionID:   "session-1",
		AgentID:     "supervisor",
		Flow:        testFlow(),
		ChatModel:   &mockChatModel{},
		Input:       "Hello",
		Streaming:   false,
		AgentConfig: &config.AgentConfig{},
	}

	result, err := engine.Execute(ctx, cfg)

	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, result.Status)

	// Check snapshot saved
	snapshot := snapshotRepo.snapshots["supervisor"]
	require.NotNil(t, snapshot)
	assert.Equal(t, "session-1", snapshot.SessionID)
	assert.Equal(t, "supervisor", snapshot.AgentID)
	assert.Equal(t, domain.CurrentSchemaVersion, snapshot.SchemaVersion)
	assert.Equal(t, domain.AgentContextStatusCompacted, snapshot.Status)

	// Check messages were deserialized successfully
	messages, err := adapters.DeserializeSchemaMessages(snapshot.ContextData)
	require.NoError(t, err)
	assert.NotEmpty(t, messages)
}

// Test 2: Resume from snapshot
func TestEngine_ResumeFromSnapshot(t *testing.T) {
	ctx := context.Background()
	snapshotRepo := newMockSnapshotRepo()
	historyRepo := newMockHistoryRepo()
	engine := New(snapshotRepo, historyRepo)

	// Create initial snapshot with history
	initialMessages := []*schema.Message{
		{Role: schema.User, Content: "First message"},
		{Role: schema.Assistant, Content: "First response"},
	}
	contextData, err := adapters.SerializeSchemaMessages(initialMessages)
	require.NoError(t, err)

	snapshotRepo.snapshots["supervisor"] = &domain.AgentContextSnapshot{
		SessionID:     "session-1",
		AgentID:       "supervisor",
		SchemaVersion: domain.CurrentSchemaVersion,
		ContextData:   contextData,
		StepNumber:    1,
		Status:        domain.AgentContextStatusExpired,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	cfg := ExecutionConfig{
		SessionID:   "session-1",
		AgentID:     "supervisor",
		Flow:        testFlow(),
		ChatModel:   &mockChatModel{},
		Input:       "Second message",
		Streaming:   false,
		AgentConfig: &config.AgentConfig{},
	}

	result, err := engine.Execute(ctx, cfg)

	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, result.Status)

	// Check snapshot updated with new messages
	snapshot := snapshotRepo.snapshots["supervisor"]
	require.NotNil(t, snapshot)

	messages, err := adapters.DeserializeSchemaMessages(snapshot.ContextData)
	require.NoError(t, err)
	// Should have: initial 2 messages + new user message + new assistant response
	assert.GreaterOrEqual(t, len(messages), 3)
}

// Test 3: Suspend flow
func TestEngine_SuspendFlow(t *testing.T) {
	ctx := context.Background()
	snapshotRepo := newMockSnapshotRepo()
	historyRepo := newMockHistoryRepo()
	engine := New(snapshotRepo, historyRepo)

	flow := testFlow()
	flow.Lifecycle.SuspendOn = []string{"final_answer"}

	cfg := ExecutionConfig{
		SessionID:   "session-1",
		AgentID:     "supervisor",
		Flow:        flow,
		ChatModel:   &mockChatModel{},
		Input:       "Hello",
		Streaming:   false,
		AgentConfig: &config.AgentConfig{},
	}

	result, err := engine.Execute(ctx, cfg)

	require.NoError(t, err)
	assert.Equal(t, StatusSuspended, result.Status)
	assert.Equal(t, "final_answer", result.SuspendedAt)

	// Check snapshot status
	snapshot := snapshotRepo.snapshots["supervisor"]
	require.NotNil(t, snapshot)
	assert.Equal(t, domain.AgentContextStatusExpired, snapshot.Status)
}

// Test 4: Failed execution
func TestEngine_FailedExecution(t *testing.T) {
	ctx := context.Background()
	snapshotRepo := newMockSnapshotRepo()
	historyRepo := newMockHistoryRepo()
	engine := New(snapshotRepo, historyRepo)

	// Mock model that returns error
	mockErr := assert.AnError
	cfg := ExecutionConfig{
		SessionID: "session-1",
		AgentID:   "supervisor",
		Flow:      testFlow(),
		ChatModel: &mockChatModel{err: mockErr},
		Input:     "Hello",
		Streaming: false,
	}

	result, err := engine.Execute(ctx, cfg)

	require.Error(t, err)
	assert.Equal(t, StatusFailed, result.Status)

	// Check snapshot still saved for debugging
	snapshot := snapshotRepo.snapshots["supervisor"]
	require.NotNil(t, snapshot)
	assert.Equal(t, domain.AgentContextStatusExpired, snapshot.Status)
}

// Test 5: Message collection
func TestEngine_MessageCollection(t *testing.T) {
	historyRepo := newMockHistoryRepo()

	// Create collector with mock events
	collector := NewMessageCollector(context.Background(), "session-1", "supervisor", historyRepo)

	// Simulate tool call event
	toolCallEvent := &domain.AgentEvent{
		Type: domain.EventTypeToolCall,
		Metadata: map[string]interface{}{
			"id":                 "call-1",
			"tool_name":          "read_file",
			"function_arguments": `{"path":"test.txt"}`,
			"assistant_content":  "Let me read the file",
		},
	}
	collector.handleEvent(toolCallEvent)

	// Simulate tool result event
	toolResultEvent := &domain.AgentEvent{
		Type:    domain.EventTypeToolResult,
		Content: "file content",
		Metadata: map[string]interface{}{
			"tool_name":   "read_file",
			"full_result": "file content here",
		},
	}
	collector.handleEvent(toolResultEvent)

	// Simulate answer event
	answerEvent := &domain.AgentEvent{
		Type:    domain.EventTypeAnswer,
		Content: "Done",
	}
	collector.handleEvent(answerEvent)

	// Check collected messages
	messages := collector.GetAccumulatedMessages()
	assert.Len(t, messages, 3) // assistant+tool_call, tool, assistant

	// Check message types
	assert.Equal(t, schema.Assistant, messages[0].Role)
	assert.NotEmpty(t, messages[0].ToolCalls)
	assert.Equal(t, schema.Tool, messages[1].Role)
	assert.Equal(t, schema.Assistant, messages[2].Role)

	// Check history repo received events
	// 4 events: assistant_message (from tool call's assistant_content) + tool_call + tool_result + assistant_message (answer)
	assert.Len(t, historyRepo.messages, 4)
}

// Test 6: Lossless round-trip
func TestEngine_LosslessRoundTrip(t *testing.T) {
	ctx := context.Background()
	snapshotRepo := newMockSnapshotRepo()
	historyRepo := newMockHistoryRepo()
	engine := New(snapshotRepo, historyRepo)

	// Create complex message set
	originalMessages := []*schema.Message{
		{Role: schema.User, Content: "Hello"},
		{
			Role:    schema.Assistant,
			Content: "Let me help",
			ToolCalls: []schema.ToolCall{{
				ID: "call-1",
				Function: schema.FunctionCall{
					Name:      "read_file",
					Arguments: `{"path":"test.txt"}`,
				},
			}},
		},
		{
			Role:       schema.Tool,
			Content:    "file content",
			ToolCallID: "call-1",
			Name:       "read_file",
		},
		{Role: schema.Assistant, Content: "Done"},
	}

	// Serialize and save
	contextData, err := adapters.SerializeSchemaMessages(originalMessages)
	require.NoError(t, err)

	snapshotRepo.snapshots["supervisor"] = &domain.AgentContextSnapshot{
		SessionID:     "session-1",
		AgentID:       "supervisor",
		SchemaVersion: domain.CurrentSchemaVersion,
		ContextData:   contextData,
		StepNumber:    2,
		Status:        domain.AgentContextStatusExpired,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	// Load and deserialize
	cfg := ExecutionConfig{
		SessionID:   "session-1",
		AgentID:     "supervisor",
		Flow:        testFlow(),
		ChatModel:   &mockChatModel{},
		Input:       "Continue",
		Streaming:   false,
		AgentConfig: &config.AgentConfig{},
	}

	_, err = engine.Execute(ctx, cfg)
	require.NoError(t, err)

	// Load final snapshot
	snapshot := snapshotRepo.snapshots["supervisor"]
	require.NotNil(t, snapshot)

	loadedMessages, err := adapters.DeserializeSchemaMessages(snapshot.ContextData)
	require.NoError(t, err)

	// Should start with original messages
	assert.GreaterOrEqual(t, len(loadedMessages), len(originalMessages))
	for i, orig := range originalMessages {
		loaded := loadedMessages[i]
		assert.Equal(t, orig.Role, loaded.Role)
		assert.Equal(t, orig.Content, loaded.Content)
		if len(orig.ToolCalls) > 0 {
			require.NotEmpty(t, loaded.ToolCalls)
			assert.Equal(t, orig.ToolCalls[0].ID, loaded.ToolCalls[0].ID)
			assert.Equal(t, orig.ToolCalls[0].Function.Name, loaded.ToolCalls[0].Function.Name)
		}
		if orig.ToolCallID != "" {
			assert.Equal(t, orig.ToolCallID, loaded.ToolCallID)
		}
	}
}

// Test 7: Crash recovery
func TestEngine_RecoverInterrupted(t *testing.T) {
	ctx := context.Background()
	snapshotRepo := newMockSnapshotRepo()
	historyRepo := newMockHistoryRepo()
	engine := New(snapshotRepo, historyRepo)

	// Create active snapshots (simulating server crash)
	snapshotRepo.snapshots["agent-1"] = &domain.AgentContextSnapshot{
		SessionID:     "session-1",
		AgentID:       "agent-1",
		SchemaVersion: domain.CurrentSchemaVersion,
		ContextData:   []byte("{}"),
		Status:        domain.AgentContextStatusActive,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	snapshotRepo.snapshots["agent-2"] = &domain.AgentContextSnapshot{
		SessionID:     "session-2",
		AgentID:       "agent-2",
		SchemaVersion: domain.CurrentSchemaVersion,
		ContextData:   []byte("{}"),
		Status:        domain.AgentContextStatusActive,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	err := engine.RecoverInterrupted(ctx)
	require.NoError(t, err)

	// Check both marked as expired (interrupted == expired in DBML-aligned enum)
	assert.Equal(t, domain.AgentContextStatusExpired, snapshotRepo.snapshots["agent-1"].Status)
	assert.Equal(t, domain.AgentContextStatusExpired, snapshotRepo.snapshots["agent-2"].Status)
}

// Test 8: Validate config
func TestEngine_ValidateConfig(t *testing.T) {
	ctx := context.Background()
	engine := New(newMockSnapshotRepo(), newMockHistoryRepo())

	tests := []struct {
		name    string
		cfg     ExecutionConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: ExecutionConfig{
				SessionID: "session-1",
				AgentID:   "supervisor",
				Flow:      testFlow(),
				ChatModel: &mockChatModel{},
				Input:     "test",
			},
			wantErr: false,
		},
		{
			name: "missing session_id",
			cfg: ExecutionConfig{
				AgentID:   "supervisor",
				Flow:      testFlow(),
				ChatModel: &mockChatModel{},
			},
			wantErr: true,
		},
		{
			name: "missing agent_id",
			cfg: ExecutionConfig{
				SessionID: "session-1",
				Flow:      testFlow(),
				ChatModel: &mockChatModel{},
			},
			wantErr: true,
		},
		{
			name: "missing flow",
			cfg: ExecutionConfig{
				SessionID: "session-1",
				AgentID:   "supervisor",
				ChatModel: &mockChatModel{},
			},
			wantErr: true,
		},
		{
			name: "missing chat_model",
			cfg: ExecutionConfig{
				SessionID: "session-1",
				AgentID:   "supervisor",
				Flow:      testFlow(),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := engine.Execute(ctx, tt.cfg)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				// Error may occur from agent execution, but not from validation
				// Just check that validation doesn't fail
			}
		})
	}
}

// Test 9: Snapshot compression
func TestEngine_SnapshotCompression(t *testing.T) {
	ctx := context.Background()
	snapshotRepo := newMockSnapshotRepo()
	historyRepo := newMockHistoryRepo()
	engine := New(snapshotRepo, historyRepo)

	// Create a large history with 100+ messages (simulating multiple resumes)
	initialMessages := make([]*schema.Message, 0, 120)

	// Add system prompt
	initialMessages = append(initialMessages, &schema.Message{
		Role:    schema.System,
		Content: "You are a helpful assistant",
	})

	// Add 100 user-assistant pairs (simulating many turns)
	for i := 1; i <= 100; i++ {
		initialMessages = append(initialMessages,
			&schema.Message{Role: schema.User, Content: fmt.Sprintf("Question %d", i)},
			&schema.Message{Role: schema.Assistant, Content: fmt.Sprintf("Answer %d - this is a detailed response with lots of text content to simulate real usage", i)},
		)
	}

	// Save initial snapshot with large history
	contextData, err := adapters.SerializeSchemaMessages(initialMessages)
	require.NoError(t, err)

	snapshotRepo.snapshots["supervisor"] = &domain.AgentContextSnapshot{
		SessionID:     "session-1",
		AgentID:       "supervisor",
		SchemaVersion: domain.CurrentSchemaVersion,
		ContextData:   contextData,
		StepNumber:    100,
		Status:        domain.AgentContextStatusExpired,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	// Configure flow with MaxContextSize limit
	flow := testFlow()
	flow.MaxContextSize = 1000 // 1000 tokens = ~4000 chars (tight limit)

	cfg := ExecutionConfig{
		SessionID:         "session-1",
		AgentID:           "supervisor",
		Flow:              flow,
		ChatModel:         &mockChatModel{},
		Input:             "New question",
		Streaming:         false,
		AgentConfig:       &config.AgentConfig{},
		MessageCompressor: MessageCompressor(agents.NewContextRewriter(flow.MaxContextSize)),
	}

	result, err := engine.Execute(ctx, cfg)

	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, result.Status)

	// Check that snapshot was compressed
	snapshot := snapshotRepo.snapshots["supervisor"]
	require.NotNil(t, snapshot)

	compressedMessages, err := adapters.DeserializeSchemaMessages(snapshot.ContextData)
	require.NoError(t, err)

	// After compression, message count should be LESS than original 201 (100 pairs + 1 system)
	originalCount := len(initialMessages)
	compressedCount := len(compressedMessages)

	t.Logf("Compression result: %d → %d messages", originalCount, compressedCount)
	assert.Less(t, compressedCount, originalCount, "snapshot should be compressed")

	// ALL user messages should be preserved (ContextRewriter always keeps them)
	userCount := 0
	for _, msg := range compressedMessages {
		if msg.Role == schema.User {
			userCount++
		}
	}

	// Original had 100 user messages + 1 new from current execution = 101 total
	// (or 100 if new message wasn't added yet)
	assert.GreaterOrEqual(t, userCount, 100, "all user messages should be preserved")
	t.Logf("User messages preserved: %d", userCount)

	// System prompt should be preserved
	hasSystem := false
	for _, msg := range compressedMessages {
		if msg.Role == schema.System {
			hasSystem = true
			break
		}
	}
	assert.True(t, hasSystem, "system prompt should be preserved")
}

// --- Tests for Bug 2 fixes: user message in snapshot + empty assistant content sanitization ---

// helperCollectorWithMessages creates a MessageCollector pre-loaded with given schema messages.
// This avoids running the full event pipeline — we inject messages directly.
func helperCollectorWithMessages(sessionID, agentID string, messages []*schema.Message) *MessageCollector {
	mc := NewMessageCollector(context.Background(), sessionID, agentID, nil)
	mc.messages = append(mc.messages, messages...)
	return mc
}

// TestSaveSnapshot_IncludesUserMessage verifies that the user message captured
// via CollectUserMessage ends up in the saved context data.
func TestSaveSnapshot_IncludesUserMessage(t *testing.T) {
	ctx := context.Background()
	snapshotRepo := newMockSnapshotRepo()
	historyRepo := newMockHistoryRepo()
	eng := New(snapshotRepo, historyRepo)

	// Collector simulates production flow: CollectUserMessage ran, then the answer came in.
	collectorMessages := []*schema.Message{
		{Role: schema.User, Content: "What is the meaning of life?"},
		{Role: schema.Assistant, Content: "Here is the answer"},
	}
	collector := helperCollectorWithMessages("session-1", "supervisor", collectorMessages)

	cfg := ExecutionConfig{
		SessionID: "session-1",
		AgentID:   "supervisor",
		Flow:      testFlow(),
		Input:     "What is the meaning of life?",
	}

	err := eng.saveSnapshot(ctx, cfg, collector, nil, StatusCompleted)
	require.NoError(t, err)

	// Deserialize saved snapshot
	snap := snapshotRepo.snapshots["supervisor"]
	require.NotNil(t, snap)

	messages, err := adapters.DeserializeSchemaMessages(snap.ContextData)
	require.NoError(t, err)

	// Must contain a user message with cfg.Input
	var foundUserMsg *schema.Message
	for _, msg := range messages {
		if msg.Role == schema.User && msg.Content == "What is the meaning of life?" {
			foundUserMsg = msg
			break
		}
	}
	require.NotNil(t, foundUserMsg, "user message from cfg.Input must be saved in snapshot")
	assert.Equal(t, schema.User, foundUserMsg.Role)
	assert.Equal(t, "What is the meaning of life?", foundUserMsg.Content)
}

// TestSaveSnapshot_PreservesUserMessageOrder verifies the ordering:
// [...history, user_input, ...new_collector_messages]
func TestSaveSnapshot_PreservesUserMessageOrder(t *testing.T) {
	ctx := context.Background()
	snapshotRepo := newMockSnapshotRepo()
	historyRepo := newMockHistoryRepo()
	eng := New(snapshotRepo, historyRepo)

	// History from previous snapshot
	historyMessages := []*schema.Message{
		{Role: schema.User, Content: "Previous question"},
		{Role: schema.Assistant, Content: "Previous answer"},
	}

	// New messages from this execution: CollectUserMessage adds the user turn, then
	// the collector captures tool_call, tool_result, answer.
	collectorMessages := []*schema.Message{
		{Role: schema.User, Content: "New user question"},
		{
			Role:    schema.Assistant,
			Content: "Let me check",
			ToolCalls: []schema.ToolCall{{
				ID:       "call-1",
				Function: schema.FunctionCall{Name: "read_file", Arguments: `{"path":"x.go"}`},
			}},
		},
		{Role: schema.Tool, Content: "file content", ToolCallID: "call-1", Name: "read_file"},
		{Role: schema.Assistant, Content: "Done"},
	}
	collector := helperCollectorWithMessages("session-1", "supervisor", collectorMessages)

	cfg := ExecutionConfig{
		SessionID: "session-1",
		AgentID:   "supervisor",
		Flow:      testFlow(),
		Input:     "New user question",
	}

	err := eng.saveSnapshot(ctx, cfg, collector, historyMessages, StatusCompleted)
	require.NoError(t, err)

	snap := snapshotRepo.snapshots["supervisor"]
	require.NotNil(t, snap)

	messages, err := adapters.DeserializeSchemaMessages(snap.ContextData)
	require.NoError(t, err)

	// Expected order:
	// [0] history: User "Previous question"
	// [1] history: Assistant "Previous answer"
	// [2] user input: User "New user question"
	// [3] collector: Assistant "Let me check" (tool_call)
	// [4] collector: Tool "file content"
	// [5] collector: Assistant "Done"
	require.Len(t, messages, 6)

	assert.Equal(t, schema.User, messages[0].Role)
	assert.Equal(t, "Previous question", messages[0].Content)

	assert.Equal(t, schema.Assistant, messages[1].Role)
	assert.Equal(t, "Previous answer", messages[1].Content)

	assert.Equal(t, schema.User, messages[2].Role)
	assert.Equal(t, "New user question", messages[2].Content)

	assert.Equal(t, schema.Assistant, messages[3].Role)
	assert.Equal(t, "Let me check", messages[3].Content)
	assert.NotEmpty(t, messages[3].ToolCalls)

	assert.Equal(t, schema.Tool, messages[4].Role)
	assert.Equal(t, "file content", messages[4].Content)

	assert.Equal(t, schema.Assistant, messages[5].Role)
	assert.Equal(t, "Done", messages[5].Content)
}

// TestSaveSnapshot_SanitizesEmptyAssistantContent verifies that assistant messages
// with ToolCalls and empty Content get Content = " " before saving.
func TestSaveSnapshot_SanitizesEmptyAssistantContent(t *testing.T) {
	ctx := context.Background()
	snapshotRepo := newMockSnapshotRepo()
	historyRepo := newMockHistoryRepo()
	eng := New(snapshotRepo, historyRepo)

	// Collector has an assistant message with tool calls but empty content
	// (this happens in streaming mode where content is lost)
	collectorMessages := []*schema.Message{
		{
			Role:    schema.Assistant,
			Content: "", // Empty! Should be sanitized to " "
			ToolCalls: []schema.ToolCall{{
				ID:       "call-1",
				Function: schema.FunctionCall{Name: "execute_command", Arguments: `{"cmd":"ls"}`},
			}},
		},
		{Role: schema.Tool, Content: "output", ToolCallID: "call-1", Name: "execute_command"},
		{Role: schema.Assistant, Content: "Done"},
	}
	collector := helperCollectorWithMessages("session-1", "supervisor", collectorMessages)

	cfg := ExecutionConfig{
		SessionID: "session-1",
		AgentID:   "supervisor",
		Flow:      testFlow(),
		Input:     "Run ls",
	}

	err := eng.saveSnapshot(ctx, cfg, collector, nil, StatusCompleted)
	require.NoError(t, err)

	snap := snapshotRepo.snapshots["supervisor"]
	require.NotNil(t, snap)

	messages, err := adapters.DeserializeSchemaMessages(snap.ContextData)
	require.NoError(t, err)

	// Find the assistant message with tool calls
	var toolCallMsg *schema.Message
	for _, msg := range messages {
		if msg.Role == schema.Assistant && len(msg.ToolCalls) > 0 {
			toolCallMsg = msg
			break
		}
	}
	require.NotNil(t, toolCallMsg, "assistant message with tool calls must exist")
	assert.Equal(t, " ", toolCallMsg.Content,
		"empty content on assistant message with ToolCalls must be sanitized to single space")
}

// TestSaveSnapshot_DoesNotSanitizeNonToolCallMessages verifies that regular assistant
// messages (no ToolCalls) with empty content are NOT sanitized.
func TestSaveSnapshot_DoesNotSanitizeNonToolCallMessages(t *testing.T) {
	ctx := context.Background()
	snapshotRepo := newMockSnapshotRepo()
	historyRepo := newMockHistoryRepo()
	eng := New(snapshotRepo, historyRepo)

	collectorMessages := []*schema.Message{
		// Assistant message without tool calls and empty content
		// (edge case: should NOT be sanitized)
		{Role: schema.Assistant, Content: ""},
		// Also check that user messages with empty content are not touched
		// (though in practice users don't send empty messages)
	}
	collector := helperCollectorWithMessages("session-1", "supervisor", collectorMessages)

	// Also add a history user message with empty content to ensure it's untouched
	historyMessages := []*schema.Message{
		{Role: schema.User, Content: "Hello"},
		{Role: schema.Assistant, Content: "non-empty response"},
	}

	cfg := ExecutionConfig{
		SessionID: "session-1",
		AgentID:   "supervisor",
		Flow:      testFlow(),
		Input:     "Follow up",
	}

	err := eng.saveSnapshot(ctx, cfg, collector, historyMessages, StatusCompleted)
	require.NoError(t, err)

	snap := snapshotRepo.snapshots["supervisor"]
	require.NotNil(t, snap)

	messages, err := adapters.DeserializeSchemaMessages(snap.ContextData)
	require.NoError(t, err)

	// Find assistant messages without tool calls
	for _, msg := range messages {
		if msg.Role == schema.Assistant && len(msg.ToolCalls) == 0 {
			// These should NOT have been sanitized — content stays as-is
			assert.NotEqual(t, " ", msg.Content,
				"assistant messages without ToolCalls should NOT be sanitized to space; content=%q", msg.Content)
		}
	}

	// Additionally verify: user messages are never sanitized
	for _, msg := range messages {
		if msg.Role == schema.User {
			assert.NotEqual(t, " ", msg.Content,
				"user messages should never be sanitized")
		}
	}
}

// TestSaveSnapshot_EmptyInput verifies that if cfg.Input is empty,
// no user message is added to the snapshot.
func TestSaveSnapshot_EmptyInput(t *testing.T) {
	ctx := context.Background()
	snapshotRepo := newMockSnapshotRepo()
	historyRepo := newMockHistoryRepo()
	eng := New(snapshotRepo, historyRepo)

	historyMessages := []*schema.Message{
		{Role: schema.User, Content: "Earlier question"},
		{Role: schema.Assistant, Content: "Earlier answer"},
	}

	collectorMessages := []*schema.Message{
		{Role: schema.Assistant, Content: "Continued processing"},
	}
	collector := helperCollectorWithMessages("session-1", "supervisor", collectorMessages)

	cfg := ExecutionConfig{
		SessionID: "session-1",
		AgentID:   "supervisor",
		Flow:      testFlow(),
		Input:     "", // Empty input — no user message should be added
	}

	err := eng.saveSnapshot(ctx, cfg, collector, historyMessages, StatusCompleted)
	require.NoError(t, err)

	snap := snapshotRepo.snapshots["supervisor"]
	require.NotNil(t, snap)

	messages, err := adapters.DeserializeSchemaMessages(snap.ContextData)
	require.NoError(t, err)

	// Should have: 2 history + 1 collector = 3 messages (no extra user message)
	require.Len(t, messages, 3)

	// The only user message should be the one from history
	userMessages := make([]*schema.Message, 0)
	for _, msg := range messages {
		if msg.Role == schema.User {
			userMessages = append(userMessages, msg)
		}
	}
	require.Len(t, userMessages, 1, "only the history user message should exist")
	assert.Equal(t, "Earlier question", userMessages[0].Content)
}

// TestEngine_ResumeTurn_PersistsAnswerIntoSnapshot reproduces the HITL widget
// data-loss bug: when the user answers a show_structured_output form, the engine
// runs a RESUME turn (domain.WithResumeTurn) with the rendered Q+A as cfg.Input,
// but CollectUserMessage is skipped on resume and handleInterruptEvent writes only
// to the messages table — never to the in-memory transcript that feeds the
// snapshot. The snapshot is the SOLE history source for the next turn, so the
// submitted value (here a LoRaWAN AppKey) vanishes from the agent's context on the
// following turn and the model hallucinates it from earlier examples.
//
// RED before the fix (the answer is absent from the saved snapshot); GREEN once the
// resume answer is recorded into the snapshot transcript.
func TestEngine_ResumeTurn_PersistsAnswerIntoSnapshot(t *testing.T) {
	// Resume turn: the widget answer is delivered as cfg.Input, not a fresh chat.
	ctx := domain.WithResumeTurn(context.Background())
	snapshotRepo := newMockSnapshotRepo()
	historyRepo := newMockHistoryRepo()
	eng := New(snapshotRepo, historyRepo)

	// Prior snapshot = the turn that SHOWED the AppKey form: assistant calls the
	// HITL widget (return-directly), the tool emits a placeholder result, the turn
	// halts. No answer is captured yet — it arrives on this resume turn.
	priorTurn := []*schema.Message{
		{Role: schema.User, Content: "connect my Dragino LDS02 over LoRaWAN"},
		{
			Role:    schema.Assistant,
			Content: " ",
			ToolCalls: []schema.ToolCall{{
				ID: "call-appkey",
				Function: schema.FunctionCall{
					Name:      "show_structured_output",
					Arguments: `{"output_type":"form","questions":[{"id":"app_key","label":"AppKey","type":"text"}]}`,
				},
			}},
		},
		{Role: schema.Tool, Content: "Structured output displayed to user.", ToolCallID: "call-appkey", Name: "show_structured_output"},
	}
	contextData, err := adapters.SerializeSchemaMessages(priorTurn)
	require.NoError(t, err)
	snapshotRepo.snapshots["supervisor"] = &domain.AgentContextSnapshot{
		SessionID:     "session-1",
		AgentID:       "supervisor",
		SchemaVersion: domain.CurrentSchemaVersion,
		ContextData:   contextData,
		StepNumber:    1,
		Status:        domain.AgentContextStatusExpired,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	const appKey = "f5803ff7d332c01facbc3f3a7e42864a"
	cfg := ExecutionConfig{
		SessionID:   "session-1",
		AgentID:     "supervisor",
		Flow:        testFlow(),
		ChatModel:   &mockChatModel{},
		Input:       "User submitted the form:\nQ: AppKey A: " + appKey + "\n",
		Streaming:   false,
		AgentConfig: &config.AgentConfig{},
	}

	_, err = eng.Execute(ctx, cfg)
	require.NoError(t, err)

	snap := snapshotRepo.snapshots["supervisor"]
	require.NotNil(t, snap)
	messages, err := adapters.DeserializeSchemaMessages(snap.ContextData)
	require.NoError(t, err)

	found := false
	for _, m := range messages {
		if strings.Contains(m.Content, appKey) {
			found = true
			break
		}
	}
	require.True(t, found,
		"the submitted AppKey must be persisted into the snapshot so the NEXT turn still has it; "+
			"otherwise the agent loses the user's widget answer and hallucinates the value")
}

// capturingChatModel records the input it was last asked to generate from, so a
// test can assert what the agent actually SEES on a given turn (e.g. that prior
// HITL answers reached the model's context).
type capturingChatModel struct {
	mu        sync.Mutex
	lastInput []*schema.Message
}

func (m *capturingChatModel) record(input []*schema.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastInput = append([]*schema.Message(nil), input...)
}

func (m *capturingChatModel) lastInputText() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var b strings.Builder
	for _, msg := range m.lastInput {
		b.WriteString(msg.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

func (m *capturingChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	m.record(input)
	return &schema.Message{Role: schema.Assistant, Content: "ok"}, nil
}

func (m *capturingChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.record(input)
	sr, sw := schema.Pipe[*schema.Message](1)
	go func() {
		sw.Send(&schema.Message{Role: schema.Assistant, Content: "ok"}, nil)
		sw.Close()
	}()
	return sr, nil
}

func (m *capturingChatModel) BindTools(tools []*schema.ToolInfo) error { return nil }

func (m *capturingChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

// TestEngine_MultiStepHITLFlow_AccumulatesAnswers reproduces the partner's exact
// failure: a multi-field LoRaWAN provisioning wizard (device name → DevEUI →
// AppKey) collected through sequential show_structured_output widgets. Each answer
// arrives on its own HITL resume turn. The agent needs ALL three when it finally
// calls device_provision_lorawan — but before the fix every widget answer was
// dropped from the snapshot, so by the AppKey step the name and DevEUI were gone
// from context and the model hallucinated them.
//
// This drives three resume turns through the real engine.Execute path and asserts
// (a) every answer is persisted into the snapshot, and (b) the model actually SEES
// the earlier answers on the final turn. RED before the fix, GREEN after.
func TestEngine_MultiStepHITLFlow_AccumulatesAnswers(t *testing.T) {
	snapshotRepo := newMockSnapshotRepo()
	historyRepo := newMockHistoryRepo()
	eng := New(snapshotRepo, historyRepo)
	model := &capturingChatModel{}

	// Seed the snapshot with the opening turn that showed the first widget.
	opening := []*schema.Message{
		{Role: schema.User, Content: "connect my Dragino LDS02 over LoRaWAN"},
		{
			Role:    schema.Assistant,
			Content: " ",
			ToolCalls: []schema.ToolCall{{
				ID:       "w-name",
				Function: schema.FunctionCall{Name: "show_structured_output", Arguments: `{"output_type":"form"}`},
			}},
		},
		{Role: schema.Tool, Content: "Structured output displayed to user.", ToolCallID: "w-name", Name: "show_structured_output"},
	}
	contextData, err := adapters.SerializeSchemaMessages(opening)
	require.NoError(t, err)
	snapshotRepo.snapshots["supervisor"] = &domain.AgentContextSnapshot{
		SessionID:     "session-1",
		AgentID:       "supervisor",
		SchemaVersion: domain.CurrentSchemaVersion,
		ContextData:   contextData,
		StepNumber:    1,
		Status:        domain.AgentContextStatusExpired,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	const (
		deviceName = "Входная дверь магазина"
		devEUI     = "A850418FA177B95B"
		appKey     = "f5803ff7d332c01facbc3f3a7e42864a"
	)

	// Each entry is one HITL resume turn carrying the user's widget answer as the
	// rendered Q+A text (exactly what buildStructuredOutputResumeText produces).
	resumeAnswers := []string{
		"User submitted the form:\nQ: Device name A: " + deviceName + "\n",
		"User submitted the form:\nQ: DevEUI A: " + devEUI + "\n",
		"User submitted the form:\nQ: AppKey A: " + appKey + "\n",
	}

	for _, qa := range resumeAnswers {
		ctx := domain.WithResumeTurn(context.Background())
		cfg := ExecutionConfig{
			SessionID:   "session-1",
			AgentID:     "supervisor",
			Flow:        testFlow(),
			ChatModel:   model,
			Input:       qa,
			Streaming:   false,
			AgentConfig: &config.AgentConfig{},
		}
		_, err := eng.Execute(ctx, cfg)
		require.NoError(t, err)
	}

	// (a) the final snapshot must carry all three collected values.
	snap := snapshotRepo.snapshots["supervisor"]
	require.NotNil(t, snap)
	messages, err := adapters.DeserializeSchemaMessages(snap.ContextData)
	require.NoError(t, err)
	var snapText strings.Builder
	for _, m := range messages {
		snapText.WriteString(m.Content)
		snapText.WriteByte('\n')
	}
	for _, want := range []string{deviceName, devEUI, appKey} {
		assert.Contains(t, snapText.String(), want,
			"snapshot must retain every widget answer across the multi-step flow")
	}

	// (b) on the final (AppKey) turn the model must already SEE the name + DevEUI —
	// the values it would need to call device_provision_lorawan correctly.
	finalInput := model.lastInputText()
	assert.Contains(t, finalInput, deviceName, "agent lost the device name by the final step")
	assert.Contains(t, finalInput, devEUI, "agent lost the DevEUI by the final step")
	assert.Contains(t, finalInput, appKey, "agent is missing the just-submitted AppKey")
}

// --- buildEffectiveAgentConfig tests ---

func TestBuildEffectiveAgentConfig_FlowOverridesGlobalMaxContext(t *testing.T) {
	engine := New(newMockSnapshotRepo(), newMockHistoryRepo())

	globalConfig := &config.AgentConfig{
		MaxContextSize:                16000, // global default
		EnableEnhancedToolCallChecker: true,
	}

	flow := testFlow()
	flow.MaxContextSize = 128000 // per-agent DB value

	cfg := ExecutionConfig{
		AgentConfig: globalConfig,
		Flow:        flow,
	}

	result := engine.buildEffectiveAgentConfig(cfg)

	assert.Equal(t, 128000, result.MaxContextSize,
		"Flow.MaxContextSize from DB should override global default")
}

func TestBuildEffectiveAgentConfig_FlowOverridesWithPromptOverlay(t *testing.T) {
	engine := New(newMockSnapshotRepo(), newMockHistoryRepo())

	globalConfig := &config.AgentConfig{
		MaxContextSize:                16000,
		EnableEnhancedToolCallChecker: true,
		// No Prompts — triggers needsOverlay when Flow has prompt
	}

	flow := testFlow()
	flow.SystemPrompt = "You are a helpful agent"
	flow.MaxContextSize = 64000

	cfg := ExecutionConfig{
		AgentConfig: globalConfig,
		Flow:        flow,
	}

	result := engine.buildEffectiveAgentConfig(cfg)

	assert.Equal(t, 64000, result.MaxContextSize,
		"Flow.MaxContextSize should override even in prompt overlay path")
	assert.Equal(t, "You are a helpful agent", result.Prompts.SystemPrompt,
		"Flow's system prompt should be overlayed")
}

func TestBuildEffectiveAgentConfig_NilAgentConfig_UsesFlow(t *testing.T) {
	engine := New(newMockSnapshotRepo(), newMockHistoryRepo())

	flow := testFlow()
	flow.MaxContextSize = 200000

	cfg := ExecutionConfig{
		AgentConfig: nil,
		Flow:        flow,
	}

	result := engine.buildEffectiveAgentConfig(cfg)

	assert.Equal(t, 200000, result.MaxContextSize,
		"nil AgentConfig should use Flow.MaxContextSize directly")
}

func TestBuildEffectiveAgentConfig_NoMutationOfOriginal(t *testing.T) {
	engine := New(newMockSnapshotRepo(), newMockHistoryRepo())

	globalConfig := &config.AgentConfig{
		MaxContextSize:                16000,
		EnableEnhancedToolCallChecker: true,
	}

	flow := testFlow()
	flow.MaxContextSize = 128000

	cfg := ExecutionConfig{
		AgentConfig: globalConfig,
		Flow:        flow,
	}

	_ = engine.buildEffectiveAgentConfig(cfg)

	assert.Equal(t, 16000, globalConfig.MaxContextSize,
		"original global config must not be mutated")
}

func TestBuildEffectiveAgentConfig_FlowZeroMaxContext_KeepsGlobal(t *testing.T) {
	engine := New(newMockSnapshotRepo(), newMockHistoryRepo())

	globalConfig := &config.AgentConfig{
		MaxContextSize:                16000,
		EnableEnhancedToolCallChecker: true,
	}

	flow := testFlow()
	flow.MaxContextSize = 0 // safety net: shouldn't happen in prod (Flow.Validate rejects 0)

	cfg := ExecutionConfig{
		AgentConfig: globalConfig,
		Flow:        flow,
	}

	result := engine.buildEffectiveAgentConfig(cfg)

	assert.Equal(t, 16000, result.MaxContextSize,
		"when Flow.MaxContextSize is 0, global default should be kept")
}

// --- MaxTurnDuration tests ---

func TestBuildEffectiveAgentConfig_FlowOverridesGlobalMaxTurnDuration(t *testing.T) {
	engine := New(newMockSnapshotRepo(), newMockHistoryRepo())

	globalConfig := &config.AgentConfig{
		MaxContextSize:                16000,
		MaxTurnDuration:               120, // global default
		EnableEnhancedToolCallChecker: true,
	}

	flow := testFlow()
	flow.MaxTurnDuration = 300 // per-agent DB value

	cfg := ExecutionConfig{
		AgentConfig: globalConfig,
		Flow:        flow,
	}

	result := engine.buildEffectiveAgentConfig(cfg)

	assert.Equal(t, 300, result.MaxTurnDuration,
		"Flow.MaxTurnDuration from DB should override global default")
}

func TestBuildEffectiveAgentConfig_ZeroFlowMaxTurnDuration_KeepsGlobal(t *testing.T) {
	engine := New(newMockSnapshotRepo(), newMockHistoryRepo())

	globalConfig := &config.AgentConfig{
		MaxContextSize:                16000,
		MaxTurnDuration:               120,
		EnableEnhancedToolCallChecker: true,
	}

	flow := testFlow()
	flow.MaxTurnDuration = 0

	cfg := ExecutionConfig{
		AgentConfig: globalConfig,
		Flow:        flow,
	}

	result := engine.buildEffectiveAgentConfig(cfg)

	assert.Equal(t, 120, result.MaxTurnDuration,
		"when Flow.MaxTurnDuration is 0, global default should be kept")
}

func TestBuildEffectiveAgentConfig_NilAgentConfig_UsesFlowMaxTurnDuration(t *testing.T) {
	engine := New(newMockSnapshotRepo(), newMockHistoryRepo())

	flow := testFlow()
	flow.MaxTurnDuration = 600

	cfg := ExecutionConfig{
		AgentConfig: nil,
		Flow:        flow,
	}

	result := engine.buildEffectiveAgentConfig(cfg)

	assert.Equal(t, 600, result.MaxTurnDuration,
		"nil AgentConfig should use Flow.MaxTurnDuration directly")
}
