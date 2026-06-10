package react

import "testing"

// TestStepWatchdogDuration verifies the per-step timeout conversion: 0 disables.
func TestStepWatchdogDuration(t *testing.T) {
	if d := (&Agent{maxStepDuration: 0}).stepWatchdogDuration(); d != 0 {
		t.Fatalf("maxStepDuration 0 must disable watchdog, got %v", d)
	}
	if d := (&Agent{maxStepDuration: 45}).stepWatchdogDuration(); d.Seconds() != 45 {
		t.Fatalf("maxStepDuration 45 → 45s, got %v", d)
	}
}
