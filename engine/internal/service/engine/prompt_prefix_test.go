package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/pkg/config"
)

// fakePromptPrefixProvider returns a fixed prefix and records how many times it
// was consulted.
type fakePromptPrefixProvider struct {
	prefix string
	calls  int
}

func (f *fakePromptPrefixProvider) PromptPrefix(ctx context.Context) string {
	f.calls++
	return f.prefix
}

func TestWithPromptPrefix(t *testing.T) {
	t.Run("prepends with blank line", func(t *testing.T) {
		cfg := &config.AgentConfig{Prompts: &config.PromptsConfig{SystemPrompt: "base prompt"}}
		got := withPromptPrefix(cfg, "PREFIX")
		require.NotNil(t, got.Prompts)
		assert.Equal(t, "PREFIX\n\nbase prompt", got.Prompts.SystemPrompt)
	})

	t.Run("empty existing prompt yields prefix alone", func(t *testing.T) {
		cfg := &config.AgentConfig{Prompts: &config.PromptsConfig{SystemPrompt: ""}}
		got := withPromptPrefix(cfg, "PREFIX")
		require.NotNil(t, got.Prompts)
		assert.Equal(t, "PREFIX", got.Prompts.SystemPrompt)
	})

	t.Run("nil prompts handled", func(t *testing.T) {
		cfg := &config.AgentConfig{Prompts: nil}
		got := withPromptPrefix(cfg, "PREFIX")
		require.NotNil(t, got.Prompts)
		assert.Equal(t, "PREFIX", got.Prompts.SystemPrompt)
	})

	t.Run("original cfg and its Prompts are unchanged (aliasing guard)", func(t *testing.T) {
		sharedPrompts := &config.PromptsConfig{SystemPrompt: "base prompt"}
		cfg := &config.AgentConfig{Prompts: sharedPrompts}

		got := withPromptPrefix(cfg, "PREFIX")

		// The original struct must not observe the prefix.
		assert.Equal(t, "base prompt", cfg.Prompts.SystemPrompt,
			"original Prompts must not be mutated — it may alias a shared config")
		assert.Same(t, sharedPrompts, cfg.Prompts, "original Prompts pointer must be untouched")
		assert.NotSame(t, sharedPrompts, got.Prompts, "result must carry a copied Prompts")
	})
}

// TestEngine_Execute_AppliesPromptPrefix drives Execute with a capturing model
// and asserts the deployment prefix reaches the system prompt only for
// top-level executions.
func TestEngine_Execute_AppliesPromptPrefix(t *testing.T) {
	newEngine := func(provider PromptPrefixProvider) (*Engine, *capturingChatModel) {
		eng := New(newMockSnapshotRepo(), newMockHistoryRepo())
		if provider != nil {
			eng.SetPromptPrefixProvider(provider)
		}
		return eng, &capturingChatModel{}
	}

	t.Run("top-level execution gets the prefix", func(t *testing.T) {
		eng, model := newEngine(&fakePromptPrefixProvider{prefix: "DEPLOY PREFIX"})
		flow := testFlow()
		flow.SystemPrompt = "You are a test agent"

		cfg := ExecutionConfig{
			SessionID:   "session-1",
			AgentID:     "supervisor",
			Flow:        flow,
			ChatModel:   model,
			Input:       "Hello",
			AgentConfig: &config.AgentConfig{Prompts: &config.PromptsConfig{SystemPrompt: "You are a test agent"}},
		}
		_, err := eng.Execute(context.Background(), cfg)
		require.NoError(t, err)

		assert.Contains(t, model.lastInputText(), "DEPLOY PREFIX",
			"top-level execution must see the deployment prefix in its system prompt")
	})

	t.Run("sub-agent execution is untouched", func(t *testing.T) {
		eng, model := newEngine(&fakePromptPrefixProvider{prefix: "DEPLOY PREFIX"})
		flow := testFlow()
		flow.SystemPrompt = "You are a worker"

		cfg := ExecutionConfig{
			SessionID:     "session-1",
			AgentID:       "worker",
			ParentAgentID: "supervisor", // marks a spawned sub-agent
			Flow:          flow,
			ChatModel:     model,
			Input:         "Do work",
			AgentConfig:   &config.AgentConfig{Prompts: &config.PromptsConfig{SystemPrompt: "You are a worker"}},
		}
		_, err := eng.Execute(context.Background(), cfg)
		require.NoError(t, err)

		assert.NotContains(t, model.lastInputText(), "DEPLOY PREFIX",
			"sub-agent execution must not receive the deployment prefix")
	})

	t.Run("nil provider is a no-op", func(t *testing.T) {
		eng, model := newEngine(nil)
		flow := testFlow()
		flow.SystemPrompt = "You are a test agent"

		cfg := ExecutionConfig{
			SessionID:   "session-1",
			AgentID:     "supervisor",
			Flow:        flow,
			ChatModel:   model,
			Input:       "Hello",
			AgentConfig: &config.AgentConfig{Prompts: &config.PromptsConfig{SystemPrompt: "You are a test agent"}},
		}
		_, err := eng.Execute(context.Background(), cfg)
		require.NoError(t, err)

		assert.NotContains(t, model.lastInputText(), "DEPLOY PREFIX")
	})
}
