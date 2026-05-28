// Package capabilities defines the Strategy pattern for agent capabilities.
// Each capability (memory, knowledge, knowledge_graphs, ...) is implemented
// as one struct in its own file. The Registry holds all registered strategies
// for O(1) lookup at runtime.
//
// Adding a new capability requires ONE new file in this package plus ONE line
// in app.NewServer to register it. No modifications to existing files.
package capabilities

import (
	"context"
	"sort"
)

// Capability is a self-describing extension that contributes runtime tools
// to an agent. Each concrete capability is implemented as a single struct
// in capabilities/{name}.go.
//
// Static capabilities (memory, knowledge) ignore ctx + agentID + config and
// return a constant list. Dynamic capabilities (knowledge_graphs) read config
// bundles and call per-tenant providers.
type Capability interface {
	// Type returns the stable identifier persisted in the capabilities table
	// (e.g. "memory", "knowledge", "knowledge_graphs").
	Type() string

	// Validate checks the per-agent config blob (capabilities.config JSONB).
	// Returns nil if the config is valid for this capability type.
	Validate(config map[string]any) error

	// Tools resolves the tool names this capability contributes for the
	// given agent under the given context. Implementations may consult
	// per-tenant state via the context (tenant_id is in context).
	Tools(ctx context.Context, agentID string, config map[string]any) ([]string, error)
}

// Registry holds all registered capabilities. Lookup is O(1) by Type().
// Constructed once in app.NewServer and passed via DI.
type Registry struct {
	items map[string]Capability
}

// NewRegistry constructs a Registry pre-populated with the given capabilities.
// Later capabilities can be added via Register.
func NewRegistry(caps ...Capability) *Registry {
	r := &Registry{items: make(map[string]Capability, len(caps))}
	for _, c := range caps {
		r.items[c.Type()] = c
	}
	return r
}

// Register adds a capability to the registry. If a capability with the same
// Type() is already registered, it is replaced.
func (r *Registry) Register(c Capability) {
	r.items[c.Type()] = c
}

// Get returns the capability registered for the given type, or false if
// no capability is registered for that type.
func (r *Registry) Get(typ string) (Capability, bool) {
	c, ok := r.items[typ]
	return c, ok
}

// AllTypes returns the sorted list of registered capability types.
// Deterministic ordering for tests and admin UI listings.
func (r *Registry) AllTypes() []string {
	out := make([]string, 0, len(r.items))
	for t := range r.items {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
