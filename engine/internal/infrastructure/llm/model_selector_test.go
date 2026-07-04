package llm

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockChatModel implements model.ToolCallingChatModel for testing
type mockChatModel struct {
	id string // used to distinguish between models in tests
}

func (m *mockChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return &schema.Message{Role: schema.Assistant, Content: m.id}, nil
}

func (m *mockChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
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

func TestModelSelector_Select(t *testing.T) {
	defaultModel := &mockChatModel{id: "default"}
	coderModel := &mockChatModel{id: "coder"}

	tests := []struct {
		name      string
		agentName string
		setup     func(s *ModelSelector)
		wantID    string
	}{
		{
			name:      "default returned when no override",
			agentName: "supervisor",
			setup:     func(s *ModelSelector) {},
			wantID:    "default",
		},
		{
			name:      "default returned for unregistered agent name",
			agentName: "reviewer",
			setup: func(s *ModelSelector) {
				s.SetModel("coder", coderModel, "coder-model")
			},
			wantID: "default",
		},
		{
			name:      "override returned when set",
			agentName: "coder",
			setup: func(s *ModelSelector) {
				s.SetModel("coder", coderModel, "coder-model")
			},
			wantID: "coder",
		},
		{
			name:      "each agent name gets its own model",
			agentName: "supervisor",
			setup: func(s *ModelSelector) {
				supervisorModel := &mockChatModel{id: "supervisor"}
				s.SetModel("supervisor", supervisorModel, "supervisor-model")
				s.SetModel("coder", coderModel, "coder-model")
			},
			wantID: "supervisor",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selector := NewModelSelector(defaultModel, "default-model")
			tt.setup(selector)

			got := selector.Select(tt.agentName)
			require.NotNil(t, got)

			// Verify by generating a response (mockChatModel returns its id as content)
			resp, err := got.Generate(context.Background(), nil)
			require.NoError(t, err)
			assert.Equal(t, tt.wantID, resp.Content)
		})
	}
}

func TestModelSelector_SetDefault(t *testing.T) {
	origDefault := &mockChatModel{id: "orig"}
	newDefault := &mockChatModel{id: "platform-free"}
	coderModel := &mockChatModel{id: "coder"}

	selector := NewModelSelector(origDefault, "orig-model")
	selector.SetModel("coder", coderModel, "coder-model")

	// SetDefault replaces the fallback used for unconfigured agents...
	selector.SetDefault(newDefault, "platform-free")

	resp, err := selector.Select("unknown-agent").Generate(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, "platform-free", resp.Content, "unconfigured agent should get the new default")
	assert.Equal(t, "platform-free", selector.ModelName("unknown-agent"))

	// ...but per-agent overrides still win over the default.
	resp, err = selector.Select("coder").Generate(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, "coder", resp.Content, "per-agent model must still take precedence over default")
}

func TestModelSelector_ModelName(t *testing.T) {
	defaultModel := &mockChatModel{id: "default"}
	coderModel := &mockChatModel{id: "coder"}

	tests := []struct {
		name      string
		agentName string
		setup     func(s *ModelSelector)
		want      string
	}{
		{
			name:      "default name returned when no override",
			agentName: "supervisor",
			setup:     func(s *ModelSelector) {},
			want:      "default-model",
		},
		{
			name:      "default name returned for unregistered agent name",
			agentName: "reviewer",
			setup: func(s *ModelSelector) {
				s.SetModel("coder", coderModel, "coder-model")
			},
			want: "default-model",
		},
		{
			name:      "override name returned when set",
			agentName: "coder",
			setup: func(s *ModelSelector) {
				s.SetModel("coder", coderModel, "coder-model")
			},
			want: "coder-model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selector := NewModelSelector(defaultModel, "default-model")
			tt.setup(selector)

			got := selector.ModelName(tt.agentName)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestModelSelector_NamedModels(t *testing.T) {
	defaultModel := &mockChatModel{id: "default"}

	t.Run("register and resolve named model", func(t *testing.T) {
		selector := NewModelSelector(defaultModel, "default-model")
		namedModel := &mockChatModel{id: "llama-4"}

		selector.RegisterNamedModel("llama-4", namedModel)

		got, err := selector.ResolveByName(context.Background(), "llama-4")
		require.NoError(t, err)
		require.NotNil(t, got)

		resp, err := got.Generate(context.Background(), nil)
		require.NoError(t, err)
		assert.Equal(t, "llama-4", resp.Content)
	})

	t.Run("resolve unknown name returns error", func(t *testing.T) {
		selector := NewModelSelector(defaultModel, "default-model")

		got, err := selector.ResolveByName(context.Background(), "nonexistent")
		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "nonexistent")
	})

	t.Run("multiple named models", func(t *testing.T) {
		selector := NewModelSelector(defaultModel, "default-model")
		model1 := &mockChatModel{id: "model-a"}
		model2 := &mockChatModel{id: "model-b"}

		selector.RegisterNamedModel("model-a", model1)
		selector.RegisterNamedModel("model-b", model2)

		assert.Equal(t, 2, selector.NamedModelCount())

		gotA, err := selector.ResolveByName(context.Background(), "model-a")
		require.NoError(t, err)
		respA, _ := gotA.Generate(context.Background(), nil)
		assert.Equal(t, "model-a", respA.Content)

		gotB, err := selector.ResolveByName(context.Background(), "model-b")
		require.NoError(t, err)
		respB, _ := gotB.Generate(context.Background(), nil)
		assert.Equal(t, "model-b", respB.Content)
	})

	t.Run("overwrite named model", func(t *testing.T) {
		selector := NewModelSelector(defaultModel, "default-model")
		original := &mockChatModel{id: "v1"}
		replacement := &mockChatModel{id: "v2"}

		selector.RegisterNamedModel("my-model", original)
		selector.RegisterNamedModel("my-model", replacement)

		got, err := selector.ResolveByName(context.Background(), "my-model")
		require.NoError(t, err)
		resp, _ := got.Generate(context.Background(), nil)
		assert.Equal(t, "v2", resp.Content)
	})

	t.Run("named model count initially zero", func(t *testing.T) {
		selector := NewModelSelector(defaultModel, "default-model")
		assert.Equal(t, 0, selector.NamedModelCount())
	})
}
