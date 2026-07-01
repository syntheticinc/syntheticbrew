package react

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
)

// variantReminder is a ContextReminderProvider whose content CHANGES on every call
// (it embeds an incrementing counter). It exists to prove the cache invariant even
// against a reminder that is NOT stable step-to-step: whatever it returns must land
// only in the trailing volatile head, never in the cache-marked stable head or the
// interior of the append-only history.
type variantReminder struct {
	calls    int64
	priority int
}

func (v *variantReminder) GetContextReminder(_ context.Context, _ string) (string, int, bool) {
	n := atomic.AddInt64(&v.calls, 1)
	return fmt.Sprintf("**REMINDER token=%d**: a reminder whose content differs on every single call.", n), v.priority, true
}

// TestModifier_CacheMarkedHeadIsTurnInvariant guards the cross-turn prompt-cache
// regression (1.8.6 folded the per-turn CURRENT TASK + reminders into the single
// frozen head, which the cache_control modifier marks — so the marked block changed
// every turn and the whole system prompt was re-billed each turn).
//
// The cache_control breakpoint marks the FIRST system message. For cross-turn caching
// it MUST be byte-identical across turns despite a different user question; the
// per-turn-volatile CURRENT TASK (and reminders) must live in a SEPARATE system
// message that is NOT the breakpoint. Since the fix, that volatile system message sits
// at the TAIL (after the whole conversation) so the append-only history stays part of
// the byte-stable cacheable prefix. RED before the split, GREEN after.
func TestModifier_CacheMarkedHeadIsTurnInvariant(t *testing.T) {
	sys := "You are a device-onboarding assistant. " + strings.Repeat("Follow the provisioning protocols carefully. ", 50)
	tools := []string{"show_structured_output", "connection_list"}
	build := func(q string) []*schema.Message {
		mm := NewMessageModifier(MessageModifierConfig{SystemPrompt: sys, ToolNames: tools, SessionID: "s1"})
		return mm.Modify(context.Background(), []*schema.Message{schema.UserMessage(q)})
	}
	a := build("Connect device ALPHA")
	b := build("Connect device BETA")

	require.Equal(t, schema.System, a[0].Role)
	require.Equal(t, schema.System, b[0].Role)

	// (1) the cache_control-marked head (msg0) must be turn-invariant.
	require.Equal(t, a[0].Content, b[0].Content,
		"the cache_control-marked head (msg0) must be byte-identical across turns for cross-turn caching")

	// (2) the volatile CURRENT TASK must NOT be inside msg0 (it would change msg0 per turn).
	require.NotContains(t, a[0].Content, "CURRENT TASK",
		"the per-turn CURRENT TASK must not live in the cache-marked head")

	// (3) the CURRENT TASK anchor must not be lost — it now lives in the trailing
	// volatile head, so it is the LAST message.
	require.Equal(t, schema.System, a[len(a)-1].Role, "the volatile head is the trailing system message")
	require.Contains(t, a[len(a)-1].Content, "CURRENT TASK",
		"CURRENT TASK must live in the trailing volatile head message")

	// (4) the volatile head is the ONLY system message after the stable head — no system
	// message may appear STRICTLY BETWEEN the head and the tail (a mid-conversation system
	// message would re-render Qwen's chat template and discard the explicit-cache prefix).
	// The conversation between out[0] and out[len-1] must be user/assistant/tool only.
	for i := 1; i < len(a)-1; i++ {
		require.NotEqual(t, schema.System, a[i].Role,
			"no system message may appear between the stable head and the trailing volatile head (index %d)", i)
	}
}

// TestModifier_HistoryIsCrossTurnStablePrefix is the deterministic (no-LLM) guard for the
// BIG-history cross-turn cache invariant. Production builds a fresh MessageModifier per
// turn, so we do the same: turn 1 answers Q1; turn 2 is a real multi-turn transcript
// (Q1, answer1, DIFFERENT Q2). The changed question rides only in turn 2's TRAILING
// volatile head; the stable head + the entire append-only history before it must stay a
// byte-identical cacheable prefix. Concretely: turn 1's non-volatile portion (everything
// except its trailing volatile head) must be a byte-identical PREFIX of turn 2's
// non-volatile portion. If it is, a provider caching the common prefix reuses the whole
// history cross-turn; only the tiny volatile tail is re-billed. RED if the volatile head
// ever moves back to the front (the changed question would then perturb the prefix).
func TestModifier_HistoryIsCrossTurnStablePrefix(t *testing.T) {
	sys := "You are a device-onboarding assistant. " + strings.Repeat("Follow the provisioning protocols carefully. ", 50)
	tools := []string{"show_structured_output", "connection_list"}

	// Turn 1: cold, question Q1. Fresh modifier (one per turn in production).
	mm1 := NewMessageModifier(MessageModifierConfig{SystemPrompt: sys, ToolNames: tools, SessionID: "s1"})
	a := mm1.Modify(context.Background(), []*schema.Message{
		schema.UserMessage("Connect device ALPHA"),
	})

	// Turn 2: grown transcript with a DIFFERENT question. Fresh modifier again.
	mm2 := NewMessageModifier(MessageModifierConfig{SystemPrompt: sys, ToolNames: tools, SessionID: "s1"})
	b := mm2.Modify(context.Background(), []*schema.Message{
		schema.UserMessage("Connect device ALPHA"),
		schema.AssistantMessage("Answer for ALPHA.", nil),
		schema.UserMessage("Connect device BETA, a completely different question"),
	})

	// The last message of each array is the trailing volatile head (CURRENT TASK); it
	// differs between the turns and is excluded from the prefix comparison.
	require.Equal(t, schema.System, a[len(a)-1].Role)
	require.Equal(t, schema.System, b[len(b)-1].Role)
	require.Contains(t, a[len(a)-1].Content, "CURRENT TASK")
	require.Contains(t, b[len(b)-1].Content, "CURRENT TASK")
	require.NotEqual(t, a[len(a)-1].Content, b[len(b)-1].Content,
		"the trailing volatile heads must differ (different questions) — otherwise the test didn't vary the turn")

	// The cacheable prefix = stable head + full conversation, i.e. everything EXCEPT the
	// trailing volatile head. Turn 1's prefix must be a byte-identical prefix of turn 2's:
	// the changed question (in the tail) does not perturb the append-only history.
	aPrefix := a[:len(a)-1]
	bPrefix := b[:len(b)-1]
	require.LessOrEqual(t, len(aPrefix), len(bPrefix),
		"turn 2's cacheable prefix must be at least as long as turn 1's (history only grows)")
	for i := range aPrefix {
		require.Equal(t, aPrefix[i].Role, bPrefix[i].Role,
			"cacheable-prefix message %d role must be byte-identical across turns", i)
		require.Equal(t, aPrefix[i].Content, bPrefix[i].Content,
			"cacheable-prefix message %d content must be byte-identical across turns (changed question must NOT perturb the prefix)", i)
	}
}

// TestModifier_HistoryStablePrefix_WithVariantReminders extends the cross-turn stable-prefix
// guard to the case that actually stresses the design: an ENABLED reminder provider whose
// content differs on every call. The other cross-turn tests build the modifier with NO
// ReminderProviders, so they only prove the CURRENT TASK is tail-confined — never that a
// CHANGED reminder is also tail-confined. If a reminder ever leaked into the stable head
// (index 0, the cache_control breakpoint) or into the interior of the append-only history,
// the cacheable prefix would change every turn and the whole (often huge) history would be
// re-billed. This test proves the invariant against a maximally hostile reminder:
//
//	(i)  the reminder content appears ONLY in the trailing volatile head (the LAST message)
//	     of each turn — never in msg[0] and never in the interior;
//	(ii) turn 1's non-volatile portion a[:len(a)-1] is a byte-identical PREFIX of turn 2's
//	     non-volatile portion b[:len(b)-1] — so a changed reminder (and a changed question)
//	     does NOT perturb the cacheable prefix.
func TestModifier_HistoryStablePrefix_WithVariantReminders(t *testing.T) {
	sys := "You are a device-onboarding assistant. " + strings.Repeat("Follow the provisioning protocols carefully. ", 50)
	tools := []string{"show_structured_output", "connection_list"}

	// Turn 1: cold, question Q1. Fresh modifier + a fresh variant reminder (one per turn in
	// production). SessionID must be non-empty or collectReminders short-circuits.
	rem1 := &variantReminder{priority: 95}
	mm1 := NewMessageModifier(MessageModifierConfig{
		SystemPrompt:      sys,
		ToolNames:         tools,
		SessionID:         "s1",
		ReminderProviders: []ContextReminderProvider{rem1},
	})
	a := mm1.Modify(context.Background(), []*schema.Message{
		schema.UserMessage("Q1: connect device ALPHA"),
	})

	// Turn 2: grown transcript with a DIFFERENT question. Fresh modifier + fresh reminder.
	rem2 := &variantReminder{priority: 95}
	mm2 := NewMessageModifier(MessageModifierConfig{
		SystemPrompt:      sys,
		ToolNames:         tools,
		SessionID:         "s1",
		ReminderProviders: []ContextReminderProvider{rem2},
	})
	b := mm2.Modify(context.Background(), []*schema.Message{
		schema.UserMessage("Q1: connect device ALPHA"),
		schema.AssistantMessage("Answer for ALPHA.", nil),
		schema.UserMessage("Q2: connect device BETA, a completely different question"),
	})

	// The reminder content is identifiable by its "REMINDER token=" prefix. It must appear
	// ONLY in the trailing volatile head (the last message), never in the stable head or the
	// interior. Check each turn independently.
	const reminderMarker = "**REMINDER token="
	for _, tc := range []struct {
		name string
		out  []*schema.Message
	}{{"turn1", a}, {"turn2", b}} {
		out := tc.out
		require.Equal(t, schema.System, out[0].Role, "%s: stable head is the first system message", tc.name)
		require.NotContains(t, out[0].Content, reminderMarker,
			"%s: a reminder must NOT leak into the cache-marked stable head (msg0)", tc.name)

		last := out[len(out)-1]
		require.Equal(t, schema.System, last.Role, "%s: volatile head is the trailing system message", tc.name)
		require.Contains(t, last.Content, reminderMarker,
			"%s: the reminder must live in the trailing volatile head", tc.name)

		// The interior (everything strictly between the stable head and the volatile head)
		// must be reminder-free AND system-free — a mid-conversation system message would
		// re-render the chat template and collapse the explicit-cache prefix on Qwen/DashScope.
		for i := 1; i < len(out)-1; i++ {
			require.NotContains(t, out[i].Content, reminderMarker,
				"%s: the reminder must NOT leak into the interior history (index %d)", tc.name, i)
			require.NotEqual(t, schema.System, out[i].Role,
				"%s: no system message may appear between the stable head and the trailing volatile head (index %d)", tc.name, i)
		}
	}

	// The two trailing volatile heads must differ: the reminders varied AND the questions
	// varied, so if they were byte-identical the test wouldn't be exercising the invariant.
	require.NotEqual(t, a[len(a)-1].Content, b[len(b)-1].Content,
		"the trailing volatile heads must differ (varying reminder + changed question)")

	// The cacheable prefix = everything EXCEPT the trailing volatile head. Turn 1's prefix
	// must be a byte-identical PREFIX of turn 2's: neither the changed reminder nor the
	// changed question perturbs the append-only history the provider caches cross-turn.
	aPrefix := a[:len(a)-1]
	bPrefix := b[:len(b)-1]
	require.LessOrEqual(t, len(aPrefix), len(bPrefix),
		"turn 2's cacheable prefix must be at least as long as turn 1's (history only grows)")
	for i := range aPrefix {
		require.Equal(t, aPrefix[i].Role, bPrefix[i].Role,
			"cacheable-prefix message %d role must be byte-identical across turns", i)
		require.Equal(t, aPrefix[i].Content, bPrefix[i].Content,
			"cacheable-prefix message %d content must be byte-identical across turns (changed reminder must NOT perturb the prefix)", i)
	}
}
