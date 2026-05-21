package app

import (
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools"
	"github.com/syntheticinc/syntheticbrew/internal/service/resilience"
)

// circuitBreakerRegistryAdapter bridges resilience.CircuitBreakerRegistry to tools.CircuitBreakerRegistry.
type circuitBreakerRegistryAdapter struct {
	registry *resilience.CircuitBreakerRegistry
}

func (a *circuitBreakerRegistryAdapter) Get(name string) tools.CircuitBreakerChecker {
	return a.registry.Get(name)
}
