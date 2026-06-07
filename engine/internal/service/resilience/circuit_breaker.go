package resilience

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// CircuitState represents the state of a circuit breaker.
type CircuitState string

const (
	CircuitClosed   CircuitState = "closed"    // normal operation
	CircuitOpen     CircuitState = "open"      // failures exceeded threshold
	CircuitHalfOpen CircuitState = "half_open" // trying one request after reset interval
)

// CircuitBreakerConfig holds configuration for a circuit breaker.
type CircuitBreakerConfig struct {
	FailureThreshold int           // consecutive failures to open (default 3)
	FailureWindow    time.Duration // window for counting failures (default 60s)
	ResetInterval    time.Duration // time before half-open (default 120s)
}

// DefaultCircuitBreakerConfig returns default circuit breaker configuration.
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		FailureThreshold: 3,
		FailureWindow:    60 * time.Second,
		ResetInterval:    120 * time.Second,
	}
}

// CircuitBreaker implements per-resource circuit breaker pattern.
// Used for MCP servers and LLM models (AC-RESIL-09..12).
type CircuitBreaker struct {
	mu                 sync.RWMutex
	name               string
	config             CircuitBreakerConfig
	state              CircuitState
	failures           []time.Time // timestamps of consecutive failures within window
	lastFailure        time.Time
	openedAt           time.Time
	failureCountAtOpen int
}

// NewCircuitBreaker creates a new circuit breaker for the named resource.
func NewCircuitBreaker(name string, config CircuitBreakerConfig) *CircuitBreaker {
	return &CircuitBreaker{
		name:   name,
		config: config,
		state:  CircuitClosed,
	}
}

// State returns the current circuit state.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.currentState()
}

// Name returns the resource name.
func (cb *CircuitBreaker) Name() string {
	return cb.name
}

// AllowRequest checks if a request is allowed.
// Returns an error if the circuit is open (AC-RESIL-10).
func (cb *CircuitBreaker) AllowRequest() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	state := cb.currentState()
	switch state {
	case CircuitClosed:
		return nil
	case CircuitHalfOpen:
		return nil // allow one probe request
	case CircuitOpen:
		return errors.Unavailable(
			"Service temporarily unavailable — please try again in a few seconds.",
			fmt.Errorf("circuit breaker open for %s: too many failures", cb.name),
		)
	}
	return nil
}

// RecordSuccess records a successful request.
// Transitions half-open → closed (AC-RESIL-11).
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == CircuitHalfOpen {
		slog.InfoContext(context.Background(), "[CircuitBreaker] half-open → closed", "resource", cb.name)
		cb.state = CircuitClosed
	}
	cb.failures = nil
}

// RecordFailure records a failed request.
// Opens circuit after threshold consecutive failures (AC-RESIL-09).
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()
	cb.lastFailure = now

	// If half-open probe failed, go back to open
	if cb.state == CircuitHalfOpen {
		slog.WarnContext(context.Background(), "[CircuitBreaker] half-open probe failed → open", "resource", cb.name)
		cb.state = CircuitOpen
		cb.openedAt = now
		cb.failureCountAtOpen = 1
		return
	}

	// Prune old failures outside the window
	cutoff := now.Add(-cb.config.FailureWindow)
	pruned := make([]time.Time, 0, len(cb.failures))
	for _, t := range cb.failures {
		if t.After(cutoff) {
			pruned = append(pruned, t)
		}
	}
	pruned = append(pruned, now)
	cb.failures = pruned

	// Check threshold
	if len(cb.failures) >= cb.config.FailureThreshold {
		slog.WarnContext(context.Background(), "[CircuitBreaker] threshold reached → open",
			"resource", cb.name, "failures", len(cb.failures))
		cb.state = CircuitOpen
		cb.openedAt = now
		cb.failureCountAtOpen = len(cb.failures)
		cb.failures = nil
	}
}

// FailureCount returns the number of failures that triggered the circuit open.
// Returns current in-window failures for CLOSED state, stored count for OPEN/HALF-OPEN.
func (cb *CircuitBreaker) FailureCount() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	if cb.state == CircuitOpen || cb.state == CircuitHalfOpen {
		return cb.failureCountAtOpen
	}
	return len(cb.failures)
}

// LastFailure returns the timestamp of the most recent failure (zero if none).
func (cb *CircuitBreaker) LastFailure() time.Time {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.lastFailure
}

// currentState returns the effective state, checking for half-open transition.
// Must be called with lock held.
func (cb *CircuitBreaker) currentState() CircuitState {
	if cb.state == CircuitOpen {
		if time.Since(cb.openedAt) >= cb.config.ResetInterval {
			cb.state = CircuitHalfOpen
			slog.InfoContext(context.Background(), "[CircuitBreaker] open → half-open", "resource", cb.name)
		}
	}
	return cb.state
}

// CircuitBreakerRegistry manages circuit breakers for multiple resources.
type CircuitBreakerRegistry struct {
	mu       sync.RWMutex
	breakers map[string]*CircuitBreaker
	config   CircuitBreakerConfig
}

// NewCircuitBreakerRegistry creates a new registry with default config.
func NewCircuitBreakerRegistry(config CircuitBreakerConfig) *CircuitBreakerRegistry {
	return &CircuitBreakerRegistry{
		breakers: make(map[string]*CircuitBreaker),
		config:   config,
	}
}

// Get returns or creates a circuit breaker for the named resource.
func (r *CircuitBreakerRegistry) Get(name string) *CircuitBreaker {
	r.mu.RLock()
	cb, ok := r.breakers[name]
	r.mu.RUnlock()
	if ok {
		return cb
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock
	if cb, ok := r.breakers[name]; ok {
		return cb
	}

	cb = NewCircuitBreaker(name, r.config)
	r.breakers[name] = cb
	return cb
}

// States returns the current state of all circuit breakers.
func (r *CircuitBreakerRegistry) States() map[string]CircuitState {
	r.mu.RLock()
	defer r.mu.RUnlock()

	states := make(map[string]CircuitState, len(r.breakers))
	for name, cb := range r.breakers {
		states[name] = cb.State()
	}
	return states
}

// Reset removes a circuit breaker by name, forcing a fresh one on next Get().
// Returns true if the breaker existed.
func (r *CircuitBreakerRegistry) Reset(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, ok := r.breakers[name]
	if ok {
		delete(r.breakers, name)
		slog.InfoContext(context.Background(), "[CircuitBreaker] reset", "resource", name)
	}
	return ok
}

// CircuitBreakerSnapshot is a point-in-time snapshot of a circuit breaker.
type CircuitBreakerSnapshot struct {
	Name         string       `json:"name"`
	State        CircuitState `json:"state"`
	FailureCount int          `json:"failure_count"`
	LastFailure  time.Time    `json:"last_failure,omitempty"`
}

// Snapshots returns snapshots of all circuit breakers.
func (r *CircuitBreakerRegistry) Snapshots() []CircuitBreakerSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]CircuitBreakerSnapshot, 0, len(r.breakers))
	for _, cb := range r.breakers {
		out = append(out, CircuitBreakerSnapshot{
			Name:         cb.name,
			State:        cb.State(),
			FailureCount: cb.FailureCount(),
			LastFailure:  cb.LastFailure(),
		})
	}
	return out
}
