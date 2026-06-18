package react

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// TestOwnedGraph_AppendOnlyPrefixInvariant is the graph-level regression guard for the
// partner-critical cache fix. It runs the REAL owned ReAct graph end-to-end against a
// fake OpenAI-compatible provider, captures EVERY outgoing request body, and proves the
// loop sends a strict append-only extension of the previous step each round:
//
//  1. consecutive bodies are prefix-extensions — every message at an index that existed
//     in the prior body is BYTE-IDENTICAL in the next (no formed message is ever mutated);
//  2. every body has exactly ONE system message, at index 0 (the frozen head) — there is
//     no standalone mid-conversation system message that would re-render Qwen's chat
//     template and discard the explicit-cache prefix;
//  3. the loop-correction note rides INSIDE a tool message (never a system/user message).
//
// The agent is attached WITHOUT a RequestPayloadModifier so the bodies are clean (no
// cache_control array-canonicalization) and the loop's own append-only behaviour is
// isolated — exactly the property under test.
func TestOwnedGraph_AppendOnlyPrefixInvariant(t *testing.T) {
	var (
		mu       sync.Mutex
		bodies   [][]byte
		requests int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, b)
		requests++
		n := requests
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		// First three model calls emit the SAME tool call with IDENTICAL arguments, so
		// the identical-args loop breaker fires (default threshold 3) and folds a
		// correction note into the third round's tool result. The fourth call answers,
		// ending the turn before any budget wall.
		if n <= 3 {
			_, _ = w.Write([]byte(`{
				"id": "c1", "object": "chat.completion", "created": 1, "model": "test-model",
				"choices": [{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[
					{"id":"call_fixed","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"same\"}"}}
				]},"finish_reason":"tool_calls"}],
				"usage": {"prompt_tokens":100,"completion_tokens":4,"total_tokens":104}
			}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"id": "c1", "object": "chat.completion", "created": 1, "model": "test-model",
			"choices": [{"index":0,"message":{"role":"assistant","content":"Done."},"finish_reason":"stop"}],
			"usage": {"prompt_tokens":100,"completion_tokens":2,"total_tokens":102}
		}`))
	}))
	defer srv.Close()

	client, err := llm.CreateClientFromDBModel(models.LLMProviderModel{
		Type: "openai_compatible", BaseURL: srv.URL, ModelName: "test-model", APIKeyEncrypted: "test-key",
	})
	require.NoError(t, err)

	lookup := &charTool{name: "lookup", run: func(string) string { return `{"facts":["A","B","C"]}` }}

	agent, err := NewAgent(context.Background(), AgentConfig{
		ChatModel:    client,
		Tools:        []einotool.BaseTool{lookup},
		MaxSteps:     6,
		SessionID:    "append-only-session",
		AgentConfig:  charAgentConfig(nil),
		ModelName:    "test-model",
		ProviderType: "openai_compatible",
		// No RequestPayloadModifier: keep bodies clean so prefix-extension is byte-exact.
		RequestPayloadModifier: nil,
	})
	require.NoError(t, err)

	answer, err := agent.RunWithCallbacks(context.Background(), "Investigate thoroughly.",
		func(*domain.AgentEvent) error { return nil })
	require.NoError(t, err)
	assert.Contains(t, answer, "Done.", "the turn must complete with the provider's answer")

	mu.Lock()
	captured := make([][]byte, len(bodies))
	copy(captured, bodies)
	mu.Unlock()
	require.GreaterOrEqual(t, len(captured), 3,
		"the multi-round tool loop must have issued several model calls (got %d)", len(captured))

	type wireMsg struct {
		Role string          `json:"role"`
		raw  json.RawMessage // the whole message object, for byte comparison
	}
	parse := func(body []byte) []wireMsg {
		var top struct {
			Messages []json.RawMessage `json:"messages"`
		}
		require.NoError(t, json.Unmarshal(body, &top))
		out := make([]wireMsg, 0, len(top.Messages))
		for _, raw := range top.Messages {
			var hdr struct {
				Role string `json:"role"`
			}
			require.NoError(t, json.Unmarshal(raw, &hdr))
			out = append(out, wireMsg{Role: hdr.Role, raw: raw})
		}
		return out
	}

	var prev []wireMsg
	noteSeen := false
	for bi, body := range captured {
		msgs := parse(body)
		require.NotEmpty(t, msgs, "body %d has no messages", bi)

		// (2) exactly one system message, at index 0 (the frozen head). No standalone
		// mid-conversation system message.
		require.Equal(t, "system", msgs[0].Role, "body %d: index 0 must be the head system message", bi)
		systemCount := 0
		for _, m := range msgs {
			if m.Role == "system" {
				systemCount++
			}
		}
		require.Equal(t, 1, systemCount,
			"body %d: exactly one system message (the head) — no mid-conversation system message", bi)

		// (1) strict prefix-extension: every message present in the prior body is
		// byte-identical here, in the same position; the body only grows at the tail.
		if prev != nil {
			require.GreaterOrEqual(t, len(msgs), len(prev),
				"body %d: message count must not shrink (was %d, now %d)", bi, len(prev), len(msgs))
			for i := range prev {
				require.JSONEq(t, string(prev[i].raw), string(msgs[i].raw),
					"body %d: message at index %d must be byte-identical to the prior body (no mutation)", bi, i)
			}
		}
		prev = msgs

		// (3) the loop-correction note rides inside a tool message only — never the head
		// or a user message.
		for _, m := range msgs {
			if strings.Contains(string(m.raw), "ENGINE NOTICE") {
				require.Equal(t, "tool", m.Role,
					"body %d: the engine note must ride inside a tool message, not a %s", bi, m.Role)
				noteSeen = true
			}
		}
	}

	// The identical-args loop must have actually fired (note folded into a tool result),
	// otherwise invariant (3) is vacuously satisfied and proves nothing.
	require.True(t, noteSeen,
		"the loop-correction note must have been folded into a tool result during the identical-args loop")
}
