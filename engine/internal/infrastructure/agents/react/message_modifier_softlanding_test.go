package react

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

// Soft-landing now lives in the owned loop, not the MessageModifier. These tests guard
// ownedGraphConfig.nearBudgetWall / softLandingNote: the model is nudged to finalize one
// round before the step wall (or past the time soft-deadline), and the nudge carries the
// finalize directive plus the formatted urgency warning — folded into a tool result, never
// a standalone system message.
func TestOwnedGraph_NearBudgetWall(t *testing.T) {
	t.Run("step soft-landing fires one round before the wall", func(t *testing.T) {
		c := ownedGraphConfig{stepBudget: 5} // wall at 5 → nudge at step >= 4
		if c.nearBudgetWall(&ownedState{step: 3}) {
			t.Error("must not soft-land before the reserve point (step 3, budget 5)")
		}
		if !c.nearBudgetWall(&ownedState{step: 4}) {
			t.Error("must soft-land one tool round before the step wall (step 4, budget 5)")
		}
	})

	t.Run("unlimited step budget never soft-lands on steps", func(t *testing.T) {
		c := ownedGraphConfig{} // stepBudget 0 + maxStep 0 → effectively unlimited
		if c.nearBudgetWall(&ownedState{step: 1_000_000}) {
			t.Error("an unlimited budget (sentinel) must never trip the step soft-landing")
		}
	})

	t.Run("time soft-landing fires at 90 percent of max_turn_duration", func(t *testing.T) {
		c := ownedGraphConfig{maxTurnDuration: 10 * time.Second} // soft deadline = 9s
		if c.nearBudgetWall(&ownedState{turnStart: time.Now().Add(-1 * time.Second)}) {
			t.Error("must not soft-land at 10% elapsed")
		}
		if !c.nearBudgetWall(&ownedState{turnStart: time.Now().Add(-9 * time.Second)}) {
			t.Error("must soft-land at 90% elapsed")
		}
	})

	t.Run("zero turnStart never trips the time branch", func(t *testing.T) {
		c := ownedGraphConfig{maxTurnDuration: 10 * time.Second}
		var zero time.Time
		if c.nearBudgetWall(&ownedState{turnStart: zero}) {
			t.Error("an unstamped turnStart must not soft-land on time")
		}
	})
}

// TestOwnedGraph_SoftLandingNote asserts the nudge content: empty before the wall, and
// at the wall it carries the finalize directive ("BUDGET REACHED") with the urgency
// warning's %d filled with the remaining step count, prepended.
func TestOwnedGraph_SoftLandingNote(t *testing.T) {
	t.Run("empty before the wall", func(t *testing.T) {
		c := ownedGraphConfig{stepBudget: 5}
		if note := c.softLandingNote(&ownedState{step: 1}); note != "" {
			t.Errorf("no nudge before the reserve point, got %q", note)
		}
	})

	t.Run("carries finalize directive plus formatted urgency at the wall", func(t *testing.T) {
		c := ownedGraphConfig{
			stepBudget:     5,
			urgencyWarning: "WARNING: only %d steps remaining!",
		}
		note := c.softLandingNote(&ownedState{step: 4}) // remaining = 5 - 4 = 1
		if !strings.Contains(note, "BUDGET REACHED") {
			t.Errorf("nudge must carry the finalize directive, got %q", note)
		}
		if !strings.Contains(note, "only 1 steps remaining!") {
			t.Errorf("urgency warning must be formatted with the remaining step count, got %q", note)
		}
		// Urgency precedes the directive (wrap-up framing first, then the hard instruction).
		if strings.Index(note, "WARNING") >= strings.Index(note, "BUDGET REACHED") {
			t.Errorf("urgency warning must precede the finalize directive, got %q", note)
		}
	})

	t.Run("no urgency configured yields the directive alone", func(t *testing.T) {
		c := ownedGraphConfig{stepBudget: 5}
		note := c.softLandingNote(&ownedState{step: 4})
		if !strings.Contains(note, "BUDGET REACHED") {
			t.Errorf("nudge must carry the finalize directive, got %q", note)
		}
		if strings.Contains(note, "WARNING") {
			t.Errorf("no urgency text when none is configured, got %q", note)
		}
	})
}

// TestFoldEngineNotesIntoToolResults asserts the note is appended under the engine
// marker to the LAST tool result, and is a no-op when there is no tool message to carry it.
func TestFoldEngineNotesIntoToolResults(t *testing.T) {
	t.Run("appends to the last tool result under the marker", func(t *testing.T) {
		output := []*schema.Message{
			{Role: schema.Tool, ToolCallID: "a", Content: "first result"},
			{Role: schema.Tool, ToolCallID: "b", Content: "second result"},
		}
		foldEngineNotesIntoToolResults(output, []string{"correction note", "soft-landing note"})

		if strings.Contains(output[0].Content, engineNoteMarker) {
			t.Error("the note must land on the LAST tool result, not an earlier one")
		}
		last := output[1].Content
		if !strings.HasPrefix(last, "second result") {
			t.Errorf("the tool's own data must be preserved before the note, got %q", last)
		}
		if !strings.Contains(last, engineNoteMarker) {
			t.Errorf("the note must be framed by the engine marker, got %q", last)
		}
		if !strings.Contains(last, "correction note") || !strings.Contains(last, "soft-landing note") {
			t.Errorf("all notes must be folded in, got %q", last)
		}
	})

	t.Run("no-op without a tool message", func(t *testing.T) {
		output := []*schema.Message{
			{Role: schema.Assistant, Content: "assistant text"},
			{Role: schema.User, Content: "user text"},
		}
		foldEngineNotesIntoToolResults(output, []string{"note"})
		for i, m := range output {
			if strings.Contains(m.Content, engineNoteMarker) {
				t.Errorf("no note may be injected when there is no tool result (message %d)", i)
			}
		}
	})

	t.Run("no-op with empty notes", func(t *testing.T) {
		output := []*schema.Message{{Role: schema.Tool, ToolCallID: "a", Content: "result"}}
		foldEngineNotesIntoToolResults(output, nil)
		if output[0].Content != "result" {
			t.Errorf("empty notes must leave the tool result untouched, got %q", output[0].Content)
		}
	})
}

// TestStartTurn_RebuildsFrozenHead verifies StartTurn discards the previous turn's frozen
// head so the next turn rebuilds it: a reminder whose value changes between turns is
// reflected only after StartTurn, never mid-turn.
func TestStartTurn_RebuildsFrozenHead(t *testing.T) {
	var n int
	m := NewMessageModifier(MessageModifierConfig{
		SystemPrompt:      "System",
		SessionID:         "rebuild-session",
		ReminderProviders: []ContextReminderProvider{&changingCountReminder{n: &n}},
	})
	input := []*schema.Message{{Role: schema.User, Content: "hi"}}

	m.StartTurn()
	first := m.Modify(context.Background(), input)[0].Content
	// Within the turn the head is frozen — the changing reminder does not re-poll.
	again := m.Modify(context.Background(), input)[0].Content
	if first != again {
		t.Fatal("head must stay frozen within a turn")
	}
	if !strings.Contains(first, "Only 1 left.") {
		t.Fatalf("first turn must capture the reminder's first value, got %q", first)
	}

	// A new turn rebuilds the head, re-polling the (now-changed) reminder.
	m.StartTurn()
	second := m.Modify(context.Background(), input)[0].Content
	if second == first {
		t.Fatal("StartTurn must rebuild the frozen head so the next turn re-polls reminders")
	}
	if !strings.Contains(second, "Only 2 left.") {
		t.Fatalf("the rebuilt head must reflect the reminder's new value, got %q", second)
	}
}
