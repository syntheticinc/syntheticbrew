package e2e

import (
	"context"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools"
	"github.com/syntheticinc/syntheticbrew/internal/service/agent"
	"github.com/syntheticinc/syntheticbrew/internal/service/orchestrator"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// mockChatModel implements ChatModel for testing
type mockChatModel struct{}

func newMockChatModel() *mockChatModel {
	return &mockChatModel{}
}

func (m *mockChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return &schema.Message{
		Role:    schema.Assistant,
		Content: "This is a mock response for testing purposes.",
	}, nil
}

func (m *mockChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	// Return simple mock stream
	idx := 0
	chunks := []string{"Mock ", "streaming ", "response."}

	reader := &schema.StreamReader[*schema.Message]{}
	// Note: Simplified implementation for testing
	_ = idx
	_ = chunks
	return reader, nil
}

func (m *mockChatModel) BindTools(tools []*schema.ToolInfo) error {
	return nil
}

func (m *mockChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

// newMockAgentService creates a mock AgentService for testing
func newMockAgentService() *agent.Service {
	cfg := agent.Config{
		ChatModel: newMockChatModel(),
		MaxSteps:  10,
	}

	agentService, err := agent.New(cfg)
	if err != nil {
		panic("failed to create mock agent service: " + err.Error())
	}

	return agentService
}

// mockTurnExecutorFactory implements TurnExecutorFactory for testing
type mockTurnExecutorFactory struct{}

func (f *mockTurnExecutorFactory) CreateForSession(proxy tools.ClientOperationsProxy, sessionID, projectKey, projectRoot, platform, agentName string) orchestrator.TurnExecutor {
	return &mockTurnExecutor{}
}

// mockTurnExecutor implements orchestrator.TurnExecutor for testing
type mockTurnExecutor struct{}

func (e *mockTurnExecutor) ExecuteTurn(ctx context.Context, sessionID, projectKey, question string,
	chunkCb func(string) error, eventCb func(*domain.AgentEvent) error) error {
	if chunkCb != nil {
		_ = chunkCb("mock answer")
	}
	return nil
}

func newMockTurnExecutorFactory() *mockTurnExecutorFactory {
	return &mockTurnExecutorFactory{}
}
