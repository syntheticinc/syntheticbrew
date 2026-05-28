package agentregistry

import (
	"context"

	"github.com/syntheticinc/syntheticbrew/internal/domain/capabilities"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
)

// defaultDeriver is the package-level fallback Deriver used by the legacy
// DeriveRuntimeTools free function. Constructed once with the built-in
// memory + knowledge capabilities so callers without injected DI still
// produce the same tool list.
//
// Production wiring (app.NewServer) bypasses this fallback by calling
// Manager.SetDeriver with a Deriver constructed over the shared registry.
// This package-level instance exists only for tests and legacy callers
// that haven't migrated to the Deriver-based API yet.
var defaultDeriver = NewDeriver(capabilities.NewRegistry(
	capabilities.MemoryCapability{},
	capabilities.KnowledgeCapability{},
))

// DeriveRuntimeTools is the legacy free-function entry point. New code should
// construct a Deriver via NewDeriver and inject it. This wrapper exists so
// untouched callers continue compiling during the capability strategy
// refactor (Этап 0).
//
// Deprecated: use Deriver.DeriveRuntimeTools through DI. Will be removed
// once all callers (including tests) are migrated.
func DeriveRuntimeTools(agent configrepo.AgentRecord, caps []configrepo.CapabilityRecord) []string {
	tools, err := defaultDeriver.DeriveRuntimeTools(context.Background(), agent, caps)
	if err != nil {
		// Built-in capabilities never return errors; this branch is
		// defensive against future strategies misbehaving in tests.
		return nil
	}
	return tools
}
