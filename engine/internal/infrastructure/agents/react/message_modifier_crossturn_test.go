package react

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
)

// TestModifier_CacheMarkedHeadIsTurnInvariant guards the cross-turn prompt-cache
// regression (1.8.6 folded the per-turn CURRENT TASK + reminders into the single
// frozen head, which the cache_control modifier marks — so the marked block changed
// every turn and the whole system prompt was re-billed each turn).
//
// The cache_control breakpoint marks the FIRST system message. For cross-turn caching
// it MUST be byte-identical across turns despite a different user question; the
// per-turn-volatile CURRENT TASK (and reminders) must live in a SEPARATE later system
// message that is not the breakpoint. RED before the split, GREEN after.
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

	// (3) CURRENT TASK must still appear in the assembled head (a separate volatile
	// system message) so the anchor is not lost.
	var systemText strings.Builder
	for _, m := range a {
		if m.Role == schema.System {
			systemText.WriteString(m.Content)
		}
	}
	require.Contains(t, systemText.String(), "CURRENT TASK",
		"CURRENT TASK must still be present, in a separate volatile head message")

	// (4) all system messages must be contiguous at the front (the head) — no
	// mid-conversation system message (that would re-render Qwen's chat template).
	firstNonSystem := -1
	for i, m := range a {
		if m.Role != schema.System {
			firstNonSystem = i
			break
		}
	}
	require.GreaterOrEqual(t, firstNonSystem, 1, "head must have at least one system message before the conversation")
	for _, m := range a[firstNonSystem:] {
		require.NotEqual(t, schema.System, m.Role, "no system message may appear after the conversation starts")
	}
}
