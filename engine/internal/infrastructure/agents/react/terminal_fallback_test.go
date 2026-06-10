package react

import (
	"context"
	"fmt"
	"testing"

	"github.com/cloudwego/eino/compose"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agents/callbacks"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// TestTerminalReasonForError guards the root cause of the original bug: a turn
// that ends because a budget (steps or our turn-time deadline) was exhausted must
// be recognised as a graceful terminal condition, not returned as a bare error.
// A genuine client cancel must NOT be treated as a budget.
func TestTerminalReasonForError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		outerCtx   error // ctx.Err() of the outer (client) context
		wantReason callbacks.TerminalReason
		wantOK     bool
	}{
		{
			name:       "eino max steps exhausted",
			err:        compose.ErrExceedMaxSteps,
			outerCtx:   nil,
			wantReason: callbacks.TerminalStepBudget,
			wantOK:     true,
		},
		{
			name:       "wrapped eino max steps",
			err:        fmt.Errorf("agent loop terminated: %w", compose.ErrExceedMaxSteps),
			outerCtx:   nil,
			wantReason: callbacks.TerminalStepBudget,
			wantOK:     true,
		},
		{
			name:       "engine budget-exhausted marker",
			err:        pkgerrors.New(pkgerrors.CodeAgentBudgetExhausted, "step quota exceeded"),
			outerCtx:   nil,
			wantReason: callbacks.TerminalStepBudget,
			wantOK:     true,
		},
		{
			name:       "turn-time deadline on child ctx, outer alive",
			err:        context.DeadlineExceeded,
			outerCtx:   nil,
			wantReason: callbacks.TerminalTimeBudget,
			wantOK:     true,
		},
		{
			name:       "wrapped deadline, outer alive",
			err:        fmt.Errorf("stream: %w", context.DeadlineExceeded),
			outerCtx:   nil,
			wantReason: callbacks.TerminalTimeBudget,
			wantOK:     true,
		},
		{
			name:       "deadline but outer ctx cancelled — genuine client cancel, not a budget",
			err:        context.DeadlineExceeded,
			outerCtx:   context.Canceled,
			wantReason: callbacks.TerminalNone,
			wantOK:     false,
		},
		{
			name:       "client cancel is not a budget",
			err:        context.Canceled,
			outerCtx:   context.Canceled,
			wantReason: callbacks.TerminalNone,
			wantOK:     false,
		},
		{
			name:       "ordinary transport error is not a budget",
			err:        fmt.Errorf("connection reset by peer"),
			outerCtx:   nil,
			wantReason: callbacks.TerminalNone,
			wantOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, ok := terminalReasonForError(tt.err, tt.outerCtx)
			if ok != tt.wantOK {
				t.Fatalf("ok: got %v, want %v", ok, tt.wantOK)
			}
			if reason != tt.wantReason {
				t.Fatalf("reason: got %v, want %v", reason, tt.wantReason)
			}
		})
	}
}

// TestStepWatchdogDuration verifies the per-step timeout conversion: 0 disables.
func TestStepWatchdogDuration(t *testing.T) {
	if d := (&Agent{maxStepDuration: 0}).stepWatchdogDuration(); d != 0 {
		t.Fatalf("maxStepDuration 0 must disable watchdog, got %v", d)
	}
	if d := (&Agent{maxStepDuration: 45}).stepWatchdogDuration(); d.Seconds() != 45 {
		t.Fatalf("maxStepDuration 45 → 45s, got %v", d)
	}
}
