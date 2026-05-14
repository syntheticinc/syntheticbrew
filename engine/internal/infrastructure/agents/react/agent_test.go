package react

import (
	"context"
	"encoding/json"
	goerrors "errors"
	"fmt"
	"strings"
	"testing"

	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/llm"
	"github.com/syntheticinc/bytebrew/engine/pkg/config"
	pkgerrors "github.com/syntheticinc/bytebrew/engine/pkg/errors"
	"github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// mockChatModel is a mock implementation of model.ChatModel for testing
type mockChatModel struct {
	generateFunc       func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error)
	streamFunc         func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error)
	bindToolsFunc      func(tools []*schema.ToolInfo) error
	getTypeFunc        func() string
	isCallbacksEnabled bool
}

func (m *mockChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if m.generateFunc != nil {
		return m.generateFunc(ctx, input, opts...)
	}
	return &schema.Message{
		Role:    schema.Assistant,
		Content: "mock response",
	}, nil
}

func (m *mockChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if m.streamFunc != nil {
		return m.streamFunc(ctx, input, opts...)
	}
	return nil, nil
}

func (m *mockChatModel) BindTools(tools []*schema.ToolInfo) error {
	if m.bindToolsFunc != nil {
		return m.bindToolsFunc(tools)
	}
	return nil
}

func (m *mockChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func (m *mockChatModel) GetType() string {
	if m.getTypeFunc != nil {
		return m.getTypeFunc()
	}
	return "mock"
}

func (m *mockChatModel) IsCallbacksEnabled() bool {
	return m.isCallbacksEnabled
}

func TestNewAgent_NilChatModel_ReturnsError(t *testing.T) {
	cfg := AgentConfig{
		ChatModel: nil,
		MaxSteps:  10,
	}

	agent, err := NewAgent(context.Background(), cfg)

	if err == nil {
		t.Error("expected error when ChatModel is nil")
	}
	if agent != nil {
		t.Error("expected nil agent when ChatModel is nil")
	}
}

func TestNewAgent_ZeroMaxSteps_UsesUnlimited(t *testing.T) {
	mockModel := &mockChatModel{}
	cfg := AgentConfig{
		ChatModel: mockModel,
		MaxSteps:  0, // Zero means unlimited (uses 10000 internally)
	}

	agent, err := NewAgent(context.Background(), cfg)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agent == nil {
		t.Fatal("expected non-nil agent")
	}
}

func TestNewAgent_WithAgentConfig(t *testing.T) {
	mockModel := &mockChatModel{}
	agentConfig := &config.AgentConfig{
		MaxSteps:       10,
		MaxContextSize: 16000,
		ContextLogPath: "./test_logs",
		Prompts: &config.PromptsConfig{
			SystemPrompt:   "Test prompt",
			UrgencyWarning: "Warning: %d steps left",
		},
	}

	cfg := AgentConfig{
		ChatModel:   mockModel,
		MaxSteps:    10,
		SessionID:   "test-session-123",
		AgentConfig: agentConfig,
		ModelName:   "test-model",
	}

	agent, err := NewAgent(context.Background(), cfg)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agent == nil {
		t.Fatal("expected non-nil agent")
	}

	// Context logger should be created
	if agent.contextLogger == nil {
		t.Error("expected contextLogger to be created")
	}
}

func TestNewAgent_BuildMessagesWithHistory(t *testing.T) {
	mockModel := &mockChatModel{}

	historyMessages := []*schema.Message{
		{Role: schema.User, Content: "Previous question"},
		{Role: schema.Assistant, Content: "Previous answer"},
	}

	cfg := AgentConfig{
		ChatModel:       mockModel,
		MaxSteps:        10,
		HistoryMessages: historyMessages,
	}

	agent, err := NewAgent(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	messages := agent.buildMessagesWithHistory("Current question")

	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(messages))
	}

	if messages[0].Content != "Previous question" {
		t.Errorf("message[0] content: got %q, want %q", messages[0].Content, "Previous question")
	}
	if messages[2].Content != "Current question" {
		t.Errorf("message[2] content: got %q, want %q", messages[2].Content, "Current question")
	}
}

func TestAgentConfig_Structure(t *testing.T) {
	mockModel := &mockChatModel{}

	cfg := AgentConfig{
		ChatModel:       mockModel,
		Tools:           nil,
		MaxSteps:        15,
		SessionID:       "session-123",
		AgentConfig:     nil,
		ModelName:       "test-model",
		HistoryMessages: nil,
	}

	if cfg.MaxSteps != 15 {
		t.Errorf("MaxSteps: got %d, want 15", cfg.MaxSteps)
	}
	if cfg.SessionID != "session-123" {
		t.Errorf("SessionID: got %q, want %q", cfg.SessionID, "session-123")
	}
}

func TestIsRateLimitError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "429 Too Many Requests",
			err:      fmt.Errorf("429 Too Many Requests"),
			expected: true,
		},
		{
			name:     "rate limit exceeded",
			err:      fmt.Errorf("rate limit exceeded"),
			expected: true,
		},
		{
			name:     "Rate Limit (mixed case)",
			err:      fmt.Errorf("Rate Limit exceeded"),
			expected: true,
		},
		{
			name:     "quota exceeded",
			err:      fmt.Errorf("quota exceeded"),
			expected: true,
		},
		{
			name:     "too many requests lowercase",
			err:      fmt.Errorf("too many requests"),
			expected: true,
		},
		{
			name:     "generic error",
			err:      fmt.Errorf("some random error"),
			expected: false,
		},
		{
			name:     "context canceled",
			err:      context.Canceled,
			expected: false,
		},
		{
			name:     "unauthorized",
			err:      fmt.Errorf("unauthorized"),
			expected: false,
		},
		{
			name:     "XML error",
			err:      fmt.Errorf("XML syntax error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRateLimitError(tt.err)
			if result != tt.expected {
				t.Errorf("isRateLimitError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestIsRecoverableAgentError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "context canceled - not recoverable",
			err:      context.Canceled,
			expected: false,
		},
		{
			name:     "context deadline exceeded - not recoverable",
			err:      context.DeadlineExceeded,
			expected: false,
		},
		{
			name:     "rate limit - recoverable (handled separately)",
			err:      fmt.Errorf("rate limit exceeded"),
			expected: true,
		},
		{
			name:     "quota exceeded - recoverable (handled separately)",
			err:      fmt.Errorf("quota exceeded"),
			expected: true,
		},
		{
			name:     "unauthorized - not recoverable",
			err:      fmt.Errorf("unauthorized access"),
			expected: false,
		},
		{
			name:     "XML syntax error - recoverable",
			err:      fmt.Errorf("XML syntax error on line 1"),
			expected: true,
		},
		{
			name:     "JSON unmarshal error - recoverable",
			err:      fmt.Errorf("json: cannot unmarshal string"),
			expected: true,
		},
		{
			name:     "tool not found - recoverable",
			err:      fmt.Errorf("tool not found: unknown_tool"),
			expected: true,
		},
		{
			name:     "GraphRunError - recoverable",
			err:      fmt.Errorf("[GraphRunError] failed to calculate next tasks"),
			expected: true,
		},
		{
			name:     "generic error - recoverable",
			err:      fmt.Errorf("some random error"),
			expected: true,
		},
		{
			name:     "exceeds max steps - not recoverable",
			err:      fmt.Errorf("agent exceeds max steps limit"),
			expected: false,
		},
		{
			name:     "max steps - not recoverable",
			err:      fmt.Errorf("[GraphRunError] max steps reached"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRecoverableAgentError(tt.err)
			if result != tt.expected {
				t.Errorf("isRecoverableAgentError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestRateLimitBackoff(t *testing.T) {
	tests := []struct {
		attempt  int
		expected string
	}{
		{0, "2s"},
		{1, "4s"},
		{2, "8s"},
		{3, "16s"},
		{4, "32s"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("attempt_%d", tt.attempt), func(t *testing.T) {
			result := rateLimitBackoff(tt.attempt)
			if result.String() != tt.expected {
				t.Errorf("rateLimitBackoff(%d) = %v, want %v", tt.attempt, result, tt.expected)
			}
		})
	}
}

func TestFormatAgentErrorFeedback(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		contains string
	}{
		{
			name:     "nil error",
			err:      nil,
			contains: "",
		},
		{
			name:     "XML error",
			err:      fmt.Errorf("XML syntax error on line 1"),
			contains: "invalid XML format",
		},
		{
			name:     "JSON error",
			err:      fmt.Errorf("json: cannot unmarshal"),
			contains: "invalid JSON format",
		},
		{
			name:     "tool not found",
			err:      fmt.Errorf("tool not found: my_tool"),
			contains: "does not exist",
		},
		{
			name:     "generic error",
			err:      fmt.Errorf("something went wrong"),
			contains: "something went wrong",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatAgentErrorFeedback(tt.err)
			if tt.contains == "" {
				if result != "" {
					t.Errorf("formatAgentErrorFeedback(%v) = %q, want empty", tt.err, result)
				}
			} else if !strings.Contains(result, tt.contains) {
				t.Errorf("formatAgentErrorFeedback(%v) = %q, should contain %q", tt.err, result, tt.contains)
			}
		})
	}
}

func TestSanitizeToolArguments_ValidJSON(t *testing.T) {
	input := `{"file_path": "main.go"}`
	result := sanitizeToolArguments(input)
	if result != input {
		t.Errorf("expected unchanged, got %q", result)
	}
}

func TestSanitizeToolArguments_XMLTags(t *testing.T) {
	input := `<parameter>{"file_path": "main.go"}</parameter>`
	result := sanitizeToolArguments(input)
	if !json.Valid([]byte(result)) {
		t.Errorf("expected valid JSON after sanitization, got %q", result)
	}
	expected := `{"file_path": "main.go"}`
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestSanitizeToolArguments_MixedContent(t *testing.T) {
	input := `some text {"action": "list"} more text`
	result := sanitizeToolArguments(input)
	if !json.Valid([]byte(result)) {
		t.Errorf("expected valid JSON extracted, got %q", result)
	}
	expected := `{"action": "list"}`
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestSanitizeToolArguments_NoJSON(t *testing.T) {
	input := `completely invalid content`
	result := sanitizeToolArguments(input)
	if result != input {
		t.Errorf("expected original returned, got %q", result)
	}
}

// Fix 2: openai-compatible tool name validation.
// The strict regex ^[a-zA-Z0-9_-]+$ rejects the dotted MCP convention
// (device.list). Without this guard the request hits OpenAI and gets an
// opaque 400; with it, the engine surfaces a clear InvalidInput error
// naming the offending tool BEFORE any upstream call.

// stubInvokableTool implements tool.InvokableTool with a fixed name. Used to
// drive NewAgent's name-validation branch without standing up a real tool.
type stubInvokableTool struct {
	name string
}

func (s *stubInvokableTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: s.name, Desc: "fix2 validation stub"}, nil
}

// InvokableRun is unused — validation runs synchronously inside NewAgent before
// any tool ever executes — but BaseTool casting requires the method to exist.
func (s *stubInvokableTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einotool.Option) (string, error) {
	return "", nil
}

func TestNewAgent_Fix2_RejectsDottedToolNameForOpenAI(t *testing.T) {
	cases := []struct {
		name         string
		providerType string
		modelName    string
		baseURL      string
		toolName     string
	}{
		// openai provider type — always validate.
		{"openai (direct) rejects alarm.definition.create", "openai", "gpt-4o-mini", "https://api.openai.com/v1", "alarm.definition.create"},
		{"openai rejects names with space", "openai", "gpt-4o", "https://api.openai.com/v1", "get issue"},
		// openai_compatible + OpenAI base URL.
		{"openai_compatible at api.openai.com rejects device.list", "openai_compatible", "gpt-4o-mini", "https://api.openai.com/v1", "device.list"},
		{"openai_compatible at azure rejects device.list", "openai_compatible", "any", "https://my.openai.azure.com/v1", "device.list"},
		// openai_compatible + OpenRouter slug routing to OpenAI.
		{"openai_compatible + openai/ slug rejects device.list", "openai_compatible", "openai/gpt-4o-mini", "https://openrouter.ai/api/v1", "device.list"},
		{"openai_compatible + azure/ slug rejects device.list", "openai_compatible", "azure/gpt-4o", "https://openrouter.ai/api/v1", "device.list"},
		// openai_compatible + bare GPT prefix (operator points compatible driver direct at OpenAI without /v1 slug).
		{"openai_compatible + bare gpt-4o-mini rejects", "openai_compatible", "gpt-4o-mini", "", "device.list"},
		{"openai_compatible + bare o1 rejects", "openai_compatible", "o1-preview", "", "device.list"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewAgent(context.Background(), AgentConfig{
				ChatModel:       llm.NewMockChatModel("answer"),
				Tools:           []einotool.BaseTool{&stubInvokableTool{name: tc.toolName}},
				ProviderType:    tc.providerType,
				ModelName:       tc.modelName,
				ProviderBaseURL: tc.baseURL,
			})
			if err == nil {
				t.Fatalf("expected validation error for tool %q (provider %q, model %q, baseURL %q), got nil",
					tc.toolName, tc.providerType, tc.modelName, tc.baseURL)
			}
			var domainErr *pkgerrors.DomainError
			if !goerrors.As(err, &domainErr) || domainErr.Code != pkgerrors.CodeInvalidInput {
				t.Errorf("error must classify as InvalidInput, got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.toolName) {
				t.Errorf("error must name the offending tool %q, got: %v", tc.toolName, err)
			}
		})
	}
}

func TestNewAgent_Fix2_AcceptsValidToolNamesForOpenAI(t *testing.T) {
	cases := []string{"device_list", "device-list", "DeviceList42"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewAgent(context.Background(), AgentConfig{
				ChatModel:       llm.NewMockChatModel("answer"),
				Tools:           []einotool.BaseTool{&stubInvokableTool{name: name}},
				ProviderType:    "openai_compatible",
				ModelName:       "openai/gpt-4o-mini",
				ProviderBaseURL: "https://openrouter.ai/api/v1",
			})
			if err != nil {
				t.Fatalf("name %q should pass openai validation, got: %v", name, err)
			}
		})
	}
}

// TestNewAgent_Fix2_AllowsDottedNameWhenModelNotOpenAIRouted guards the key
// regression: providerType "openai_compatible" with a model NOT routed to
// OpenAI (qwen, glm, anthropic-via-OpenRouter, vLLM/llama.cpp local) MUST
// keep accepting dotted MCP tool names. base_url must also not match an
// OpenAI endpoint.
func TestNewAgent_Fix2_AllowsDottedNameWhenModelNotOpenAIRouted(t *testing.T) {
	cases := []struct {
		name      string
		modelName string
		baseURL   string
	}{
		{"qwen via OpenRouter", "qwen/qwen3-coder", "https://openrouter.ai/api/v1"},
		{"glm via OpenRouter", "z-ai/glm-4.7", "https://openrouter.ai/api/v1"},
		{"anthropic via OpenRouter", "anthropic/claude-haiku-4.5", "https://openrouter.ai/api/v1"},
		{"deepseek-coder bare", "deepseek-coder", "https://openrouter.ai/api/v1"},
		{"mistral local", "mistralai/Mixtral-8x7B", "http://vllm.svc:8000/v1"},
		{"empty all", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewAgent(context.Background(), AgentConfig{
				ChatModel:       llm.NewMockChatModel("answer"),
				Tools:           []einotool.BaseTool{&stubInvokableTool{name: "device.list"}},
				ProviderType:    "openai_compatible",
				ModelName:       tc.modelName,
				ProviderBaseURL: tc.baseURL,
			})
			if err != nil {
				t.Fatalf("openai_compatible + model %q + baseURL %q must tolerate dotted MCP tool names, got: %v",
					tc.modelName, tc.baseURL, err)
			}
		})
	}
}

func TestNewAgent_Fix2_AllowsDottedToolNameForNonOpenAIProviders(t *testing.T) {
	cases := []string{"anthropic", "ollama", "google", "azure_openai", ""}
	for _, provider := range cases {
		t.Run("provider="+provider, func(t *testing.T) {
			_, err := NewAgent(context.Background(), AgentConfig{
				ChatModel:       llm.NewMockChatModel("answer"),
				Tools:           []einotool.BaseTool{&stubInvokableTool{name: "device.list"}},
				ProviderType:    provider,
				ModelName:       "any-model",
				ProviderBaseURL: "https://example.com/v1",
			})
			if err != nil {
				t.Fatalf("provider %q must tolerate dotted MCP tool names, got: %v", provider, err)
			}
		})
	}
}

func TestNewAgent_Fix2_RejectsFirstInvalidToolName(t *testing.T) {
	_, err := NewAgent(context.Background(), AgentConfig{
		ChatModel: llm.NewMockChatModel("answer"),
		Tools: []einotool.BaseTool{
			&stubInvokableTool{name: "ok_tool"},
			&stubInvokableTool{name: "bad.tool"},
			&stubInvokableTool{name: "also.bad"},
		},
		ProviderType: "openai_compatible",
		ModelName:    "openai/gpt-4o-mini",
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "bad.tool") {
		t.Errorf("validation must name the first offending tool, got: %v", err)
	}
}
