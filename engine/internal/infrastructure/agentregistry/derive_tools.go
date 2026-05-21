package agentregistry

import (
	"sort"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
)

// DeriveRuntimeTools returns the sorted, deduplicated tool names available to
// the agent at execution time. Deterministic: same inputs → same output order.
func DeriveRuntimeTools(agent configrepo.AgentRecord, capabilities []configrepo.CapabilityRecord) []string {
	seen := make(map[string]bool)
	var tools []string

	add := func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
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

	for _, c := range capabilities {
		if !c.Enabled {
			continue
		}
		switch c.Type {
		case "memory":
			add("memory_recall")
			add("memory_store")
		case "knowledge":
			add("knowledge_search")
		}
	}

	sort.Strings(tools)
	return tools
}
