package callbacks

import (
	"context"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// TestTerminalAnswer_RetractsStreamedProse verifies that when partial prose was
// streamed live before a terminal condition force-stopped the turn, the graceful
// answer is preceded by a RetractAssistant event — so the live stream matches
// the persisted history (which keeps only the graceful answer). Mirrors the HITL
// scrub.
func TestTerminalAnswer_RetractsStreamedProse(t *testing.T) {
	captured := newCapturedEvents()
	b := newTestBuilder(captured)

	// Simulate live prose chunks having reached the client this turn.
	b.modelHandler.accumulatedMu.Lock()
	b.modelHandler.anyChunkStreamed = true
	b.modelHandler.accumulatedMu.Unlock()

	b.terminal.Trip(TerminalIdenticalArgsLoop, "hardware_search", "")

	msg := b.EmitTerminalAnswer(context.Background())
	if msg == "" {
		t.Fatal("expected a graceful terminal answer")
	}

	if captured.byType[domain.EventTypeRetractAssistant] != 1 {
		t.Fatalf("expected exactly one RetractAssistant, got %d", captured.byType[domain.EventTypeRetractAssistant])
	}
	if captured.byType[domain.EventTypeAnswer] != 1 {
		t.Fatalf("expected exactly one Answer, got %d", captured.byType[domain.EventTypeAnswer])
	}

	// Retract must come BEFORE the answer so the client scrubs first, then renders.
	var retractIdx, answerIdx = -1, -1
	for i, e := range captured.all {
		switch e.Type {
		case domain.EventTypeRetractAssistant:
			retractIdx = i
		case domain.EventTypeAnswer:
			answerIdx = i
		}
	}
	if retractIdx == -1 || answerIdx == -1 || retractIdx >= answerIdx {
		t.Fatalf("retract must precede answer: retractIdx=%d answerIdx=%d", retractIdx, answerIdx)
	}
}

// TestTerminalAnswer_NoRetractWhenNothingStreamed verifies the retract fires only
// when there is orphaned partial prose. The non-streaming path (and any turn that
// emitted no live chunks) must not emit a spurious retract.
func TestTerminalAnswer_NoRetractWhenNothingStreamed(t *testing.T) {
	captured := newCapturedEvents()
	b := newTestBuilder(captured)

	b.terminal.Trip(TerminalStepBudget, "", "")

	msg := b.EmitTerminalAnswer(context.Background())
	if msg == "" {
		t.Fatal("expected a graceful terminal answer")
	}
	if captured.byType[domain.EventTypeRetractAssistant] != 0 {
		t.Fatalf("no prose was streamed; RetractAssistant must not fire, got %d", captured.byType[domain.EventTypeRetractAssistant])
	}
	if captured.byType[domain.EventTypeAnswer] != 1 {
		t.Fatalf("expected exactly one Answer, got %d", captured.byType[domain.EventTypeAnswer])
	}
}

// TestTerminalAnswer_EmptyWhenNotTripped verifies EmitTerminalAnswer is a no-op
// when no terminal condition tripped (the normal completion path).
func TestTerminalAnswer_EmptyWhenNotTripped(t *testing.T) {
	captured := newCapturedEvents()
	b := newTestBuilder(captured)

	if msg := b.EmitTerminalAnswer(context.Background()); msg != "" {
		t.Fatalf("expected empty answer when not tripped, got %q", msg)
	}
	if len(captured.all) != 0 {
		t.Fatalf("no events should be emitted when not tripped, got %d", len(captured.all))
	}
}
