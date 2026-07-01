//go:build live_llm

// A/B experiment (live qwen3.7-plus) that settled whether the engine needs explicit
// cache_control markers at all on this model, or whether append-only + x-session-id
// sticky routing caches on its own (the automatic-cache mode that works for
// qwen3-coder-next / openai / gemini). The answer for qwen3.7-plus: it does NOT
// auto-cache — the sticky-only arm caches ~0% at every depth — so the markers are the
// working lever and must not be dropped. It grows one turn's tool loop deep and, at each
// step, sends the SAME transcript twice under two sticky sessions:
//
//	A (automatic): raw body, x-session-id only — NO cache_control markers.
//	B (cc): the production cache_control modifier (head + moving tail).
//
//	SYNTHETICBREW_E2E_OPENROUTER_KEY=... go test -tags live_llm \
//	  -run TestCacheAB_DepthAutomaticVsCC -v -timeout 3000s ./internal/infrastructure/agents/react/
//
// Env: CACHE_AB_STEPS=<n> (default 34, clamped 8..60).
package react

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// abSteps returns the configured depth (default 34 — deep enough that the "depth"
// samples counted from step 24 sit on a large, well-warmed prefix).
func abSteps() int {
	steps := 34
	if v := os.Getenv("CACHE_AB_STEPS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 8 && n <= 60 {
			steps = n
		}
	}
	return steps
}

// abPost sends body under the sticky session and returns (prompt, cached, ok).
// ok=false means the pooled Alibaba 429 survived the backoff — the caller records
// the step as a gap rather than reding the paid suite.
func abPost(t *testing.T, key, sessionID string, body []byte) (prompt, cached int, ok bool) {
	t.Helper()
	for attempt, backoff := 0, 30*time.Second; attempt < 4; attempt, backoff = attempt+1, backoff*2 {
		req, _ := http.NewRequest("POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-session-id", sessionID)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests || bytes.Contains(bytes.ToLower(raw), []byte("rate limit")) {
			t.Logf("  [%s] upstream 429, backoff %s then retry", sessionID, backoff)
			time.Sleep(backoff)
			continue
		}
		var d struct {
			Usage struct {
				PromptTokens        int `json:"prompt_tokens"`
				PromptTokensDetails struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
			} `json:"usage"`
		}
		require.NoError(t, json.Unmarshal(raw, &d), "decode OR response: %s", string(raw))
		return d.Usage.PromptTokens, d.Usage.PromptTokensDetails.CachedTokens, true
	}
	return 0, 0, false
}

func pct(cached, prompt int) float64 {
	if prompt <= 0 {
		return 0
	}
	return 100 * float64(cached) / float64(prompt)
}

// TestCacheAB_DepthAutomaticVsCC proves why the explicit cache_control markers are
// load-bearing on qwen3.7-plus: this model does NOT do automatic prefix caching. Arm A
// sends the raw body with only the x-session-id sticky header (the lever that works for
// automatic-cache providers like qwen3-coder-next / openai / gemini); arm B adds the
// engine's cache_control markers. On qwen3.7-plus arm A caches ~0% at every depth while
// arm B caches substantially — so dropping the markers (relying on automatic caching)
// would give this model NO cache at all. It asserts the direction, not a fixed number.
func TestCacheAB_DepthAutomaticVsCC(t *testing.T) {
	key := os.Getenv("SYNTHETICBREW_E2E_OPENROUTER_KEY")
	if key == "" {
		t.Skip("SYNTHETICBREW_E2E_OPENROUTER_KEY not set")
	}
	cc := llm.NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true})
	require.NotNil(t, cc)

	// A ~2k-token turn-invariant head so both modes have a solid cacheable prefix.
	sysPrompt := strings.Repeat("You are an enterprise IoT diagnostics agent. Investigate carefully, cite device ids, never invent data. ", 60)
	tools := []string{"get_device_status"}

	ctx := context.Background()
	sidAuto := "ab-depth-auto-001"
	sidCC := "ab-depth-cc-001"

	// One turn: a single frozen head, an append-only growing tool loop (the partner's
	// long-agentic-loop shape and the "full depth" the goal targets).
	mm := NewMessageModifier(MessageModifierConfig{SystemPrompt: sysPrompt, ToolNames: tools, SessionID: "ab-depth"})
	mm.StartTurn()
	transcript := []*schema.Message{schema.UserMessage("Investigate device X1 across all sensors, step by step, exhaustively.")}

	steps := abSteps()
	depthFrom := 24 // count "depth" samples from here (a deep, warmed prefix)
	type row struct{ aPct, bPct float64 }
	var rows []row
	var aDepth, bDepth []float64

	for s := 1; s <= steps; s++ {
		msgs := mm.Modify(ctx, transcript)
		raw := crossturnOpenAIBody(msgs)
		ccBody, err := cc(raw)
		require.NoError(t, err)

		pA, cA, okA := abPost(t, key, sidAuto, raw)
		time.Sleep(12 * time.Second)
		pB, cB, okB := abPost(t, key, sidCC, ccBody)

		aP, bP := pct(cA, pA), pct(cB, pB)
		rows = append(rows, row{aPct: aP, bPct: bP})
		t.Logf("step %2d | A(auto) prompt=%6d cached=%6d %5.1f%% %s | B(cc) prompt=%6d cached=%6d %5.1f%% %s",
			s, pA, cA, aP, okStr(okA), pB, cB, bP, okStr(okB))

		if s >= depthFrom && okA && okB {
			aDepth = append(aDepth, aP)
			bDepth = append(bDepth, bP)
		}

		transcript = appendToolRound(transcript, s)
		time.Sleep(12 * time.Second)
	}

	t.Logf("=== A/B depth summary (%d steps) ===", steps)
	for i, r := range rows {
		t.Logf("  step %2d: A=%5.1f%%  B=%5.1f%%", i+1, r.aPct, r.bPct)
	}
	if len(aDepth) == 0 {
		t.Skip("no clean depth samples (rate-limited) — rerun spaced out")
	}
	aMean, bMean := mean(aDepth), mean(bDepth)
	t.Logf("DEPTH (steps ≥ %d): automatic mean=%.1f%%  cc+stride mean=%.1f%%  (n=%d)", depthFrom, aMean, bMean, len(aDepth))

	// The gate: on qwen3.7-plus the sticky-only (no-marker) arm A caches ~nothing, while
	// the cache_control arm B caches substantially — proving the markers are load-bearing
	// for this model and must NOT be dropped in favour of automatic caching.
	require.Less(t, aMean, 20.0,
		"qwen3.7-plus does NOT auto-cache: with no cache_control markers it caches ~0%% at depth (got %.1f%%) — the markers are load-bearing", aMean)
	require.Greater(t, bMean, 50.0,
		"the cache_control markers must actually cache on qwen3.7-plus (got %.1f%% at depth) — this is the working lever, not automatic caching", bMean)
}

func okStr(ok bool) string {
	if ok {
		return ""
	}
	return "(429-skip)"
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}
