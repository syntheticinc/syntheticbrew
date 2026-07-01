//go:build live_llm

// The decisive depth experiment for the EXPLICIT-cache path (live qwen3.7-plus,
// which — unlike qwen3-coder-next — does NOT auto-cache: no markers ⇒ 0% cached).
//
// It pits the two explicit-marker schemes head-to-head at DEPTH, past the point
// where the fixed-stride cap bites:
//
//	B (fixed-stride): current production — head + checkpoints at msgs 16/32/48.
//	                  4-breakpoint cap ⇒ nothing past ~msg 48 is marked ⇒ the
//	                  recent tail is re-billed at depth (the partner's "partial cache").
//	C (moving-tail):  head + a SINGLE breakpoint on the last stable conversation
//	                  message, moved forward each step. Each step's tail marker
//	                  chains to the PREVIOUS request's tail write (~2 blocks back,
//	                  within the provider lookback), so it should track the FULL
//	                  prefix to any depth with no stride cap.
//
// Front-loaded: a deep base (past msg 48) is warmed once per arm, then grown a few
// steps. B should plateau/decay (fixed markers can't reach the tail); C should stay
// near-full-prefix cached at every depth. If C ≥ B at depth with no collapse, the
// fixed-stride machinery is replaceable by moving-tail → full-depth explicit caching.
//
//	SYNTHETICBREW_E2E_OPENROUTER_KEY=... go test -tags live_llm \
//	  -run TestCacheDepth_StrideVsMovingTail -v -timeout 2400s ./internal/infrastructure/agents/react/
package react

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// markHeadStride is a LOCAL, self-contained reimplementation of the OLD fixed-stride
// cache scheme (head + checkpoints at messages 16/32/48, capped at 4 breakpoints), kept
// here only as the baseline arm of this depth comparison. It does NOT call the production
// modifier — that now ships moving-tail — so this baseline stays a fixed reference of the
// capped behaviour the fix replaced, and the comparison against the production modifier
// (arm C) is not tautological. On a long conversation it marks nothing past ~message 48,
// so its cached prefix freezes there while the prompt keeps growing.
func markHeadStride(raw []byte) ([]byte, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, err
	}
	var msgs []map[string]json.RawMessage
	if err := json.Unmarshal(top["messages"], &msgs); err != nil {
		return nil, err
	}
	role := func(m map[string]json.RawMessage) string {
		var r string
		_ = json.Unmarshal(m["role"], &r)
		return r
	}
	cacheable := func(m map[string]json.RawMessage) bool {
		var s string
		return json.Unmarshal(m["content"], &s) == nil && s != ""
	}
	head := -1
	for i, m := range msgs {
		if role(m) == "system" {
			head = i
			break
		}
	}
	tail := -1 // last non-system cacheable message (bounds the stride walk)
	for i := len(msgs) - 1; i >= 0; i-- {
		if role(msgs[i]) != "system" && cacheable(msgs[i]) {
			tail = i
			break
		}
	}
	lastCacheableAtOrBefore := func(limit int) int {
		if limit >= len(msgs) {
			limit = len(msgs) - 1
		}
		for i := limit; i >= 0; i-- {
			if cacheable(msgs[i]) {
				return i
			}
		}
		return -1
	}
	marks := map[int]bool{}
	order := 0
	add := func(i int) {
		if i >= 0 && !marks[i] && order < 4 { // provider 4-breakpoint cap
			marks[i] = true
			order++
		}
	}
	add(head)
	for b := 16; b <= tail && order < 4; b += 16 { // fixed stride 16/32/48
		add(lastCacheableAtOrBefore(b))
	}
	ephemeral := json.RawMessage(`{"type":"ephemeral"}`)
	for i, m := range msgs {
		if !cacheable(m) {
			continue
		}
		var s string
		_ = json.Unmarshal(m["content"], &s)
		text, _ := json.Marshal(s)
		part := map[string]json.RawMessage{"type": json.RawMessage(`"text"`), "text": text}
		if marks[i] {
			part["cache_control"] = ephemeral
		}
		m["content"], _ = json.Marshal([]map[string]json.RawMessage{part})
	}
	top["messages"], _ = json.Marshal(msgs)
	return json.Marshal(top)
}

func TestCacheDepth_StrideVsMovingTail(t *testing.T) {
	key := os.Getenv("SYNTHETICBREW_E2E_OPENROUTER_KEY")
	if key == "" {
		t.Skip("SYNTHETICBREW_E2E_OPENROUTER_KEY not set")
	}
	// Arm C = the PRODUCTION modifier (now head + moving-tail). Arm B = the local
	// markHeadStride baseline (the deleted fixed-stride scheme). This keeps the comparison
	// honest: it pits the shipped code against the old capped scheme, not against itself.
	prod := llm.NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true})
	require.NotNil(t, prod)

	sysPrompt := strings.Repeat("You are an enterprise IoT diagnostics agent. Investigate carefully, cite device ids, never invent data. ", 60)
	tools := []string{"get_device_status"}
	ctx := context.Background()

	mm := NewMessageModifier(MessageModifierConfig{SystemPrompt: sysPrompt, ToolNames: tools, SessionID: "depth"})
	mm.StartTurn()

	// Deep base: 28 tool rounds ⇒ ~57 conversation messages ⇒ modified ~59, well past
	// the stride-48 checkpoint so B is already in its capped regime at grown step 1.
	transcript := []*schema.Message{schema.UserMessage("Investigate device X1 across all sensors, exhaustively, step by step.")}
	for s := 1; s <= 28; s++ {
		transcript = appendToolRound(transcript, s)
	}

	sidStride := "depth-stride-001"
	sidMoving := "depth-moving-001"

	post := func(sid string, body []byte) (int, int, bool) { return abPost(t, key, sid, body) }

	// Warm both arms on the identical deep base (cold ⇒ cached≈0), establishing each
	// scheme's cache writes before measuring the grown steps.
	{
		msgs := mm.Modify(ctx, transcript)
		raw := crossturnOpenAIBody(msgs)
		sb, _ := markHeadStride(raw)
		mb, _ := prod(raw)
		post(sidStride, sb)
		time.Sleep(12 * time.Second)
		post(sidMoving, mb)
		time.Sleep(12 * time.Second)
	}

	type row struct {
		step               int
		bP, cP             float64
		bPr, bCa, cPr, cCa int
	}
	var rows []row
	var bDepth, cDepth []float64
	grow := 10
	for g := 1; g <= grow; g++ {
		transcript = appendToolRound(transcript, 100+g)
		msgs := mm.Modify(ctx, transcript)
		raw := crossturnOpenAIBody(msgs)
		sb, err := markHeadStride(raw)
		require.NoError(t, err)
		mb, err := prod(raw)
		require.NoError(t, err)

		bp, bc, okB := post(sidStride, sb)
		time.Sleep(12 * time.Second)
		cp, cc, okC := post(sidMoving, mb)
		time.Sleep(12 * time.Second)

		bPct, cPct := pct(bc, bp), pct(cc, cp)
		rows = append(rows, row{step: g, bP: bPct, cP: cPct, bPr: bp, bCa: bc, cPr: cp, cCa: cc})
		t.Logf("grow %2d | B(stride) prompt=%6d cached=%6d %5.1f%% %s | C(moving-tail) prompt=%6d cached=%6d %5.1f%% %s",
			g, bp, bc, bPct, okStr(okB), cp, cc, cPct, okStr(okC))
		if okB && okC {
			bDepth = append(bDepth, bPct)
			cDepth = append(cDepth, cPct)
		}
	}

	t.Logf("=== stride vs moving-tail at DEPTH (base ~57 msgs + %d grown) ===", grow)
	for _, r := range rows {
		t.Logf("  grow %2d: B(stride)=%5.1f%%  C(moving-tail)=%5.1f%%", r.step, r.bP, r.cP)
	}
	require.NotEmpty(t, cDepth, "no clean samples (rate-limited) — rerun spaced out")
	bMean, cMean := mean(bDepth), mean(cDepth)
	t.Logf("DEPTH MEAN: B(stride)=%.1f%%  C(moving-tail)=%.1f%%  (n=%d)", bMean, cMean, len(cDepth))

	// The decision gate: moving-tail must reach full depth (near-full-prefix cached at
	// every grown step, so it does NOT decay as the tail grows past the stride cap) and
	// must beat the capped stride scheme at depth.
	require.Greater(t, cMean, 90.0,
		"moving-tail must cache near the FULL prefix at depth (got %.1f%%) — no stride cap", cMean)
	require.Greater(t, cMean, bMean,
		"moving-tail (%.1f%%) must beat capped fixed-stride (%.1f%%) at depth", cMean, bMean)
	// Guard the no-collapse property explicitly: C must never dip to the head-only level.
	for _, r := range rows {
		if r.cCa > 0 {
			require.Greater(t, r.cP, 50.0,
				"moving-tail must not collapse to head-only at any depth (grow %d = %.1f%%, cached=%d prompt=%d)", r.step, r.cP, r.cCa, r.cPr)
		}
	}
}
