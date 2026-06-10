package callbacks

import (
	"context"
	"testing"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

func noopEventCallback(*domain.AgentEvent) error { return nil }

// TestStepWatchdog_FiresOnHungStep verifies that the watchdog trips
// TerminalStepTimeout and cancels the loop when no activity occurs for longer
// than maxStepDuration.
func TestStepWatchdog_FiresOnHungStep(t *testing.T) {
	aborted := make(chan struct{})
	cb := NewBuilder(BuilderConfig{
		EventCallback: noopEventCallback,
		AbortLoop:     func() { close(aborted) },
	})

	stop := cb.StartStepWatchdog(context.Background(), 40*time.Millisecond)
	defer stop()

	select {
	case <-aborted:
	case <-time.After(3 * time.Second):
		t.Fatal("watchdog did not fire on a hung step")
	}

	reason, _, _, ok := cb.TerminalTripped()
	if !ok || reason != TerminalStepTimeout {
		t.Fatalf("terminal: got reason=%v ok=%v, want TerminalStepTimeout", reason, ok)
	}
}

// TestStepWatchdog_ActivityKeepsAlive verifies that regular activity prevents the
// watchdog from firing — a legitimately long step (steady chunks) is not a hang.
func TestStepWatchdog_ActivityKeepsAlive(t *testing.T) {
	cb := NewBuilder(BuilderConfig{
		EventCallback: noopEventCallback,
		AbortLoop:     func() { t.Error("watchdog fired while activity was ongoing") },
	})

	stop := cb.StartStepWatchdog(context.Background(), 80*time.Millisecond)

	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		cb.activity.Touch()
		time.Sleep(20 * time.Millisecond)
	}
	stop()

	if _, _, _, ok := cb.TerminalTripped(); ok {
		t.Fatal("watchdog tripped despite steady activity")
	}
}

// TestStepWatchdog_DisabledWhenZero verifies a non-positive duration disables it.
func TestStepWatchdog_DisabledWhenZero(t *testing.T) {
	cb := NewBuilder(BuilderConfig{
		EventCallback: noopEventCallback,
		AbortLoop:     func() { t.Error("watchdog must not run when disabled") },
	})
	stop := cb.StartStepWatchdog(context.Background(), 0)
	stop()
	time.Sleep(60 * time.Millisecond)
	if _, _, _, ok := cb.TerminalTripped(); ok {
		t.Fatal("disabled watchdog tripped")
	}
}
