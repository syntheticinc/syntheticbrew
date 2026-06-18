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

// headFoldedReminder supplies reminder content the modifier folds into the FROZEN HEAD
// (out[0]) once per turn. It carries a marker so a test can prove the reminder lands in
// the head system message, not as a separate trailing message.
type headFoldedReminder struct{ marker string }

func (d headFoldedReminder) GetContextReminder(_ context.Context, _ string) (string, int, bool) {
	return d.marker + ": " + strings.Repeat("stable standing tool guidance ", 40), 98, true
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

// TestOwnedGraph_CacheBreakpointOnHeadAndCacheableTail verifies the two cache breakpoints
// land on the FROZEN HEAD and the LAST natural (cacheable) message. The MessageModifier
// folds every reminder into the byte-frozen head and appends only natural user/assistant/
// tool turns after it, never rewriting earlier ones — so the head is the stable front
// breakpoint and the last natural message is the byte-stable tail breakpoint. The
// reminder marker therefore appears INSIDE the head, not as a separate trailing message.
func TestOwnedGraph_CacheBreakpointOnHeadAndCacheableTail(t *testing.T) {
	const reminderMarker = "HEAD-FOLDED-REMINDER-MARKER"
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
		ContextReminderProviders: []ContextReminderProvider{headFoldedReminder{marker: reminderMarker}},
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
	require.GreaterOrEqual(t, len(msgs), 4, "head + history + user turn")

	hasCC := func(m map[string]json.RawMessage) bool {
		c, ok := m["content"]
		if !ok || jsonKindIsArray(c) == false {
			return false
		}
		return strings.Contains(string(c), "ephemeral")
	}
	contentStr := func(m map[string]json.RawMessage) string { return string(m["content"]) }

	// The reminder is folded into the head system message, not a separate trailing message.
	head := msgs[0]
	require.Equal(t, "system", messageRoleOf(head), "msgs[0] must be the frozen head")
	require.Contains(t, contentStr(head), reminderMarker, "the reminder must be folded into the head, not trailed")
	assert.True(t, hasCC(head), "head system message must carry a cache breakpoint")

	// The last natural (cacheable) message is the user turn — the byte-stable tail
	// breakpoint. It is NOT a reminder; the reminder lives in the head.
	last := msgs[len(msgs)-1]
	require.Contains(t, contentStr(last), userInputMarker, "the tail must be the natural user turn, not a reminder")
	assert.True(t, hasCC(last), "the last cacheable message must carry the tail cache breakpoint")

	// Only head + tail are breakpoints; interior history is read from cache, not marked.
	for i, m := range msgs[1 : len(msgs)-1] {
		assert.False(t, hasCC(m), "only head and tail are breakpoints; interior message %d must not be marked", i+1)
	}
}

// messageRoleOf extracts the JSON "role" field from a decoded message object.
func messageRoleOf(m map[string]json.RawMessage) string {
	raw, ok := m["role"]
	if !ok {
		return ""
	}
	var role string
	if json.Unmarshal(raw, &role) != nil {
		return ""
	}
	return role
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
