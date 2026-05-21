package flowregistry

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// flowEntry holds a flow and its associated cancel function.
// The cancel func is stored here (not in domain) to keep ActiveFlow pure.
type flowEntry struct {
	flow        *domain.ActiveFlow
	cancel      context.CancelFunc
	messageSink interface{ PublishUserMessage(string) error }
}

// InMemoryRegistry is an in-memory implementation of the flow registry.
type InMemoryRegistry struct {
	mu    sync.RWMutex
	flows map[string]*flowEntry
}

// NewInMemoryRegistry creates a new InMemoryRegistry.
func NewInMemoryRegistry() *InMemoryRegistry {
	return &InMemoryRegistry{
		flows: make(map[string]*flowEntry),
	}
}

// Register registers an active flow with its cancel function.
// If a flow already exists for this session, its cancel func is called and the flow is replaced.
// cancel may be nil if cancellation is not needed.
func (r *InMemoryRegistry) Register(sessionID string, flow *domain.ActiveFlow, cancel context.CancelFunc) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, exists := r.flows[sessionID]; exists {
		if existing.cancel != nil {
			existing.cancel()
		}
		slog.InfoContext(context.Background(), "replacing existing flow", "session_id", sessionID)
	}

	r.flows[sessionID] = &flowEntry{flow: flow, cancel: cancel}

	return nil
}

// Unregister removes a flow from the registry (idempotent — no error if not found).
func (r *InMemoryRegistry) Unregister(sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.flows, sessionID)

	return nil
}

// UnregisterIfCurrent atomically unregisters the flow only if the currently
// registered flow matches the expected one (pointer equality).
// This prevents a stale defer from removing a replacement flow.
func (r *InMemoryRegistry) UnregisterIfCurrent(sessionID string, expected *domain.ActiveFlow) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, exists := r.flows[sessionID]
	if !exists {
		return false
	}
	if entry.flow != expected {
		return false
	}

	delete(r.flows, sessionID)
	return true
}

// Get returns the active flow by session_id.
func (r *InMemoryRegistry) Get(sessionID string) (*domain.ActiveFlow, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, exists := r.flows[sessionID]
	if !exists {
		return nil, false
	}
	return entry.flow, true
}

// IsActive reports whether the session has an active running flow.
func (r *InMemoryRegistry) IsActive(sessionID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, exists := r.flows[sessionID]
	if !exists {
		return false
	}

	return entry.flow.IsRunning()
}

// ListActiveFlows returns all currently registered flows
func (r *InMemoryRegistry) ListActiveFlows() []*domain.ActiveFlow {
	r.mu.RLock()
	defer r.mu.RUnlock()

	flows := make([]*domain.ActiveFlow, 0, len(r.flows))
	for _, entry := range r.flows {
		flows = append(flows, entry.flow)
	}
	return flows
}

// SetMessageSink attaches a message sink to an existing flow entry.
func (r *InMemoryRegistry) SetMessageSink(sessionID string, sink interface{ PublishUserMessage(string) error }) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if entry, exists := r.flows[sessionID]; exists {
		entry.messageSink = sink
	}
}

// PublishUserMessage delivers a user message to the active flow's EventBus.
// Returns false if the session is not active or has no message sink.
func (r *InMemoryRegistry) PublishUserMessage(sessionID, message string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, exists := r.flows[sessionID]
	if !exists || entry.messageSink == nil {
		return false
	}

	if err := entry.messageSink.PublishUserMessage(message); err != nil {
		slog.ErrorContext(context.Background(), "failed to publish user message to active flow", "session_id", sessionID, "error", err)
		return false
	}
	return true
}

// CancelFlow cancels the flow for the given session ID
func (r *InMemoryRegistry) CancelFlow(sessionID string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, exists := r.flows[sessionID]
	if !exists {
		return fmt.Errorf("flow not found for session: %s", sessionID)
	}

	if entry.cancel != nil {
		entry.cancel()
	}
	return nil
}
