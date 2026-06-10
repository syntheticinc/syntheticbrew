package callbacks

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// TerminalReason classifies why a turn was force-terminated by a budget,
// loop-breaker, or watchdog rather than completing normally. Every reason maps
// to a distinct, graceful user-facing answer so the client always receives a
// final answer + done event — never a bare error or a dropped stream.
type TerminalReason int

const (
	// TerminalNone means the turn was not force-terminated.
	TerminalNone TerminalReason = iota
	// TerminalToolErrorLoop — a single tool returned [ERROR] repeatedly.
	TerminalToolErrorLoop
	// TerminalIdenticalArgsLoop — the model repeated a byte-identical tool
	// call (same name + arguments) without making progress.
	TerminalIdenticalArgsLoop
	// TerminalStepTimeout — a single step produced no activity for longer
	// than the agent's max_step_duration (a hung model call or tool).
	TerminalStepTimeout
	// TerminalTimeBudget — the turn exceeded max_turn_duration.
	TerminalTimeBudget
	// TerminalStepBudget — the turn exceeded max_steps.
	TerminalStepBudget
)

// String renders the reason for logs.
func (r TerminalReason) String() string {
	switch r {
	case TerminalToolErrorLoop:
		return "tool_error_loop"
	case TerminalIdenticalArgsLoop:
		return "identical_args_loop"
	case TerminalStepTimeout:
		return "step_timeout"
	case TerminalTimeBudget:
		return "time_budget"
	case TerminalStepBudget:
		return "step_budget"
	default:
		return "none"
	}
}

// TerminalState records the first terminal condition that fires during a turn
// and cancels the Eino react loop so it actually stops (cancelling a per-callback
// child context does not). First-wins: once tripped, later trips are ignored, so
// the user-facing reason reflects the root cause. All methods are thread-safe.
type TerminalState struct {
	abortLoop context.CancelFunc

	mu       sync.Mutex
	reason   TerminalReason
	toolName string
	detail   string
}

// NewTerminalState creates a TerminalState. abortLoop cancels the react-loop
// context on the first trip; nil disables the cancel (the reason is still
// recorded) — used in unit tests.
func NewTerminalState(abortLoop context.CancelFunc) *TerminalState {
	return &TerminalState{abortLoop: abortLoop}
}

// Trip records reason (first-wins) and cancels the loop. Returns true only when
// this call set the reason; false if a prior trip already won or reason is None.
func (t *TerminalState) Trip(reason TerminalReason, toolName, detail string) bool {
	if reason == TerminalNone {
		return false
	}
	t.mu.Lock()
	if t.reason != TerminalNone {
		t.mu.Unlock()
		return false
	}
	t.reason = reason
	t.toolName = toolName
	t.detail = detail
	t.mu.Unlock()

	if t.abortLoop != nil {
		t.abortLoop()
	}
	return true
}

// Tripped reports the recorded terminal condition, if any.
func (t *TerminalState) Tripped() (reason TerminalReason, toolName, detail string, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reason, t.toolName, t.detail, t.reason != TerminalNone
}

// formatTerminalMessage builds the graceful, user-facing final answer for a
// terminal reason. Hardcoded and self-contained — these fire only when the model
// did not (or could not) produce its own answer, so they must stand alone.
func formatTerminalMessage(reason TerminalReason, toolName, detail string) string {
	switch reason {
	case TerminalToolErrorLoop:
		r := strings.TrimSpace(strings.TrimPrefix(detail, "[ERROR]"))
		if len(r) > 300 {
			r = r[:300] + "…"
		}
		if r == "" {
			return fmt.Sprintf("I couldn't complete this request: the %q operation kept failing, so I stopped retrying to avoid looping. Please check the request details or try again shortly.", toolName)
		}
		return fmt.Sprintf("I couldn't complete this request: the %q operation kept failing (%s). I stopped retrying to avoid looping. Please check the request details or try again shortly.", toolName, r)
	case TerminalIdenticalArgsLoop:
		return fmt.Sprintf("I couldn't complete this request: I kept repeating the same %q call without making progress, so I stopped to avoid looping. Please refine or narrow the request and I'll try again.", toolName)
	case TerminalStepTimeout:
		return "I couldn't finish this request: one step took too long to respond, so I stopped rather than leave you waiting. Please try again, or narrow the request."
	case TerminalTimeBudget:
		return "I spent the time available on this request and couldn't finish it. Please narrow the request or ask for a specific part, and I'll continue from there."
	case TerminalStepBudget:
		return "I reached the maximum number of steps for this request before finishing. Please narrow the request or break it into smaller parts, and I'll continue."
	default:
		return ""
	}
}

// ActivityClock tracks the timestamp of the most recent agent activity — a model
// chunk, or a tool start/end. The step watchdog reads it to detect a step that
// has hung: produced no activity for longer than max_step_duration. Thread-safe.
type ActivityClock struct {
	last atomic.Int64 // unix nanoseconds
}

// NewActivityClock returns a clock stamped at the current time.
func NewActivityClock() *ActivityClock {
	c := &ActivityClock{}
	c.last.Store(time.Now().UnixNano())
	return c
}

// Touch records activity at the current time.
func (c *ActivityClock) Touch() {
	c.last.Store(time.Now().UnixNano())
}

// Idle returns how long it has been since the last Touch.
func (c *ActivityClock) Idle() time.Duration {
	return time.Duration(time.Now().UnixNano() - c.last.Load())
}
