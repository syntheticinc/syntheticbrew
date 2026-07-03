package usagelimit

import "testing"

func TestStepAccumulator_IncOnlyCountsBegunSessions(t *testing.T) {
	a := NewStepAccumulator()

	// A session that was never Begin'd (e.g. a background task run) must not
	// accumulate — otherwise its map entry would leak forever.
	a.Inc("task-session")
	a.Inc("task-session")
	if got := a.Take("task-session"); got != 0 {
		t.Fatalf("un-begun session must count 0, got %d", got)
	}

	// A Begin'd session accumulates and drains cleanly.
	a.Begin("chat-session")
	a.Inc("chat-session")
	a.Inc("chat-session")
	a.Inc("chat-session")
	if got := a.Take("chat-session"); got != 3 {
		t.Fatalf("begun session must count 3, got %d", got)
	}
	// After Take, further Inc is ignored (drained) — no leak.
	a.Inc("chat-session")
	if got := a.Take("chat-session"); got != 0 {
		t.Fatalf("drained session must count 0, got %d", got)
	}

	// Discard also drops the entry.
	a.Begin("s2")
	a.Inc("s2")
	a.Discard("s2")
	if got := a.Take("s2"); got != 0 {
		t.Fatalf("discarded session must count 0, got %d", got)
	}
}
