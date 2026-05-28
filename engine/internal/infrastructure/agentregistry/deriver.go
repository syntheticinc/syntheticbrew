package agentregistry

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/syntheticinc/syntheticbrew/internal/domain/capabilities"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
)

// Deriver resolves the sorted, deduplicated set of runtime tools available
// to an agent. It composes capability strategies via the capabilities.Registry
// instead of dispatching capability types via a switch statement.
//
// Adding a new capability does NOT require changes to this file: the new
// capability registers itself in capabilities.Registry at server startup.
type Deriver struct {
	caps *capabilities.Registry
}

// NewDeriver constructs a Deriver backed by the given capability registry.
func NewDeriver(caps *capabilities.Registry) *Deriver {
	return &Deriver{caps: caps}
}

// DeriveRuntimeTools returns the sorted, deduplicated tool names available to
// the agent at execution time. Deterministic: same inputs → same output order.
//
// Sources, in order:
//  1. agent.BuiltinTools
//  2. agent.CustomTools[*].Name
//  3. "spawn_" + each agent.CanSpawn entry
//  4. tools contributed by each enabled capability via the Registry
//
// Unknown capability types are logged and skipped (forward-compatible: an
// older binary will not crash on a newer DB row).
//
// Returns error if any registered capability fails to resolve its tools.
func (d *Deriver) DeriveRuntimeTools(
	ctx context.Context,
	agent configrepo.AgentRecord,
	caps []configrepo.CapabilityRecord,
) ([]string, error) {
	seen := make(map[string]struct{})
	var tools []string

	add := func(name string) {
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		tools = append(tools, name)
	}

	for _, t := range agent.BuiltinTools {
		add(t)
	}
	for _, ct := range agent.CustomTools {
		add(ct.Name)
	}
	for _, name := range agent.CanSpawn {
		add("spawn_" + name)
	}

	for _, c := range caps {
		if !c.Enabled {
			continue
		}
		impl, ok := d.caps.Get(string(c.Type))
		if !ok {
			slog.WarnContext(ctx, "unknown capability type, skipping",
				"type", c.Type,
				"agent_id", agent.ID,
			)
			continue
		}
		names, err := impl.Tools(ctx, agent.ID, c.Config)
		if err != nil {
			return nil, fmt.Errorf("resolve tools for capability %s: %w", c.Type, err)
		}
		for _, n := range names {
			add(n)
		}
	}

	sort.Strings(tools)
	return tools, nil
}
