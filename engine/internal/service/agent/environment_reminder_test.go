package agent

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestEnvironmentReminder_TimestampFrozenAcrossCalls is the regression guard for the
// prompt-cache stability fix: the reminder text must be byte-identical on every call
// within a turn. The timestamp is stamped once at construction (turn start), never
// re-read per step — otherwise a minute roll mid-turn would change already-sent content
// and explicit-cache providers would discard the whole prefix cache.
func TestEnvironmentReminder_TimestampFrozenAcrossCalls(t *testing.T) {
	r := NewEnvironmentContextReminder("/repo", "linux")
	// Pin the stamp to a fixed past instant so the assertion is deterministic and proves
	// the output is driven by stampedAt, not time.Now().
	r.stampedAt = time.Date(2020, 1, 2, 3, 4, 0, 0, time.UTC)

	c1, _, ok1 := r.GetContextReminder(context.Background(), "s")
	// Wall-clock advances between calls; a frozen reminder must not.
	c2, _, ok2 := r.GetContextReminder(context.Background(), "s")

	if !ok1 || !ok2 {
		t.Fatalf("reminder should emit content, got ok1=%v ok2=%v", ok1, ok2)
	}
	if c1 != c2 {
		t.Fatalf("reminder text must be byte-identical across calls (cache stability), got:\n1: %q\n2: %q", c1, c2)
	}
	if !strings.Contains(c1, "2020-01-02 03:04") {
		t.Fatalf("reminder must render the frozen construction time, not time.Now(); got: %q", c1)
	}
}

// TestEnvironmentReminder_EmptyWhenNoEnv keeps the no-environment short-circuit covered.
func TestEnvironmentReminder_EmptyWhenNoEnv(t *testing.T) {
	r := NewEnvironmentContextReminder("", "")
	if c, _, ok := r.GetContextReminder(context.Background(), "s"); ok || c != "" {
		t.Fatalf("empty env should emit nothing, got ok=%v content=%q", ok, c)
	}
}
