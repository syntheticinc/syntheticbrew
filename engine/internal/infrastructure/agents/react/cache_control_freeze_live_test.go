//go:build live_llm

package react

import (
	"context"
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

// changingLiveReminder returns a DIFFERENT string on every call, mirroring the real
// EnvironmentContextReminder (time-to-minute) / TaskReminderContextProvider (live
// counts). Under freeze-by-source the modifier must snapshot its FIRST value and keep
// the trailing block byte-identical for the rest of the turn; without freezing each new
// value appends and collapses the explicit-cache prefix.
type changingLiveReminder struct{ n int }

func (c *changingLiveReminder) GetContextReminder(_ context.Context, _ string) (string, int, bool) {
	c.n++
	return fmt.Sprintf("**ENVIRONMENT:** tick %d — current time 15:%02d. %s",
		c.n, c.n, strings.Repeat("standing environment note for the agent. ", 20)), 95, true
}

// TestFreezeBySource_LiveQwen37Plus is the definitive LOCAL proof that the engine's
// freeze-by-source reminder accumulator makes the explicit-cache prefix GROW on the
// partner's actual model (qwen/qwen3.7-plus → Alibaba via OpenRouter), instead of
// collapsing every step. Self-contained RED→GREEN against the real provider:
//
//	GREEN — the real MessageModifier (freeze-by-source) snapshots the changing
//	        reminder once → trailing block byte-stable → cached_tokens GROWS.
//	RED   — the pre-fix shape (a changing reminder re-appended raw each step) →
//	        trailing block changes every step → cached_tokens collapses to ~0.
//
// Both arms run the SAME growing transcript through the SAME engine cache_control
// modifier; the only difference is freeze-by-source. Build-tagged live_llm (paid,
// ~$0.10); skips without the key.
//
//	SYNTHETICBREW_E2E_OPENROUTER_KEY=... go test -tags live_llm \
//	  -run TestFreezeBySource_LiveQwen37Plus -v ./internal/infrastructure/agents/react/
func TestFreezeBySource_LiveQwen37Plus(t *testing.T) {
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
		func(_ context.Context, _ []*schema.Message, raw []byte) ([]byte, error) { return mod(raw) },
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
			openaiext.WithExtraHeader(map[string]string{"x-session-id": "rit-freeze-live-001"}))
		require.NoError(t, gerr)
		require.NotNil(t, out.ResponseMeta)
		require.NotNil(t, out.ResponseMeta.Usage)
		return out.ResponseMeta.Usage.PromptTokens, out.ResponseMeta.Usage.PromptTokenDetails.CachedTokens
	}

	const steps = 6

	// GREEN arm: real MessageModifier with freeze-by-source over a changing reminder.
	green := NewMessageModifier(MessageModifierConfig{
		SystemPrompt:      head,
		SessionID:         "rit-freeze-live-001",
		ReminderProviders: []ContextReminderProvider{&changingLiveReminder{}},
	})
	green.StartTurn()
	greenTranscript := []*schema.Message{schema.UserMessage("Investigate device X1 fault thoroughly.")}
	var greenCached []int
	for s := 1; s <= steps; s++ {
		modified := green.Modify(context.Background(), greenTranscript)
		p, c := send(modified)
		greenCached = append(greenCached, c)
		t.Logf("GREEN step %d: prompt=%d cached=%d", s, p, c)
		greenTranscript = growStep(greenTranscript, s)
	}

	// RED arm: pre-fix shape — the changing reminder re-appended raw (new value) each step.
	redChanging := &changingLiveReminder{}
	redTranscript := []*schema.Message{schema.UserMessage("Investigate device X1 fault thoroughly.")}
	var redCached []int
	for s := 1; s <= steps; s++ {
		reminder, _, _ := redChanging.GetContextReminder(context.Background(), "")
		msgs := append([]*schema.Message{schema.SystemMessage(head)}, redTranscript...)
		msgs = append(msgs, schema.SystemMessage(reminder)) // changes every step → cache killer
		p, c := send(msgs)
		redCached = append(redCached, c)
		t.Logf("RED step %d: prompt=%d cached=%d", s, p, c)
		redTranscript = growStep(redTranscript, s)
	}

	t.Logf("GREEN cached-per-step = %v", greenCached)
	t.Logf("RED   cached-per-step = %v", redCached)

	maxLate := func(xs []int) int {
		m := 0
		for _, x := range xs[2:] { // step 3+ (after warm-up)
			if x > m {
				m = x
			}
		}
		return m
	}
	greenLate, redLate := maxLate(greenCached), maxLate(redCached)
	t.Logf("max-late cached: GREEN=%d RED=%d", greenLate, redLate)

	require.Greater(t, greenLate, 0,
		"freeze-by-source: the stable prefix must cache (cached_tokens grow) on qwen3.7-plus")
	require.Greater(t, greenLate, redLate*2,
		"freeze-by-source must cache substantially more than the pre-fix changing-tail shape "+
			"(GREEN grows with the conversation; RED collapses to ~head)")
}
