package react

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

func TestShouldFinalize(t *testing.T) {
	t.Run("step soft-landing reserves the last model call", func(t *testing.T) {
		m := NewMessageModifier(MessageModifierConfig{MaxSteps: 10}) // softModelCallBudget = 5
		var zero time.Time
		if m.shouldFinalize(3, zero) {
			t.Error("must not finalize before the reserve point (step 3)")
		}
		if !m.shouldFinalize(4, zero) {
			t.Error("must finalize one model call before the step wall (step 4)")
		}
	})

	t.Run("unlimited steps never finalize on steps", func(t *testing.T) {
		m := NewMessageModifier(MessageModifierConfig{MaxSteps: 0})
		var zero time.Time
		if m.shouldFinalize(1000, zero) {
			t.Error("maxSteps 0 (unlimited) must not trigger step soft-landing")
		}
	})

	t.Run("time soft-landing fires at 90 percent", func(t *testing.T) {
		m := NewMessageModifier(MessageModifierConfig{MaxTurnDuration: 10}) // soft deadline = 9s
		if m.shouldFinalize(0, time.Now().Add(-1*time.Second)) {
			t.Error("must not finalize at 10% elapsed")
		}
		if !m.shouldFinalize(0, time.Now().Add(-9*time.Second)) {
			t.Error("must finalize at 90% elapsed")
		}
	})

	t.Run("no budgets configured never finalizes", func(t *testing.T) {
		m := NewMessageModifier(MessageModifierConfig{})
		if m.shouldFinalize(100, time.Now().Add(-1*time.Hour)) {
			t.Error("with no budgets there is nothing to soft-land")
		}
	})
}

// TestModify_InjectsFinalizeDirective verifies the directive reaches the system
// prompt once the step reserve point is crossed.
func TestModify_InjectsFinalizeDirective(t *testing.T) {
	m := NewMessageModifier(MessageModifierConfig{SystemPrompt: "base", MaxSteps: 6}) // budget = 3, fires at step 2
	m.StartTurn()

	input := []*schema.Message{{Role: schema.User, Content: "hi"}}

	// Step 0, 1: below the reserve point — no directive.
	for i := 0; i < 2; i++ {
		out := m.Modify(context.Background(), input)
		if strings.Contains(out[0].Content, "BUDGET REACHED") {
			t.Fatalf("directive injected too early at model call %d", i)
		}
	}
	// Step 2: reserve point reached — directive present.
	out := m.Modify(context.Background(), input)
	if !strings.Contains(out[0].Content, "BUDGET REACHED") {
		t.Fatal("finalize directive not injected at the reserve point")
	}
}

// TestStartTurn_ResetsState verifies StartTurn stamps time and resets the
// per-turn model-call counter so a reused modifier soft-lands correctly each turn.
func TestStartTurn_ResetsState(t *testing.T) {
	m := NewMessageModifier(MessageModifierConfig{SystemPrompt: "base", MaxSteps: 6})
	input := []*schema.Message{{Role: schema.User, Content: "hi"}}

	m.StartTurn()
	for i := 0; i < 3; i++ {
		m.Modify(context.Background(), input)
	}
	if m.GetStep() != 3 {
		t.Fatalf("step counter: got %d, want 3", m.GetStep())
	}

	m.StartTurn()
	if m.GetStep() != 0 {
		t.Fatalf("StartTurn must reset step counter, got %d", m.GetStep())
	}
	if m.turnStart.IsZero() {
		t.Fatal("StartTurn must stamp turnStart")
	}
}
