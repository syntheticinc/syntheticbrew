package capability

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/syntheticinc/syntheticbrew/internal/domain/capabilities"
)

// CapabilityReader reads enabled capabilities for an agent.
type CapabilityReader interface {
	ListEnabledByAgent(ctx context.Context, agentName string) ([]CapabilityRecord, error)
}

// CapabilityRecord mirrors the repository record for the service boundary.
type CapabilityRecord struct {
	ID        string
	AgentName string
	Type      string
	Config    map[string]interface{}
	Enabled   bool
}

// CapabilityResolver looks up a strategy for a capability type and resolves
// its runtime tool names. Implemented by *capabilities.Registry.
//
// Defined here (consumer-side) so the injector depends on a minimal contract
// instead of the full Registry surface (ISP).
type CapabilityResolver interface {
	Get(typ string) (capabilities.Capability, bool)
}

// Injector reads agent capabilities and returns the tool names that should be
// auto-injected. Strategy lookup is delegated to a CapabilityResolver — adding
// a new capability requires zero changes to this file.
type Injector struct {
	reader   CapabilityReader
	resolver CapabilityResolver
}

// NewInjector creates a new capability injector that dispatches capability
// types via the provided resolver (typically *capabilities.Registry).
func NewInjector(reader CapabilityReader, resolver CapabilityResolver) *Injector {
	return &Injector{reader: reader, resolver: resolver}
}

// InjectedTools returns the tool names that should be added to an agent based
// on its enabled capabilities. Unknown capability types are logged and skipped
// (forward-compatible: an older binary will not crash on a newer DB row).
func (inj *Injector) InjectedTools(ctx context.Context, agentName string) ([]string, error) {
	caps, err := inj.reader.ListEnabledByAgent(ctx, agentName)
	if err != nil {
		return nil, fmt.Errorf("list capabilities for agent %q: %w", agentName, err)
	}

	var tools []string
	seen := make(map[string]bool)

	for _, c := range caps {
		impl, ok := inj.resolver.Get(c.Type)
		if !ok {
			slog.WarnContext(ctx, "capability injector: unknown capability type, skipping",
				"agent", agentName,
				"type", c.Type,
			)
			continue
		}
		names, err := impl.Tools(ctx, agentName, c.Config)
		if err != nil {
			return nil, fmt.Errorf("resolve tools for capability %q on agent %q: %w", c.Type, agentName, err)
		}
		for _, toolName := range names {
			if !seen[toolName] {
				seen[toolName] = true
				tools = append(tools, toolName)
			}
		}
	}

	if len(tools) > 0 {
		slog.InfoContext(ctx, "capability injector: injecting tools", "agent", agentName, "tools", tools)
	}

	return tools, nil
}
