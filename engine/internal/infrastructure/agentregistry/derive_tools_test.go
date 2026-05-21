package agentregistry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
)

// scenario 1: no capabilities, no can_spawn → base tools only (sorted)
func TestDeriveRuntimeTools_BaseOnly(t *testing.T) {
	agent := configrepo.AgentRecord{
		BuiltinTools: []string{"read_file", "write_file"},
	}
	got := DeriveRuntimeTools(agent, nil)
	assert.Equal(t, []string{"read_file", "write_file"}, got)
}

// scenario 2: memory capability enabled → base + memory_recall + memory_store
func TestDeriveRuntimeTools_MemoryEnabled(t *testing.T) {
	agent := configrepo.AgentRecord{
		BuiltinTools: []string{"show_structured_output"},
	}
	caps := []configrepo.CapabilityRecord{
		{Type: "memory", Enabled: true},
	}
	got := DeriveRuntimeTools(agent, caps)
	assert.Equal(t, []string{"memory_recall", "memory_store", "show_structured_output"}, got)
}

// scenario 3: memory capability disabled → base only
func TestDeriveRuntimeTools_MemoryDisabled(t *testing.T) {
	agent := configrepo.AgentRecord{
		BuiltinTools: []string{"show_structured_output"},
	}
	caps := []configrepo.CapabilityRecord{
		{Type: "memory", Enabled: false},
	}
	got := DeriveRuntimeTools(agent, caps)
	assert.Equal(t, []string{"show_structured_output"}, got)
}

// scenario 4: knowledge enabled + can_spawn=[worker] → base + knowledge_search + spawn_worker
func TestDeriveRuntimeTools_KnowledgeAndSpawn(t *testing.T) {
	agent := configrepo.AgentRecord{
		BuiltinTools: []string{"show_structured_output"},
		CanSpawn:     []string{"worker"},
	}
	caps := []configrepo.CapabilityRecord{
		{Type: "knowledge", Enabled: true},
	}
	got := DeriveRuntimeTools(agent, caps)
	assert.Equal(t, []string{"knowledge_search", "show_structured_output", "spawn_worker"}, got)
}

// scenario 5: overlap — agent.BuiltinTools already contains memory_store,
// memory capability present → memory_store appears exactly once
func TestDeriveRuntimeTools_NoDeduplication(t *testing.T) {
	agent := configrepo.AgentRecord{
		BuiltinTools: []string{"memory_store"},
	}
	caps := []configrepo.CapabilityRecord{
		{Type: "memory", Enabled: true},
	}
	got := DeriveRuntimeTools(agent, caps)
	// memory_store is in base; memory cap also injects memory_recall + memory_store
	// but dedup means memory_store only once
	assert.Equal(t, []string{"memory_recall", "memory_store"}, got)
}

// scenario 6: order stability — shuffle CanSpawn and capabilities, output is always sorted
func TestDeriveRuntimeTools_DeterministicOrder(t *testing.T) {
	agent1 := configrepo.AgentRecord{
		BuiltinTools: []string{"show_structured_output"},
		CanSpawn:     []string{"zebra", "alpha"},
	}
	caps1 := []configrepo.CapabilityRecord{
		{Type: "knowledge", Enabled: true},
		{Type: "memory", Enabled: true},
	}

	agent2 := configrepo.AgentRecord{
		BuiltinTools: []string{"show_structured_output"},
		CanSpawn:     []string{"alpha", "zebra"},
	}
	caps2 := []configrepo.CapabilityRecord{
		{Type: "memory", Enabled: true},
		{Type: "knowledge", Enabled: true},
	}

	got1 := DeriveRuntimeTools(agent1, caps1)
	got2 := DeriveRuntimeTools(agent2, caps2)
	assert.Equal(t, got1, got2, "output must be deterministic regardless of input order")
	// Verify it is actually sorted
	for i := 1; i < len(got1); i++ {
		assert.LessOrEqual(t, got1[i-1], got1[i], "tools must be in sorted order")
	}
}
