package react

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// dynamicTrailingReminder injects a per-call-changing system message at the tail,
// mirroring the engine's tool-call-history / environment reminders.
type dynamicTrailingReminder struct{ marker string }

func (d dynamicTrailingReminder) GetContextReminder(_ context.Context, _ string) (string, int, bool) {
	return d.marker + ": " + strings.Repeat("changing tool history state ", 40), 98, true
}

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

// TestOwnedGraph_HistoryBreakpointSkipsTrailingReminder is the regression guard for
// the cached_tokens-frozen-at-system bug: when the engine injects a dynamic trailing
// system reminder (tool-call history / environment), the history cache breakpoint
// must land on the last STABLE conversation message, NOT on the ever-changing
// reminder — otherwise that block is never re-read and only the static head caches.
func TestOwnedGraph_HistoryBreakpointSkipsTrailingReminder(t *testing.T) {
	const reminderMarker = "DYNAMIC-REMINDER-MARKER"
	var lastBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "c1", "object": "chat.completion", "created": 1, "model": "test-model",
			"choices": [{"index":0,"message":{"role":"assistant","content":"Answer."},"finish_reason":"stop"}],
			"usage": {"prompt_tokens":2000,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":1900}}
		}`))
	}))
	defer srv.Close()

	client, err := llm.CreateClientFromDBModel(models.LLMProviderModel{
		Type: "openai_compatible", BaseURL: srv.URL, ModelName: "test-model", APIKeyEncrypted: "test-key",
	})
	require.NoError(t, err)

	const userInputMarker = "STABLE-USER-TURN-MARKER"
	agent, err := NewAgent(context.Background(), AgentConfig{
		ChatModel:    client,
		MaxSteps:     4,
		SessionID:    "cache-trailing-session",
		AgentConfig:  charAgentConfig(nil),
		ModelName:    "test-model",
		ProviderType: "openai_compatible",
		HistoryMessages: []*schema.Message{
			{Role: schema.User, Content: "Earlier question. " + strings.Repeat("context ", 40)},
			{Role: schema.Assistant, Content: "Earlier answer. " + strings.Repeat("context ", 40)},
		},
		ContextReminderProviders: []ContextReminderProvider{dynamicTrailingReminder{marker: reminderMarker}},
		RequestPayloadModifier:   llm.NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true, MinPrefixTokens: 1}),
	})
	require.NoError(t, err)

	input := userInputMarker + " " + strings.Repeat("Please be thorough. ", 60)
	_, err = agent.RunWithCallbacks(context.Background(), input, func(*domain.AgentEvent) error { return nil })
	require.NoError(t, err)
	require.NotEmpty(t, lastBody, "the chat node must have called the provider")

	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(lastBody, &top))
	var msgs []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(top["messages"], &msgs))
	require.GreaterOrEqual(t, len(msgs), 4, "head + history + user + trailing reminder")

	hasCC := func(m map[string]json.RawMessage) bool {
		c, ok := m["content"]
		if !ok || jsonKindIsArray(c) == false {
			return false
		}
		return strings.Contains(string(c), "ephemeral")
	}
	contentStr := func(m map[string]json.RawMessage) string { return string(m["content"]) }

	last := msgs[len(msgs)-1]
	require.Contains(t, contentStr(last), reminderMarker, "the trailing message must be the dynamic reminder")
	assert.False(t, hasCC(last), "the dynamic trailing reminder must NOT carry the cache breakpoint")

	// head marked, and exactly one non-head message (the stable user turn) marked.
	assert.True(t, hasCC(msgs[0]), "head system message must be marked")
	markedUser := false
	for _, m := range msgs[1:] {
		if hasCC(m) {
			assert.Contains(t, contentStr(m), userInputMarker,
				"the history breakpoint must land on the stable user turn")
			markedUser = true
		}
	}
	assert.True(t, markedUser, "a stable conversation message must carry the history breakpoint")
}

// jsonKindIsArray reports whether a raw JSON value is an array (first non-space byte '[').
func jsonKindIsArray(raw json.RawMessage) bool {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '[':
			return true
		default:
			return false
		}
	}
	return false
}
