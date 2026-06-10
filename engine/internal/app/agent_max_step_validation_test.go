package app

import (
	"testing"

	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// TestValidateMaxStepDuration guards the SCC-03 contract: an out-of-range
// max_step_duration must be rejected at the API layer as InvalidInput (→ 400),
// not slip through to the DB CHECK and surface as a 500-class constraint error.
// Mirrors the CHECK chk_agent_step_duration (0 OR 10..3600).
func TestValidateMaxStepDuration(t *testing.T) {
	valid := []int{0, 10, 11, 120, 300, 3599, 3600}
	for _, v := range valid {
		if err := validateMaxStepDuration(v); err != nil {
			t.Errorf("validateMaxStepDuration(%d) = %v, want nil (in-range)", v, err)
		}
	}

	invalid := []int{-1, 1, 5, 9, 3601, 4000, 100000}
	for _, v := range invalid {
		err := validateMaxStepDuration(v)
		if err == nil {
			t.Errorf("validateMaxStepDuration(%d) = nil, want InvalidInput", v)
			continue
		}
		if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
			t.Errorf("validateMaxStepDuration(%d) error code = %v, want CodeInvalidInput (maps to 400)", v, err)
		}
	}
}
