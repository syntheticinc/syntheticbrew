//go:build live_llm

// The definitive cross-turn regression (live qwen3.7-plus) for the partner's case:
// a LARGE, HITL-heavy multi-turn conversation driven through the REAL MessageModifier
// (tail-placed volatile head) + the REAL cache_control modifier. Proves the whole
// append-only history stays cached across turns even as each new user message / form
// submission changes the CURRENT TASK. Before the tail fix, every turn re-billed the
// whole history (only the ~system prompt cached).
//
//	SYNTHETICBREW_E2E_OPENROUTER_KEY=... go test -tags live_llm \
//	  -run TestCrossTurnBigHistory -v -timeout 1200s ./internal/infrastructure/agents/react/
package react

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

func TestCrossTurnBigHistory_LiveQwen(t *testing.T) {
	key := os.Getenv("SYNTHETICBREW_E2E_OPENROUTER_KEY")
	if key == "" {
		t.Skip("SYNTHETICBREW_E2E_OPENROUTER_KEY not set")
	}
	cc := llm.NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true})
	require.NotNil(t, cc)

	// A large, turn-invariant system prompt + tools, like a real IoT ops agent (~2k tokens
	// here; the effect scales with the history, not the head).
	sysPrompt := strings.Repeat("You are the IoT operations assistant. Operate devices, rules, alarms and dashboards; cite ids; never invent data. ", 150)
	tools := []string{"show_structured_output", "device_list", "device_delete", "device_provision_lorawan", "connection_list", "hardware_search"}

	// The history's bulk: one huge tool result (~30k tokens), like the partner's
	// list_use_case output. Frozen in history from turn 1 onward.
	bigTool := &schema.Message{Role: schema.Tool, ToolCallID: "t0", ToolName: "device_list",
		Content: "[TOOL OUTPUT] " + strings.Repeat(`{"id":"dev","name":"a device automation scenario record with details"},`, 2000)}

	ctx := context.Background()
	sid := "bighistory-live-001"

	// postTurn builds the turn's request through the PRODUCTION MessageModifier (which now
	// tail-places the volatile head) + the production cache_control modifier, sends it under
	// the sticky session, and returns (prompt_tokens, cached_tokens). It also asserts the
	// production shape: stable head at index 0, volatile (CURRENT TASK) as the LAST message.
	postTurn := func(transcript []*schema.Message) (int, int) {
		mm := NewMessageModifier(MessageModifierConfig{SystemPrompt: sysPrompt, ToolNames: tools, SessionID: sid})
		msgs := mm.Modify(ctx, transcript)
		require.Equal(t, schema.System, msgs[0].Role, "index 0 must be the stable head")
		last := msgs[len(msgs)-1]
		require.Equal(t, schema.System, last.Role, "the volatile head must be the LAST message (tail placement)")
		require.Contains(t, last.Content, "CURRENT TASK", "the tail message must be the volatile CURRENT TASK")
		require.NotContains(t, msgs[0].Content, "CURRENT TASK", "the cache-marked head must not carry the per-turn task")
		return placementPost(t, key, sid, msgs, cc)
	}

	// Turn 1 (cold): the initial question + a tool loop that fetched the huge result.
	transcript := []*schema.Message{
		schema.UserMessage("What can I automate on my account?"),
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "t0", Type: "function", Function: schema.FunctionCall{Name: "device_list", Arguments: "{}"}}}},
		bigTool,
		schema.AssistantMessage("Based on your profile, here are the automation options.", nil),
	}
	p1, c1 := postTurn(transcript)
	t.Logf("turn 1 (cold): prompt=%d cached=%d", p1, c1)
	time.Sleep(15 * time.Second)

	// Turns 2..6: a HITL-heavy stretch — each a NEW user message / form submission that
	// changes CURRENT TASK, growing the append-only transcript. The whole history must stay
	// cached (tail-placed volatile), so cached ≈ prompt minus the tiny newest additions.
	steps := []string{
		"Delete the device named Car_Main",
		"User submitted the form: Q: confirm? A: Confirm",
		"Now provision a new LoRaWAN sensor Milesight WS303",
		"User submitted the form: Q: DevEUI? A: 70B3D57BA0000001",
		"Show diagnostics for the new device",
	}
	minPct := 100.0
	for i, q := range steps {
		transcript = append(transcript,
			schema.UserMessage(q),
			&schema.Message{Role: schema.Tool, ToolCallID: fmt.Sprintf("s%d", i), ToolName: "op", Content: "Structured output displayed to user."},
			schema.AssistantMessage(fmt.Sprintf("Done with step %d.", i+1), nil),
		)
		p, c := postTurn(transcript)
		pct := 100 * float64(c) / float64(p)
		if pct < minPct {
			minPct = pct
		}
		t.Logf("turn %d (changed question): prompt=%d cached=%d  (%.0f%% cached)", i+2, p, c, pct)
		// The whole append-only history must stay cached cross-turn — NOT collapse to the
		// ~system-prompt (~ a few k of ~35k) that the front-placement bug produced. 80% is a
		// wide margin below the ~99% expected and far above the ~15% bug level.
		require.Greater(t, pct, 80.0,
			"cross-turn: the append-only history must stay cached with the tail-placed volatile (turn %d got %.0f%%, prompt=%d cached=%d)", i+2, pct, p, c)
		time.Sleep(15 * time.Second)
	}
	t.Logf("worst cross-turn cache ratio across the HITL-heavy conversation: %.0f%%", minPct)
}
