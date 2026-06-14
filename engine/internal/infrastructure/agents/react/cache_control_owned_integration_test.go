package react

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// TestOwnedGraph_CacheControlReachesChatNodeWire is the end-to-end routing proof:
// a REAL react.Agent (owned compose.Graph) runs a turn against a fake provider,
// with the prompt-cache modifier threaded the production way
// (compose.WithChatModelOption(...).DesignateNode("chat")). It asserts the
// modifier actually fired on the chat node's model call — i.e. the serialized
// request that left the engine carries cache_control on the stable prefix. This
// covers the one link the llm-package wire test cannot: DesignateNode routing
// through the owned graph.
func TestOwnedGraph_CacheControlReachesChatNodeWire(t *testing.T) {
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, b)
		w.Header().Set("Content-Type", "application/json")
		// A plain text answer, no tool calls → the owned graph routes to END.
		_, _ = w.Write([]byte(`{
			"id": "c1", "object": "chat.completion", "created": 1, "model": "test-model",
			"choices": [{"index":0,"message":{"role":"assistant","content":"Paris is the capital."},"finish_reason":"stop"}],
			"usage": {"prompt_tokens":1500,"completion_tokens":6,"total_tokens":1506,"prompt_tokens_details":{"cached_tokens":1200}}
		}`))
	}))
	defer srv.Close()

	client, err := llm.CreateClientFromDBModel(models.LLMProviderModel{
		Type:            "openai_compatible",
		BaseURL:         srv.URL,
		ModelName:       "test-model",
		APIKeyEncrypted: "test-key",
	})
	require.NoError(t, err)

	agent, err := NewAgent(context.Background(), AgentConfig{
		ChatModel:              client,
		Tools:                  nil,
		MaxSteps:               4,
		SessionID:              "cache-owned-session",
		AgentConfig:            charAgentConfig(nil),
		ModelName:              "test-model",
		ProviderType:           "openai_compatible",
		ProviderBaseURL:        srv.URL,
		RequestPayloadModifier: llm.NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true, MinPrefixTokens: 1}),
	})
	require.NoError(t, err)

	// Large user turn so the stable prefix clears the cache gate.
	input := "What is the capital of France? " + strings.Repeat("Please be thorough and precise. ", 60)
	answer, err := agent.RunWithCallbacks(context.Background(), input,
		func(*domain.AgentEvent) error { return nil })
	require.NoError(t, err)
	assert.Contains(t, answer, "Paris", "the turn must complete with the provider's answer")

	require.NotEmpty(t, bodies, "the chat node must have called the provider")
	marked := false
	for _, b := range bodies {
		if strings.Contains(string(b), `"cache_control"`) && strings.Contains(string(b), "ephemeral") {
			marked = true
		}
	}
	assert.True(t, marked,
		"the request leaving the owned-graph chat node must carry cache_control — proves DesignateNode routing + modifier wiring end-to-end")
}
