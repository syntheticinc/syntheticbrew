package app

import (
	"time"

	deliveryhttp "github.com/syntheticinc/syntheticbrew/internal/delivery/http"
	"github.com/syntheticinc/syntheticbrew/internal/service/resilience"
)

// circuitBreakerQuerierHTTPAdapter adapts CircuitBreakerRegistry to the
// deliveryhttp.CircuitBreakerQuerier interface (consumer-side).
type circuitBreakerQuerierHTTPAdapter struct {
	registry *resilience.CircuitBreakerRegistry
}

func (a *circuitBreakerQuerierHTTPAdapter) Snapshots() []deliveryhttp.CircuitBreakerState {
	raw := a.registry.Snapshots()
	out := make([]deliveryhttp.CircuitBreakerState, len(raw))
	for i, s := range raw {
		var lastFailure *time.Time
		if !s.LastFailure.IsZero() {
			t := s.LastFailure
			lastFailure = &t
		}
		out[i] = deliveryhttp.CircuitBreakerState{
			Name:         s.Name,
			State:        string(s.State),
			FailureCount: s.FailureCount,
			LastFailure:  lastFailure,
		}
	}
	return out
}

func (a *circuitBreakerQuerierHTTPAdapter) Reset(name string) bool {
	return a.registry.Reset(name)
}

// Compile-time interface check.
var _ deliveryhttp.CircuitBreakerQuerier = (*circuitBreakerQuerierHTTPAdapter)(nil)
