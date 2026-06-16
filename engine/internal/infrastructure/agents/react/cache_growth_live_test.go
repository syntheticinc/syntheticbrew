//go:build live_llm

package react

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	openaiext "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// TestCacheGrowth_LiveQwen37Plus proves the SHIPPED caching config (system+tail
// cache_control breakpoints, append-only history, x-session-id) makes the cached prefix
// GROW across a multi-step tool loop on qwen3.7-plus — once the per-block size clears
// Qwen's explicit-cache minimum (~1024 tokens). Earlier "cache stuck at head" findings
// were an artifact of a synthetic conversation whose tail blocks were below that minimum.
//
// Throttle-resilient: OpenRouter routes Qwen through pooled (non-BYOK) Alibaba access,
// which 429s on bursts of large requests, so each step is spaced and retried with backoff.
// Build-tagged live_llm (paid). Run:
//
//	SYNTHETICBREW_E2E_OPENROUTER_KEY=... go test -tags live_llm \
//	  -run TestCacheGrowth_LiveQwen37Plus -v -timeout 1200s ./internal/infrastructure/agents/react/
func TestCacheGrowth_LiveQwen37Plus(t *testing.T) {
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

	// Exactly the modifier the engine attaches in production (default breakpoints =
	// system + last cacheable tail). No reminder — isolate the growth mechanism.
	mod := llm.NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true})
	require.NotNil(t, mod)
	cacheOpt := openaiext.WithRequestPayloadModifier(
		func(_ context.Context, _ []*schema.Message, raw []byte) ([]byte, error) { return mod(raw) },
	)
	sessionOpt := openaiext.WithExtraHeader(map[string]string{"x-session-id": "rit-cache-growth-001"})

	// Head > 1024 tokens so the system block is cacheable on its own.
	head := "You are an enterprise IoT diagnostics agent. Standing operating policy:\n" +
		strings.Repeat("Each tool returns JSON with a status and a data field; errors carry an error "+
			"field and a numeric code. Investigate carefully, cite device ids, never invent data. ", 60)

	mm := NewMessageModifier(MessageModifierConfig{SystemPrompt: head, SessionID: "rit-cache-growth-001"})
	mm.StartTurn()
	transcript := []*schema.Message{schema.UserMessage("Investigate device X1 across all sensors, step by step.")}

	// streamCached retries on transient upstream 429s (pooled Alibaba throttle).
	streamCached := func(msgs []*schema.Message) (int, error) {
		var lastErr error
		for attempt, backoff := 0, 30*time.Second; attempt < 4; attempt, backoff = attempt+1, backoff*2 {
			sr, gErr := client.Stream(context.Background(), msgs, cacheOpt, sessionOpt)
			if gErr != nil {
				lastErr = gErr
				if strings.Contains(gErr.Error(), "429") || strings.Contains(strings.ToLower(gErr.Error()), "rate") {
					t.Logf("  upstream 429, backoff %s then retry", backoff)
					time.Sleep(backoff)
					continue
				}
				return 0, gErr
			}
			cached := 0
			for {
				frame, rErr := sr.Recv()
				if rErr == io.EOF {
					break
				}
				if rErr != nil {
					lastErr = rErr
					break
				}
				if frame.ResponseMeta != nil && frame.ResponseMeta.Usage != nil {
					cached = frame.ResponseMeta.Usage.PromptTokenDetails.CachedTokens
				}
			}
			sr.Close()
			if lastErr == nil {
				return cached, nil
			}
		}
		return 0, lastErr
	}

	// 12 steps reaches the first fixed checkpoint (stride 16 messages ≈ step 9) so the
	// staircase is visible; CACHE_GROWTH_STEPS=20 exercises the second checkpoint too.
	steps := 12
	if v := os.Getenv("CACHE_GROWTH_STEPS"); v == "20" {
		steps = 20
	}
	var cachedPerStep []int
	for s := 1; s <= steps; s++ {
		c, sErr := streamCached(mm.Modify(context.Background(), transcript))
		require.NoError(t, sErr, "step %d", s)
		cachedPerStep = append(cachedPerStep, c)
		t.Logf("step %d: cached=%d", s, c)

		// Each tool result > 1024 tokens so the growing tail clears the explicit-cache block minimum.
		callID := fmt.Sprintf("call_%d", s)
		transcript = append(transcript,
			&schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
				ID: callID, Type: "function",
				Function: schema.FunctionCall{Name: "get_device_status", Arguments: fmt.Sprintf(`{"device_id":"X1","sensor":%d}`, s)},
			}}},
			&schema.Message{Role: schema.Tool, ToolCallID: callID, ToolName: "get_device_status",
				Content: fmt.Sprintf(`{"status":"ok","sensor":%d,"log":"%s"}`, s, strings.Repeat("trace event recorded; ", 220))},
		)
		time.Sleep(15 * time.Second) // space steps to avoid pooled-Alibaba 429
	}

	t.Logf("cached-per-step = %v", cachedPerStep)

	// Growth: the cached prefix must be larger late in the loop than right after warm-up.
	require.Greater(t, cachedPerStep[steps-1], cachedPerStep[2],
		"cached must GROW as the conversation extends past the per-block minimum (got %v)", cachedPerStep)
	// No full collapse after warm-up: every post-warm-up step keeps a non-trivial cache.
	for i := 3; i < steps; i++ {
		require.Greater(t, cachedPerStep[i], 0,
			"no full cache drop after warm-up at step %d (got %v)", i+1, cachedPerStep)
	}
}
