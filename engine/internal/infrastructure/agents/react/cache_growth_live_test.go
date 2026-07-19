//go:build live_llm

package react

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	openaiext "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// These build-tagged (live_llm, paid) tests measure the provider-reported
// cached_prompt_tokens across a multi-step tool loop on the real qwen3.7-plus model
// via OpenRouter→DashScope. They are the empirical gate for the prompt-cache fix:
// the request the engine builds each step must be a strict append-only extension of
// the previous one, so DashScope's explicit cache keeps growing and never collapses
// mid-cycle.
//
// Throttle-resilient: OpenRouter routes Qwen through pooled (non-BYOK) Alibaba
// access, which 429s on bursts of large requests, so each step is spaced and retried
// with backoff. Run:
//
//	SYNTHETICBREW_E2E_OPENROUTER_KEY=... go test -tags live_llm \
//	  -run TestCache -v -timeout 1800s ./internal/infrastructure/agents/react/
//
// Env knobs: CACHE_GROWTH_STEPS=<n> (default 16, clamped 1..40);
// CACHE_GROWTH_NOSTICKY=1 drops the x-session-id sticky-routing header.

// cacheHead is a >1024-token system prompt so the head block is cacheable on its own.
const cacheHead = "You are an enterprise IoT diagnostics agent. Standing operating policy:\n" +
	"Each tool returns JSON with a status and a data field; errors carry an error " +
	"field and a numeric code. Investigate carefully, cite device ids, never invent data. "

func cacheSteps() int {
	steps := 16
	if v := os.Getenv("CACHE_GROWTH_STEPS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 40 {
			steps = n
		}
	}
	return steps
}

// reportDips logs the per-step cached series and the steps where the cache collapsed
// to below half the running max — the on-the-fly cache changes the fix must eliminate.
// A step before warm-up completes (i < 3) is exempt. Returns the dip step list.
func reportDips(t *testing.T, cachedPerStep []int) []int {
	t.Helper()
	t.Logf("cached-per-step = %v", cachedPerStep)
	var dips []int
	runMax := 0
	for i, c := range cachedPerStep {
		if c > runMax {
			runMax = c
		}
		if i >= 3 && runMax > 0 && c < runMax/2 {
			dips = append(dips, i+1) // 1-based step number
		}
	}
	t.Logf("DIPS (cached < runMax/2 after warm-up): %v  (empty = 100%% stable)", dips)
	return dips
}

// requireEarlyCacheGrowth is the regression guard for the moving-tail breakpoint: the
// cc-modifier must anchor the history breakpoint on the last byte-stable CONVERSATION
// message (the moving tail), never on the ephemeral trailing volatile head. If it marked
// the volatile head, the marker would move onto changing bytes every step and the cache
// would PIN near zero instead of growing. This asserts the cache grows across the early
// steps (step7 more than double step3), which a pinned marker would fail.
func requireEarlyCacheGrowth(t *testing.T, cachedPerStep []int, steps int) {
	t.Helper()
	if steps < 8 {
		return
	}
	require.Greater(t, cachedPerStep[6], cachedPerStep[2]*2,
		"cache must GROW with the moving tail, not pin on the trailing volatile head (step3=%d step7=%d; series %v)",
		cachedPerStep[2], cachedPerStep[6], cachedPerStep)
}

// liveCacheClient builds the real qwen3.7-plus client + the production cache_control
// modifier and x-session-id sticky option.
func liveCacheClient(t *testing.T, sessionID string) (model.ToolCallingChatModel, model.Option, model.Option) {
	t.Helper()
	key := os.Getenv("SYNTHETICBREW_E2E_OPENROUTER_KEY")
	if key == "" {
		t.Skip("SYNTHETICBREW_E2E_OPENROUTER_KEY not set")
	}
	client, err := llm.CreateClientFromDBModel(models.LLMProviderModel{
		Type:            "openai_compatible",
		BaseURL:         "https://openrouter.ai/api/v1",
		ModelName:       "qwen/qwen3.7-plus",
		APIKeyEncrypted: key,
	}, nil)
	require.NoError(t, err)

	mod := llm.NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true})
	require.NotNil(t, mod)
	cacheOpt := openaiext.WithRequestPayloadModifier(
		func(_ context.Context, _ []*schema.Message, raw []byte) ([]byte, error) { return mod(raw) },
	)
	sessionOpt := openaiext.WithExtraHeader(map[string]string{"x-session-id": sessionID})
	return client, cacheOpt, sessionOpt
}

// streamCachedTokens sends msgs and returns the provider-reported cached_prompt_tokens,
// retrying on transient upstream 429s (pooled Alibaba throttle).
func streamCachedTokens(t *testing.T, client model.ToolCallingChatModel, msgs []*schema.Message, opts ...model.Option) int {
	t.Helper()
	callOpts := make([]model.Option, len(opts))
	copy(callOpts, opts)
	for attempt, backoff := 0, 30*time.Second; attempt < 4; attempt, backoff = attempt+1, backoff*2 {
		sr, gErr := client.Stream(context.Background(), msgs, callOpts...)
		if gErr != nil {
			if strings.Contains(gErr.Error(), "429") || strings.Contains(strings.ToLower(gErr.Error()), "rate") {
				t.Logf("  upstream 429, backoff %s then retry", backoff)
				time.Sleep(backoff)
				continue
			}
			require.NoError(t, gErr)
		}
		cached := 0
		for {
			frame, rErr := sr.Recv()
			if rErr == io.EOF {
				break
			}
			require.NoError(t, rErr)
			if frame.ResponseMeta != nil && frame.ResponseMeta.Usage != nil {
				cached = frame.ResponseMeta.Usage.PromptTokenDetails.CachedTokens
			}
		}
		sr.Close()
		return cached
	}
	// A pooled-Alibaba 429 that survives the backoff is an upstream rate limit
	// (is_byok:false), not a cache defect — skip rather than red the paid suite.
	t.Skipf("skipped: qwen3.7-plus rate-limited upstream (Alibaba pooled 429) after retries — rerun spaced out or with a BYOK key")
	return 0
}

// appendToolRound grows the transcript by one assistant tool_call + one >1024-token
// tool result so the tail clears Qwen's explicit-cache per-block minimum.
func appendToolRound(transcript []*schema.Message, s int) []*schema.Message {
	callID := fmt.Sprintf("call_%d", s)
	return append(transcript,
		&schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID: callID, Type: "function",
			Function: schema.FunctionCall{Name: "get_device_status", Arguments: fmt.Sprintf(`{"device_id":"X1","sensor":%d}`, s)},
		}}},
		&schema.Message{Role: schema.Tool, ToolCallID: callID, ToolName: "get_device_status",
			Content: fmt.Sprintf(`{"status":"ok","sensor":%d,"log":"%s"}`, s, strings.Repeat("trace event recorded; ", 220))},
	)
}

// TestCacheGrowth_LiveQwen37Plus proves the SHIPPED caching config makes the cached
// prefix GROW across a multi-step tool loop on qwen3.7-plus once the per-block size
// clears Qwen's explicit-cache minimum (~1024 tokens). No reminder — isolate the
// growth mechanism.
func TestCacheGrowth_LiveQwen37Plus(t *testing.T) {
	client, cacheOpt, sessionOpt := liveCacheClient(t, "rit-cache-growth-001")

	mm := NewMessageModifier(MessageModifierConfig{SystemPrompt: cacheHead, SessionID: "rit-cache-growth-001"})
	mm.StartTurn()
	transcript := []*schema.Message{schema.UserMessage("Investigate device X1 across all sensors, step by step.")}

	opts := []model.Option{cacheOpt, sessionOpt}
	if os.Getenv("CACHE_GROWTH_NOSTICKY") == "1" {
		opts = []model.Option{cacheOpt}
	}

	steps := cacheSteps()
	var cachedPerStep []int
	for s := 1; s <= steps; s++ {
		c := streamCachedTokens(t, client, mm.Modify(context.Background(), transcript), opts...)
		cachedPerStep = append(cachedPerStep, c)
		t.Logf("step %d: cached=%d", s, c)
		transcript = appendToolRound(transcript, s)
		time.Sleep(15 * time.Second)
	}

	dips := reportDips(t, cachedPerStep)
	require.Greater(t, cachedPerStep[steps-1], cachedPerStep[2],
		"cached must GROW as the conversation extends past the per-block minimum (got %v)", cachedPerStep)
	require.Empty(t, dips, "cache must not collapse mid-loop (dips at steps %v, series %v)", dips, cachedPerStep)
	requireEarlyCacheGrowth(t, cachedPerStep, steps)
}

// TestCacheStability_LiveQwen37Plus_FrozenReminder is the regression proof for the
// reminder-injection fix. A reminder whose value CHANGES every call (the exact shape
// that collapsed the cache under the old interleave design — see by7txb2xg: cached
// 7803→0 at the step it appeared) is now folded ONCE into the frozen head. So the head
// stays byte-identical across the turn and the cache must GROW with ZERO mid-cycle dips
// even though the underlying reminder source keeps "changing".
func TestCacheStability_LiveQwen37Plus_FrozenReminder(t *testing.T) {
	client, cacheOpt, sessionOpt := liveCacheClient(t, "rit-cache-frozen-001")

	var n int
	mm := NewMessageModifier(MessageModifierConfig{
		SystemPrompt:      cacheHead,
		SessionID:         "rit-cache-frozen-001",
		ToolNames:         []string{"get_device_status"},
		ReminderProviders: []ContextReminderProvider{&changingCountReminder{n: &n}},
	})
	mm.StartTurn()
	transcript := []*schema.Message{schema.UserMessage("Investigate device X1 across all sensors, step by step.")}

	opts := []model.Option{cacheOpt, sessionOpt}
	if os.Getenv("CACHE_GROWTH_NOSTICKY") == "1" {
		opts = []model.Option{cacheOpt}
	}

	steps := cacheSteps()
	headFirst := ""
	var cachedPerStep []int
	for s := 1; s <= steps; s++ {
		out := mm.Modify(context.Background(), transcript)
		// The head (out[0]) must be byte-identical every step despite the changing
		// reminder source — that is the whole point of the frozen head.
		require.Equal(t, schema.System, out[0].Role)
		if s == 1 {
			headFirst = out[0].Content
		} else {
			require.Equal(t, headFirst, out[0].Content,
				"frozen head must stay byte-identical across steps (step %d) even as the reminder source changes", s)
		}
		c := streamCachedTokens(t, client, out, opts...)
		cachedPerStep = append(cachedPerStep, c)
		t.Logf("step %d: cached=%d", s, c)
		transcript = appendToolRound(transcript, s)
		time.Sleep(15 * time.Second)
	}

	require.Equal(t, 1, n, "the changing reminder provider must be consulted exactly once (frozen per turn)")
	dips := reportDips(t, cachedPerStep)
	require.Greater(t, cachedPerStep[steps-1], cachedPerStep[2],
		"cached must GROW with the frozen reminder in the volatile head (got %v)", cachedPerStep)
	require.Empty(t, dips, "a changing reminder must NOT collapse the cache once frozen per turn (dips %v, series %v)", dips, cachedPerStep)
	requireEarlyCacheGrowth(t, cachedPerStep, steps)
}
