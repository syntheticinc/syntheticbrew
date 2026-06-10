package app

import (
	"testing"

	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// TestValidateMaxTurnDuration guards the SCC-03 contract for max_turn_duration:
// an out-of-range value must be rejected at the API layer as InvalidInput (→ 400),
// not flow into time.Duration arithmetic where a huge value overflows int64 to a
// negative (already-expired) deadline or creates an effectively-infinite turn.
// Bound: 0 (default 120s) OR 10..86400.
func TestValidateMaxTurnDuration(t *testing.T) {
	valid := []int{0, 1, 5, 10, 120, 300, 3600, 86400}
	for _, v := range valid {
		if err := validateMaxTurnDuration(v); err != nil {
			t.Errorf("validateMaxTurnDuration(%d) = %v, want nil (in-range)", v, err)
		}
	}

	// Includes the int64-overflow class (>~9.2e9s overflows ns) and the unbounded
	// case the security review flagged as the goroutine-park enabler.
	invalid := []int{-1, 86401, 100000, 9_300_000_000, 1 << 60}
	for _, v := range invalid {
		err := validateMaxTurnDuration(v)
		if err == nil {
			t.Errorf("validateMaxTurnDuration(%d) = nil, want InvalidInput", v)
			continue
		}
		if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
			t.Errorf("validateMaxTurnDuration(%d) error code = %v, want CodeInvalidInput (maps to 400)", v, err)
		}
	}
}
