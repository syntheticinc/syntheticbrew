package usagelimit

import "sync"

// StepAccumulator counts real agent steps per in-flight turn, keyed by session
// id. The enforcement seam settles a turn with the exact number of steps the
// agent took, so the accumulator is driven by the same per-step signal the
// agent runtime already fires (one Inc per IncrementStep) and drained once when
// the turn completes.
//
// Inc only counts sessions that have been Begin'd. The per-step signal is a
// process-global callback fired by EVERY turn source (chat, admin assistant,
// background task runs), but only the turn owners that settle usage (the chat
// adapter) call Begin. Steps from a source that never Begins are ignored, so a
// non-settling source (e.g. a cron task) can never leak a map entry — the
// accumulator only ever holds entries for turns that will be Taken or
// Discarded.
//
// It is process-global but strictly session-keyed: one session's turn never
// reads or resets another's counter, so it is safe under the multi-tenant
// invariant (session ids are unique across tenants).
type StepAccumulator struct {
	mu     sync.Mutex
	counts map[string]int
}

// NewStepAccumulator creates an empty StepAccumulator.
func NewStepAccumulator() *StepAccumulator {
	return &StepAccumulator{counts: make(map[string]int)}
}

// Begin registers a session as counting, starting from zero. Only Begin'd
// sessions accumulate steps via Inc; this scopes accumulation to the turn owners
// that settle (Take/Discard) so non-settling step sources never leak. A "" id is
// ignored. Every Begin must be matched by a later Take or Discard.
func (a *StepAccumulator) Begin(sessionID string) {
	if sessionID == "" {
		return
	}
	a.mu.Lock()
	a.counts[sessionID] = 0
	a.mu.Unlock()
}

// Inc records one agent step for a session, but ONLY if that session was
// Begin'd. A "" session id, or a session no owner is counting, is ignored.
func (a *StepAccumulator) Inc(sessionID string) {
	if sessionID == "" {
		return
	}
	a.mu.Lock()
	if _, counting := a.counts[sessionID]; counting {
		a.counts[sessionID]++
	}
	a.mu.Unlock()
}

// Take returns the accumulated step count for a session and clears it, so a
// subsequent turn on the same session starts from zero. Reading a session with
// no recorded steps returns 0.
func (a *StepAccumulator) Take(sessionID string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := a.counts[sessionID]
	delete(a.counts, sessionID)
	return n
}

// Discard drops any accumulated steps for a session without reading them. Used
// when a turn ends without a billable settle (errored before output) so the
// count does not leak into the next turn.
func (a *StepAccumulator) Discard(sessionID string) {
	if sessionID == "" {
		return
	}
	a.mu.Lock()
	delete(a.counts, sessionID)
	a.mu.Unlock()
}
