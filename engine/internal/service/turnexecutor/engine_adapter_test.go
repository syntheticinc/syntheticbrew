package turnexecutor

import (
	"context"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools"
	"github.com/syntheticinc/syntheticbrew/internal/service/engine"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
	"github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock implementations

type mockEngine struct {
	executeFn func(ctx context.Context, cfg engine.ExecutionConfig) (*engine.ExecutionResult, error)
}

func (m *mockEngine) Execute(ctx context.Context, cfg engine.ExecutionConfig) (*engine.ExecutionResult, error) {
	if m.executeFn == nil {
		return &engine.ExecutionResult{Status: engine.StatusCompleted}, nil
	}
	return m.executeFn(ctx, cfg)
}

// HistoryRepo returns nil — tests don't exercise the messages-table persistence
// path; production wires the real GORM-backed repo in server.go.
func (m *mockEngine) HistoryRepo() engine.HistoryRepository {
	return nil
}

type mockFlowProvider struct {
	flow *domain.Flow
	err  error
}

func (m *mockFlowProvider) GetFlow(ctx context.Context, agentName string) (*domain.Flow, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.flow == nil {
		return testFlowForAdapter(), nil
	}
	return m.flow, nil
}

type mockToolResolver struct {
	tools []einotool.InvokableTool
	err   error
}

func (m *mockToolResolver) Resolve(ctx context.Context, toolNames []string, deps tools.ToolDependencies) ([]einotool.InvokableTool, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.tools, nil
}

type mockToolDependenciesProvider struct {
	deps tools.ToolDependencies
}

func (m *mockToolDependenciesProvider) GetDependencies(sessionID, projectKey string) tools.ToolDependencies {
	return m.deps
}

type mockContextReminderProvider struct {
	reminder string
	priority int
	enabled  bool
}

func (m *mockContextReminderProvider) GetContextReminder(ctx context.Context, sessionID string) (string, int, bool) {
	return m.reminder, m.priority, m.enabled
}

type mockToolCallRecorder struct{}

func (m *mockToolCallRecorder) RecordToolCall(sessionID, toolName string) {}

func (m *mockToolCallRecorder) RecordToolResult(sessionID, toolName, result string) {}

type mockChatModelAdapter struct {
	response *schema.Message
	err      error
}

func (m *mockChatModelAdapter) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
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

func (m *mockChatModelAdapter) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	sr, sw := schema.Pipe[*schema.Message](1)
	sw.Close()
	return sr, nil
}

func (m *mockChatModelAdapter) BindTools(tools []*schema.ToolInfo) error {
	return nil
}

func (m *mockChatModelAdapter) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

// Test helpers

func testFlowForAdapter() *domain.Flow {
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

// Test 1: NewEngineAdapter validation - all nil checks work
func TestEngineAdapter_NewValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name: "valid config",
			cfg: Config{
				Engine:       &mockEngine{},
				FlowProvider: &mockFlowProvider{},
				ToolResolver: &mockToolResolver{},
				ToolDeps:     &mockToolDependenciesProvider{},
				ChatModel:    &mockChatModelAdapter{},
			},
			wantErr: "",
		},
		{
			name: "missing engine",
			cfg: Config{
				FlowProvider: &mockFlowProvider{},
				ToolResolver: &mockToolResolver{},
				ToolDeps:     &mockToolDependenciesProvider{},
				ChatModel:    &mockChatModelAdapter{},
			},
			wantErr: "engine is required",
		},
		{
			name: "missing flow provider",
			cfg: Config{
				Engine:       &mockEngine{},
				ToolResolver: &mockToolResolver{},
				ToolDeps:     &mockToolDependenciesProvider{},
				ChatModel:    &mockChatModelAdapter{},
			},
			wantErr: "flow provider is required",
		},
		{
			name: "missing tool resolver",
			cfg: Config{
				Engine:       &mockEngine{},
				FlowProvider: &mockFlowProvider{},
				ToolDeps:     &mockToolDependenciesProvider{},
				ChatModel:    &mockChatModelAdapter{},
			},
			wantErr: "tool resolver is required",
		},
		{
			name: "missing tool dependencies provider",
			cfg: Config{
				Engine:       &mockEngine{},
				FlowProvider: &mockFlowProvider{},
				ToolResolver: &mockToolResolver{},
				ChatModel:    &mockChatModelAdapter{},
			},
			wantErr: "tool dependencies provider is required",
		},
		{
			name: "missing chat model",
			cfg: Config{
				Engine:       &mockEngine{},
				FlowProvider: &mockFlowProvider{},
				ToolResolver: &mockToolResolver{},
				ToolDeps:     &mockToolDependenciesProvider{},
			},
			wantErr: "chat model is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter, err := NewEngineAdapter(tt.cfg)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Nil(t, adapter)
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, adapter)
		})
	}
}

// Test 2: ExecuteTurn with mock Engine - correct ExecutionConfig passed
func TestEngineAdapter_ExecuteTurn(t *testing.T) {
	ctx := context.Background()

	// Track what was passed to engine.Execute
	var capturedCfg engine.ExecutionConfig
	mockEngine := &mockEngine{
		executeFn: func(ctx context.Context, cfg engine.ExecutionConfig) (*engine.ExecutionResult, error) {
			capturedCfg = cfg
			return &engine.ExecutionResult{
				Status: engine.StatusCompleted,
				Answer: "test answer",
			}, nil
		},
	}

	mockFlowProvider := &mockFlowProvider{
		flow: testFlowForAdapter(),
	}

	mockToolResolver := &mockToolResolver{
		tools: []einotool.InvokableTool{},
	}

	mockToolDeps := &mockToolDependenciesProvider{
		deps: tools.ToolDependencies{
			SessionID:  "session-1",
			ProjectKey: "project-1",
		},
	}

	mockChatModel := &mockChatModelAdapter{}

	agentConfig := &config.AgentConfig{
		MaxContextSize: 4000,
	}

	adapter, err := NewEngineAdapter(Config{
		Engine:       mockEngine,
		FlowProvider: mockFlowProvider,
		ToolResolver: mockToolResolver,
		ToolDeps:     mockToolDeps,
		ChatModel:    mockChatModel,
		AgentConfig:  agentConfig,
		ModelName:    "test-model",
		AgentName:    "supervisor",
		AgentUUID:    "supervisor",
	})
	require.NoError(t, err)

	// Execute turn
	chunkCallback := func(chunk string) error {
		return nil
	}
	eventCallback := func(event *domain.AgentEvent) error {
		return nil
	}

	err = adapter.ExecuteTurn(
		ctx,
		"session-1",
		"project-1",
		"test question",
		chunkCallback,
		eventCallback,
	)

	require.NoError(t, err)

	// Verify capturedCfg has correct values
	assert.Equal(t, "session-1", capturedCfg.SessionID)
	assert.Equal(t, "supervisor", capturedCfg.AgentID)
	assert.Equal(t, "test question", capturedCfg.Input)
	assert.True(t, capturedCfg.Streaming)
	assert.NotNil(t, capturedCfg.ChatModel)
	assert.NotNil(t, capturedCfg.Flow)
	assert.Equal(t, "supervisor", capturedCfg.Flow.Type)
	assert.NotNil(t, capturedCfg.ChunkCallback)
	assert.NotNil(t, capturedCfg.EventCallback)
	assert.Equal(t, "test-model", capturedCfg.ModelName)
	assert.Equal(t, agentConfig, capturedCfg.AgentConfig)
}

// Test 3: ExecuteTurn with optional dependencies
func TestEngineAdapter_ExecuteTurn_WithOptionalDeps(t *testing.T) {
	ctx := context.Background()

	var capturedCfg engine.ExecutionConfig
	mockEngine := &mockEngine{
		executeFn: func(ctx context.Context, cfg engine.ExecutionConfig) (*engine.ExecutionResult, error) {
			capturedCfg = cfg
			return &engine.ExecutionResult{Status: engine.StatusCompleted}, nil
		},
	}

	mockReminder := &mockContextReminderProvider{
		reminder: "test reminder",
		priority: 50,
		enabled:  true,
	}
	mockRecorder := &mockToolCallRecorder{}

	adapter, err := NewEngineAdapter(Config{
		Engine:           mockEngine,
		FlowProvider:     &mockFlowProvider{},
		ToolResolver:     &mockToolResolver{},
		ToolDeps:         &mockToolDependenciesProvider{},
		ChatModel:        &mockChatModelAdapter{},
		ContextReminders: []ContextReminderProvider{mockReminder},
		ToolCallRecorder: mockRecorder,
	})
	require.NoError(t, err)

	err = adapter.ExecuteTurn(
		ctx,
		"session-1",
		"project-1",
		"test question",
		func(chunk string) error { return nil },
		func(event *domain.AgentEvent) error { return nil },
	)

	require.NoError(t, err)

	// Verify optional dependencies were passed
	assert.NotNil(t, capturedCfg.ContextReminders)
	assert.Len(t, capturedCfg.ContextReminders, 1)
	assert.NotNil(t, capturedCfg.ToolCallRecorder)
}

// Test 4: ExecuteTurn handles flow provider error
func TestEngineAdapter_ExecuteTurn_FlowProviderError(t *testing.T) {
	ctx := context.Background()

	mockFlowProvider := &mockFlowProvider{
		err: assert.AnError,
	}

	adapter, err := NewEngineAdapter(Config{
		Engine:       &mockEngine{},
		FlowProvider: mockFlowProvider,
		ToolResolver: &mockToolResolver{},
		ToolDeps:     &mockToolDependenciesProvider{},
		ChatModel:    &mockChatModelAdapter{},
	})
	require.NoError(t, err)

	err = adapter.ExecuteTurn(
		ctx,
		"session-1",
		"project-1",
		"test question",
		func(chunk string) error { return nil },
		func(event *domain.AgentEvent) error { return nil },
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get flow")
}

// Test 5: ExecuteTurn handles tool resolver error
func TestEngineAdapter_ExecuteTurn_ToolResolverError(t *testing.T) {
	ctx := context.Background()

	mockToolResolver := &mockToolResolver{
		err: assert.AnError,
	}

	adapter, err := NewEngineAdapter(Config{
		Engine:       &mockEngine{},
		FlowProvider: &mockFlowProvider{},
		ToolResolver: mockToolResolver,
		ToolDeps:     &mockToolDependenciesProvider{},
		ChatModel:    &mockChatModelAdapter{},
	})
	require.NoError(t, err)

	err = adapter.ExecuteTurn(
		ctx,
		"session-1",
		"project-1",
		"test question",
		func(chunk string) error { return nil },
		func(event *domain.AgentEvent) error { return nil },
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve tools")
}

// Test 6: ExecuteTurn handles engine execution error
func TestEngineAdapter_ExecuteTurn_EngineError(t *testing.T) {
	ctx := context.Background()

	mockEngine := &mockEngine{
		executeFn: func(ctx context.Context, cfg engine.ExecutionConfig) (*engine.ExecutionResult, error) {
			return nil, assert.AnError
		},
	}

	adapter, err := NewEngineAdapter(Config{
		Engine:       mockEngine,
		FlowProvider: &mockFlowProvider{},
		ToolResolver: &mockToolResolver{},
		ToolDeps:     &mockToolDependenciesProvider{},
		ChatModel:    &mockChatModelAdapter{},
	})
	require.NoError(t, err)

	err = adapter.ExecuteTurn(
		ctx,
		"session-1",
		"project-1",
		"test question",
		func(chunk string) error { return nil },
		func(event *domain.AgentEvent) error { return nil },
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "execute engine")
}
