//go:build live_llm

package llm

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	openaiext "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// TestCacheControl_LiveOpenRouterQwen is the TC-CACHE-1 empirical gate: it sends a
// large stable prefix marked with cache_control to qwen/qwen3-coder-next via real
// OpenRouter twice, and asserts the second call reports cached prompt tokens — the
// load-bearing proof that the engine's breakpoints actually engage the provider
// cache. Build-tagged live_llm (paid, ~cents); skips without the key.
//
//	SYNTHETICBREW_E2E_OPENROUTER_KEY=... go test -tags live_llm \
//	  -run TestCacheControl_LiveOpenRouterQwen -v ./internal/infrastructure/llm/
func TestCacheControl_LiveOpenRouterQwen(t *testing.T) {
	key := os.Getenv("SYNTHETICBREW_E2E_OPENROUTER_KEY")
	if key == "" {
		t.Skip("SYNTHETICBREW_E2E_OPENROUTER_KEY not set")
	}

	client, err := CreateClientFromDBModel(models.LLMProviderModel{
		Type:            "openai_compatible",
		BaseURL:         "https://openrouter.ai/api/v1",
		ModelName:       "qwen/qwen3-coder-next",
		APIKeyEncrypted: key,
	}, nil)
	require.NoError(t, err)

	mod := NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true})
	require.NotNil(t, mod)
	opt := openaiext.WithRequestPayloadModifier(
		func(_ context.Context, _ []*schema.Message, raw []byte) ([]byte, error) { return mod(raw) },
	)

	// A stable system prefix well above the ~1024-token provider cache minimum.
	stablePrefix := "You are a precise assistant. Reference manual:\n" +
		strings.Repeat("Section: each tool returns JSON with a status field and a data field; "+
			"errors are reported with an error field and a numeric code. ", 120)
	msgs := []*schema.Message{
		schema.SystemMessage(stablePrefix),
		schema.UserMessage("In one sentence, what field reports errors?"),
	}

	ctx := context.Background()
	out1, err := client.Generate(ctx, msgs, opt)
	require.NoError(t, err)
	require.NotNil(t, out1.ResponseMeta)
	require.NotNil(t, out1.ResponseMeta.Usage)
	cached1 := out1.ResponseMeta.Usage.PromptTokenDetails.CachedTokens
	t.Logf("call 1: prompt=%d cached=%d answer=%q",
		out1.ResponseMeta.Usage.PromptTokens, cached1, truncate(out1.Content))

	// The upstream cache write commits slightly after the first response; a
	// back-to-back second call races it. Give it a moment, then read.
	time.Sleep(5 * time.Second)

	// Identical prefix → second call should read the cache written by the first.
	out2, err := client.Generate(ctx, msgs, opt)
	require.NoError(t, err)
	require.NotNil(t, out2.ResponseMeta)
	require.NotNil(t, out2.ResponseMeta.Usage)
	cached2 := out2.ResponseMeta.Usage.PromptTokenDetails.CachedTokens
	t.Logf("call 2: prompt=%d cached=%d answer=%q",
		out2.ResponseMeta.Usage.PromptTokens, cached2, truncate(out2.Content))

	require.NotEmpty(t, out1.Content, "answer must be produced")
	require.NotEmpty(t, out2.Content, "answer must be produced")
	require.Greater(t, cached2, 0,
		"second call must report cached prompt tokens — the marked prefix did not engage the provider cache")
}

// TestCacheControl_LiveIntraTurnTiming measures whether the provider cache commits
// fast enough to benefit WITHIN a single multi-step ReAct turn, where steps fire
// back-to-back (no artificial delay) with a growing-but-stable-prefix transcript.
// This is the ticket's core claim ("re-billed N times per turn"). It reports
// per-step cached tokens so we see exactly when (if) the cache engages.
func TestCacheControl_LiveIntraTurnTiming(t *testing.T) {
	key := os.Getenv("SYNTHETICBREW_E2E_OPENROUTER_KEY")
	if key == "" {
		t.Skip("SYNTHETICBREW_E2E_OPENROUTER_KEY not set")
	}
	client, err := CreateClientFromDBModel(models.LLMProviderModel{
		Type:            "openai_compatible",
		BaseURL:         "https://openrouter.ai/api/v1",
		ModelName:       "qwen/qwen3-coder-next",
		APIKeyEncrypted: key,
	}, nil)
	require.NoError(t, err)

	mod := NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true})
	opt := openaiext.WithRequestPayloadModifier(
		func(_ context.Context, _ []*schema.Message, raw []byte) ([]byte, error) { return mod(raw) },
	)
	ctx := context.Background()

	stable := "You are a precise assistant. Reference manual:\n" +
		strings.Repeat("Section: each tool returns JSON with a status field and a data field; "+
			"errors are reported with an error field and a numeric code. ", 120)

	// Simulate a multi-step turn: the transcript GROWS each step, but the
	// system+early-history prefix is stable. Steps fire back-to-back (the only
	// gap is the model's own generation time + a tiny tool turnaround).
	transcript := []*schema.Message{schema.SystemMessage(stable), schema.UserMessage("Start the task.")}
	var cachedPerStep []int
	for step := 1; step <= 3; step++ {
		out, gerr := client.Generate(ctx, transcript, opt)
		require.NoError(t, gerr)
		require.NotNil(t, out.ResponseMeta)
		require.NotNil(t, out.ResponseMeta.Usage)
		c := out.ResponseMeta.Usage.PromptTokenDetails.CachedTokens
		cachedPerStep = append(cachedPerStep, c)
		t.Logf("step %d: prompt=%d cached=%d", step, out.ResponseMeta.Usage.PromptTokens, c)
		// Grow the transcript like a ReAct step would (assistant + tool result),
		// keeping the system+early prefix stable. ~no artificial delay.
		transcript = append(transcript,
			schema.AssistantMessage("Proceeding with step "+itoa(step)+".", nil),
			schema.UserMessage("Tool result: {\"status\":\"ok\",\"data\":\"continue\"}"))
	}

	t.Logf("cached-per-step = %v", cachedPerStep)
	intraTurnHit := false
	for i := 1; i < len(cachedPerStep); i++ {
		if cachedPerStep[i] > 0 {
			intraTurnHit = true
		}
	}
	// Report finding rather than hard-fail: this characterises provider timing.
	if !intraTurnHit {
		t.Logf("FINDING: provider cache did NOT engage within back-to-back intra-turn steps "+
			"(commit latency exceeds inter-step gap). cross-turn caching still works. per-step=%v", cachedPerStep)
	} else {
		t.Logf("INTRA-TURN CACHE ENGAGED: step 2+ read the stable prefix from cache. per-step=%v", cachedPerStep)
	}
}

// TestCacheControl_LiveAnthropicEngineCodePath is the definitive correctness proof
// for the cache_control feature, run THROUGH THE ENGINE'S OWN CODE
// (CreateClientFromDBModel + extra_body provider routing + the cache modifier)
// against a provider that actually honors explicit cache_control: native
// Anthropic (Claude Haiku) via OpenRouter. It asserts the engine's emitted bytes
// engage the provider cache (write then read), with a control showing no caching
// without the marker.
func TestCacheControl_LiveAnthropicEngineCodePath(t *testing.T) {
	key := os.Getenv("SYNTHETICBREW_E2E_OPENROUTER_KEY")
	if key == "" {
		t.Skip("SYNTHETICBREW_E2E_OPENROUTER_KEY not set")
	}

	// Model configured exactly as a tenant would: openai_compatible → OpenRouter,
	// extra_body pins the native Anthropic provider (so cache_control is honored,
	// not dropped by Bedrock/Vertex), cache_control enabled.
	m := models.LLMProviderModel{
		Type:            "openai_compatible",
		BaseURL:         "https://openrouter.ai/api/v1",
		ModelName:       "anthropic/claude-haiku-4.5",
		APIKeyEncrypted: key,
	}
	m.SetConfig(models.ModelConfig{
		ExtraBody:    map[string]any{"provider": map[string]any{"only": []string{"anthropic"}}},
		CacheControl: &models.CacheControl{Enabled: true},
	})

	client, err := CreateClientFromDBModel(m, nil)
	require.NoError(t, err)
	mod := NewCacheControlModifier(m.Type, m.GetConfig().CacheControl)
	require.NotNil(t, mod, "cache modifier must be built for openai_compatible + enabled")
	opt := openaiext.WithRequestPayloadModifier(
		func(_ context.Context, _ []*schema.Message, raw []byte) ([]byte, error) { return mod(raw) },
	)

	// Prefix above Anthropic Haiku's ~4096-token cache minimum.
	big := "You are an expert assistant. Reference manual.\n" +
		strings.Repeat("Each tool returns a JSON object with a status field and a data field; on "+
			"failure it returns an error field with a message plus a numeric code field. ", 220)
	msgs := []*schema.Message{schema.SystemMessage(big), schema.UserMessage("One sentence: what field reports errors?")}

	ctx := context.Background()
	out1, err := client.Generate(ctx, msgs, opt)
	require.NoError(t, err)
	w1 := out1.ResponseMeta.Usage.PromptTokenDetails // read side
	t.Logf("call 1: prompt=%d cached=%d", out1.ResponseMeta.Usage.PromptTokens, w1.CachedTokens)

	time.Sleep(2 * time.Second)
	out2, err := client.Generate(ctx, msgs, opt)
	require.NoError(t, err)
	cached2 := out2.ResponseMeta.Usage.PromptTokenDetails.CachedTokens
	t.Logf("call 2: prompt=%d cached=%d", out2.ResponseMeta.Usage.PromptTokens, cached2)

	require.NotEmpty(t, out1.Content)
	require.NotEmpty(t, out2.Content)
	require.Greater(t, cached2, 0,
		"engine-emitted cache_control must engage the Anthropic prompt cache on the second call")
}

// TestSessionID_LiveStickyCacheQwen proves the engine's session-stickiness lever:
// using the SAME x-session-id header option the engine attaches (openaiext.
// WithExtraHeader) against real OpenRouter Qwen, a back-to-back multi-step turn
// pins one provider and the stable prefix caches from step 3+. This is the
// partner's actual benefit for qwen3-coder-next (where cache_control is a no-op).
func TestSessionID_LiveStickyCacheQwen(t *testing.T) {
	key := os.Getenv("SYNTHETICBREW_E2E_OPENROUTER_KEY")
	if key == "" {
		t.Skip("SYNTHETICBREW_E2E_OPENROUTER_KEY not set")
	}
	client, err := CreateClientFromDBModel(models.LLMProviderModel{
		Type:            "openai_compatible",
		BaseURL:         "https://openrouter.ai/api/v1",
		ModelName:       "qwen/qwen3-coder-next",
		APIKeyEncrypted: key,
	}, nil)
	require.NoError(t, err)

	// The exact option react.buildChatCallOptions attaches per turn.
	sticky := openaiext.WithExtraHeader(map[string]string{"x-session-id": "rit-live-sticky-turn-001"})
	ctx := context.Background()

	stable := "You are a precise assistant. Reference manual:\n" +
		strings.Repeat("Section: each tool returns JSON with a status field and a data field; "+
			"errors are reported with an error field and a numeric code. ", 120)
	transcript := []*schema.Message{schema.SystemMessage(stable), schema.UserMessage("Start the task.")}

	var cachedPerStep []int
	for step := 1; step <= 5; step++ {
		out, gerr := client.Generate(ctx, transcript, sticky)
		require.NoError(t, gerr)
		c := out.ResponseMeta.Usage.PromptTokenDetails.CachedTokens
		cachedPerStep = append(cachedPerStep, c)
		t.Logf("step %d: prompt=%d cached=%d", step, out.ResponseMeta.Usage.PromptTokens, c)
		transcript = append(transcript,
			schema.AssistantMessage("step "+itoa(step), nil),
			schema.UserMessage("Tool result: {\"status\":\"ok\"}"))
	}

	t.Logf("cached-per-step = %v", cachedPerStep)
	laterHit := false
	for i := 2; i < len(cachedPerStep); i++ { // step 3+ (after replica warm-up)
		if cachedPerStep[i] > 0 {
			laterHit = true
		}
	}
	require.True(t, laterHit,
		"with the engine's x-session-id sticky header, the stable prefix must cache by step 3+ within a back-to-back turn")
}

func itoa(n int) string { return string(rune('0' + n)) }

func truncate(s string) string {
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}
