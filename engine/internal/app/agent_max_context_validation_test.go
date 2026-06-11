package app

import (
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// TestValidateMaxContextSize guards the SCC-03 contract: an out-of-range
// max_context_size must be rejected at the API layer as InvalidInput (→ 400)
// instead of silently degenerating (an absurd value overflows the rewriter's
// char-budget arithmetic into a negative budget that compresses every request).
func TestValidateMaxContextSize(t *testing.T) {
	valid := []int{0, 1, 1000, 16000, 128000, domain.MaxContextSizeCeiling}
	for _, v := range valid {
		if err := validateMaxContextSize(v); err != nil {
			t.Errorf("validateMaxContextSize(%d) = %v, want nil (in-range)", v, err)
		}
	}

	invalid := []int{-1, -1000, domain.MaxContextSizeCeiling + 1, 1 << 62}
	for _, v := range invalid {
		err := validateMaxContextSize(v)
		if err == nil {
			t.Errorf("validateMaxContextSize(%d) = nil, want InvalidInput", v)
			continue
		}
		if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
			t.Errorf("validateMaxContextSize(%d) error code = %v, want CodeInvalidInput (maps to 400)", v, err)
		}
	}
}
