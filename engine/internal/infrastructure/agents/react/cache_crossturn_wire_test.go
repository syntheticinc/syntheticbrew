package react

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
)

// TestOwnedGraph_StableHeadByteIdenticalAcrossTurns proves the cross-turn cache fix
// through the FULL engine wire path (real owned graph + real eino serialization + real
// cache_control modifier), not a hand-built body. It runs TWO separate agent runs (two
// conversation turns with DIFFERENT user questions) against a capturing fake provider and
// asserts the cache-marked STABLE head (msgs[0]) is byte-identical across the two turns —
// the precise condition the provider needs to reuse its cache. Since the fix, the volatile
// head (CURRENT TASK) sits at the TAIL (the last message), so the append-only history stays
// in the byte-stable cacheable prefix; it differs between the turns (different questions).
//
// It also writes the two real wire bodies to _scratch so a live run can feed them straight
// to OpenRouter and confirm the actual cache hit on real serialization.
func TestOwnedGraph_StableHeadByteIdenticalAcrossTurns(t *testing.T) {
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","object":"chat.completion","created":1,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"Answer."},"finish_reason":"stop"}],"usage":{"prompt_tokens":2000,"completion_tokens":2}}`))
	}))
	defer srv.Close()

	client, err := llm.CreateClientFromDBModel(models.LLMProviderModel{
		Type: "openai_compatible", BaseURL: srv.URL, ModelName: "test-model", APIKeyEncrypted: "test-key",
	})
	require.NoError(t, err)

	// A large, turn-invariant system prompt so the stable head clears any provider cache
	// minimum when the dumped bodies are replayed live.
	bigSys := "You are a device-onboarding assistant. " + strings.Repeat("Follow the provisioning protocols carefully. ", 200)
	ccMod := llm.NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true, MinPrefixTokens: 1})
	require.NotNil(t, ccMod)

	runTurn := func(history []*schema.Message, userInput string) []byte {
		bodies = nil
		agent, aerr := NewAgent(context.Background(), AgentConfig{
			ChatModel:              client,
			MaxSteps:               4,
			SessionID:              "crossturn-wire-session",
			AgentConfig:            charAgentConfig(func(c *config.AgentConfig) { c.Prompts.SystemPrompt = bigSys }),
			ModelName:              "test-model",
			ProviderType:           "openai_compatible",
			HistoryMessages:        history,
			RequestPayloadModifier: ccMod,
		})
		require.NoError(t, aerr)
		_, rerr := agent.RunWithCallbacks(context.Background(), userInput, func(*domain.AgentEvent) error { return nil })
		require.NoError(t, rerr)
		require.NotEmpty(t, bodies, "the chat node must have called the provider")
		return bodies[0]
	}

	// Turn 1: question ALPHA (cold).
	body1 := runTurn(nil, "Connect device ALPHA")
	// Turn 2: a DIFFERENT question BETA, with the prior turn in history (real multi-turn).
	body2 := runTurn([]*schema.Message{
		{Role: schema.User, Content: "Connect device ALPHA"},
		{Role: schema.Assistant, Content: "Answer."},
	}, "Connect device BETA, a different question entirely")

	stableHead := func(body []byte) (content string, all []map[string]json.RawMessage) {
		var top map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(body, &top))
		var msgs []map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(top["messages"], &msgs))
		require.GreaterOrEqual(t, len(msgs), 2, "stable head + conversation + trailing volatile head")
		require.Equal(t, "system", messageRoleOf(msgs[0]), "msgs[0] must be the stable head")
		return string(msgs[0]["content"]), msgs
	}

	head1, msgs1 := stableHead(body1)
	head2, msgs2 := stableHead(body2)

	// THE cross-turn cache condition: the cache-marked stable head is byte-identical
	// across turns, on the REAL serialized wire bytes.
	require.Equal(t, head1, head2,
		"the cache-marked stable head (msgs[0]) must be byte-identical across turns on the real wire")
	require.Contains(t, head1, "ephemeral", "the stable head must carry the cache_control breakpoint")
	require.NotContains(t, head1, "CURRENT TASK", "the stable head must not carry the per-turn task focus")

	// Since the fix, the volatile head (CURRENT TASK) is the LAST message, after the whole
	// conversation. Sanity: the trailing volatile heads actually differ between the two turns
	// — proves the turns really varied (different CURRENT TASK), so the byte-identity above is
	// real. The volatile head at the tail keeps the append-only history in the cacheable prefix.
	vol1 := msgs1[len(msgs1)-1]
	vol2 := msgs2[len(msgs2)-1]
	require.Equal(t, "system", messageRoleOf(vol1), "the trailing message must be the volatile head")
	require.Equal(t, "system", messageRoleOf(vol2), "the trailing message must be the volatile head")
	require.Contains(t, string(vol1["content"]), "CURRENT TASK", "the trailing volatile head carries the per-turn task focus")
	require.NotEqual(t, string(vol1["content"]), string(vol2["content"]),
		"the trailing volatile head must differ between the two questions (otherwise the test didn't vary the turn)")

	// No system message may appear STRICTLY BETWEEN the stable head and the trailing volatile
	// head — the conversation there is user/assistant/tool only (a mid-conversation system
	// message would re-render Qwen's chat template and discard the cache prefix).
	for i := 1; i < len(msgs1)-1; i++ {
		require.NotEqual(t, "system", messageRoleOf(msgs1[i]),
			"turn 1: no system message may appear between the stable head and the trailing volatile head (index %d)", i)
	}

	// Dump the real wire bodies so a live run can replay them against OpenRouter.
	if dir := os.Getenv("CROSSTURN_DUMP_DIR"); dir != "" {
		_ = os.WriteFile(dir+"/crossturn_wire_body1.json", body1, 0o644)
		_ = os.WriteFile(dir+"/crossturn_wire_body2.json", body2, 0o644)
	}
}
