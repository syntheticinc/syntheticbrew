//go:build live_llm

package react

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	openaiext "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// changingCountLiveReminder returns a DIFFERENT string on every call, mirroring a live
// countdown ("Only N left") or env-time reminder. Under append-increment the modifier
// appends a NEW reminder for each changed value, interleaved so prior content keeps its
// position — the explicit-cache prefix GROWS while the live count still changes per step.
type changingCountLiveReminder struct{ n int }

func (c *changingCountLiveReminder) GetContextReminder(_ context.Context, _ string) (string, int, bool) {
	c.n++
	return fmt.Sprintf("**COUNTDOWN:** tick %d — only %d steps left. %s",
		c.n, c.n, strings.Repeat("standing environment note for the agent. ", 20)), 95, true
}

// TestAppendIncrement_LiveQwen37Plus is the definitive LOCAL proof that the engine's
// append-increment reminder injection makes the explicit-cache prefix GROW on the
// partner's actual model (qwen/qwen3.7-plus → Alibaba via OpenRouter) WHILE keeping the
// live, per-step-changing count. The real MessageModifier appends a fresh reminder each
// step (changing value) but interleaves it at the input tail, so each request stays a
// strict prefix of the next → cached_tokens grow. Build-tagged live_llm (paid, ~$0.10);
// skips without the key.
//
//	SYNTHETICBREW_E2E_OPENROUTER_KEY=... go test -tags live_llm \
//	  -run TestAppendIncrement_LiveQwen37Plus -v ./internal/infrastructure/agents/react/
func TestAppendIncrement_LiveQwen37Plus(t *testing.T) {
	key := os.Getenv("SYNTHETICBREW_E2E_OPENROUTER_KEY")
	if key == "" {
		t.Skip("SYNTHETICBREW_E2E_OPENROUTER_KEY not set")
	}

	client, err := llm.CreateClientFromDBModel(models.LLMProviderModel{
		Type:            "openai_compatible",
		BaseURL:         "https://openrouter.ai/api/v1",
		ModelName:       "qwen/qwen3.7-plus",
		APIKeyEncrypted: key,
	})
	require.NoError(t, err)

	mod := llm.NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true, MinPrefixTokens: 1})
	require.NotNil(t, mod)
	cacheOpt := openaiext.WithRequestPayloadModifier(
		func(_ context.Context, _ []*schema.Message, raw []byte) ([]byte, error) {
			b, err := mod(raw)
			if err != nil {
				return nil, err
			}
			// OpenRouter only reports prompt_tokens_details.cached_tokens when usage.include
			// is set on the body. Without it caching still happens provider-side but cached=0
			// in the response — inject it so this test can observe the cache.
			var top map[string]json.RawMessage
			if json.Unmarshal(b, &top) == nil {
				top["usage"] = json.RawMessage(`{"include":true}`)
				if nb, e := json.Marshal(top); e == nil {
					return nb, nil
				}
			}
			return b, nil
		},
	)

	// A big stable system head, clearing the provider cache minimum.
	head := "You are an enterprise IoT support agent. Standing policy:\n" +
		strings.Repeat("Each tool returns JSON with a status and a data field; errors carry an error "+
			"field and a numeric code. Investigate carefully and cite device ids. ", 90)

	// A frozen base conversation that GROWS one tool exchange per step (stable prefix).
	growStep := func(transcript []*schema.Message, step int) []*schema.Message {
		return append(transcript,
			schema.AssistantMessage(fmt.Sprintf("Checking source %d.", step), nil),
			schema.UserMessage(fmt.Sprintf("Tool result %d: {\"status\":\"ok\",\"data\":\"log line %d, %s\"}",
				step, step, strings.Repeat("payload ", 10))))
	}

	send := func(msgs []*schema.Message) (prompt, cached int) {
		out, gerr := client.Generate(context.Background(), msgs, cacheOpt,
			openaiext.WithExtraHeader(map[string]string{"x-session-id": "rit-append-increment-001"}))
		require.NoError(t, gerr)
		require.NotNil(t, out.ResponseMeta)
		require.NotNil(t, out.ResponseMeta.Usage)
		return out.ResponseMeta.Usage.PromptTokens, out.ResponseMeta.Usage.PromptTokenDetails.CachedTokens
	}

	const steps = 6

	// The real MessageModifier with append-increment over a changing-count reminder.
	mm := NewMessageModifier(MessageModifierConfig{
		SystemPrompt:      head,
		SessionID:         "rit-append-increment-001",
		ReminderProviders: []ContextReminderProvider{&changingCountLiveReminder{}},
	})
	mm.StartTurn()
	transcript := []*schema.Message{schema.UserMessage("Investigate device X1 fault thoroughly.")}

	var cachedPerStep []int
	seenValues := map[string]bool{}
	for s := 1; s <= steps; s++ {
		modified := mm.Modify(context.Background(), transcript)

		// Prove the LIVE value changes: the newest reminder must carry "tick s".
		var newest string
		for _, msg := range modified {
			if msg.Role == schema.System && strings.Contains(msg.Content, "**COUNTDOWN:**") {
				newest = msg.Content
			}
		}
		require.Contains(t, newest, fmt.Sprintf("tick %d", s),
			"the live countdown reminder must change per step (step %d)", s)
		require.False(t, seenValues[newest], "each live value must be distinct (step %d)", s)
		seenValues[newest] = true

		p, c := send(modified)
		cachedPerStep = append(cachedPerStep, c)
		t.Logf("step %d: prompt=%d cached=%d", s, p, c)
		transcript = growStep(transcript, s)
	}

	t.Logf("cached-per-step = %v", cachedPerStep)

	maxLate := func(xs []int) int {
		m := 0
		for _, x := range xs[2:] { // step 3+ (after warm-up)
			if x > m {
				m = x
			}
		}
		return m
	}
	require.Greater(t, maxLate(cachedPerStep), 0,
		"append-increment: the growing stable prefix must cache (cached_tokens grow) on qwen3.7-plus")
	require.Greater(t, cachedPerStep[steps-1], cachedPerStep[1],
		"cached_tokens must GROW across steps as the interleaved prefix extends (not collapse)")
}
