package agentregistry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain/capabilities"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
)

// newTestDeriver builds a Deriver wired with the two production capabilities
// (memory + knowledge). This mirrors the registry constructed in app.NewServer.
func newTestDeriver() *Deriver {
	return NewDeriver(capabilities.NewRegistry(
		capabilities.MemoryCapability{},
		capabilities.KnowledgeCapability{},
	))
}

// parityFixture pairs an input scenario with a name for test reporting.
type parityFixture struct {
	name  string
	agent configrepo.AgentRecord
	caps  []configrepo.CapabilityRecord
}

// parityFixtures returns 20 representative scenarios covering all known
// agent/capability shapes. The same fixtures are run through both the
// legacy DeriveRuntimeTools function and the new Deriver method to assert
// behavioural parity (zero-regression guarantee for Этап 0).
func parityFixtures() []parityFixture {
	return []parityFixture{
		{
			name:  "01_empty_agent_no_caps",
			agent: configrepo.AgentRecord{},
			caps:  nil,
		},
		{
			name:  "02_builtins_only",
			agent: configrepo.AgentRecord{BuiltinTools: []string{"read_file", "write_file"}},
		},
		{
			name:  "03_builtins_sorted_check",
			agent: configrepo.AgentRecord{BuiltinTools: []string{"zebra", "alpha", "mu"}},
		},
		{
			name: "04_with_custom_tools",
			agent: configrepo.AgentRecord{
				BuiltinTools: []string{"read_file"},
				CustomTools: []configrepo.CustomToolRecord{
					{Name: "custom_one"},
					{Name: "custom_two"},
				},
			},
		},
		{
			name: "05_with_spawn",
			agent: configrepo.AgentRecord{
				BuiltinTools: []string{"read_file"},
				CanSpawn:     []string{"worker", "reporter"},
			},
		},
		{
			name: "06_memory_enabled",
			agent: configrepo.AgentRecord{
				ID:           "agent-06",
				BuiltinTools: []string{"show_structured_output"},
			},
			caps: []configrepo.CapabilityRecord{
				{Type: "memory", Enabled: true},
			},
		},
		{
			name: "07_memory_disabled",
			agent: configrepo.AgentRecord{
				ID:           "agent-07",
				BuiltinTools: []string{"show_structured_output"},
			},
			caps: []configrepo.CapabilityRecord{
				{Type: "memory", Enabled: false},
			},
		},
		{
			name: "08_knowledge_enabled",
			agent: configrepo.AgentRecord{
				ID:           "agent-08",
				BuiltinTools: []string{"show_structured_output"},
			},
			caps: []configrepo.CapabilityRecord{
				{Type: "knowledge", Enabled: true},
			},
		},
		{
			name: "09_knowledge_disabled",
			agent: configrepo.AgentRecord{
				ID:           "agent-09",
				BuiltinTools: []string{"show_structured_output"},
			},
			caps: []configrepo.CapabilityRecord{
				{Type: "knowledge", Enabled: false},
			},
		},
		{
			name: "10_both_caps_enabled",
			agent: configrepo.AgentRecord{
				ID:           "agent-10",
				BuiltinTools: []string{"show_structured_output"},
			},
			caps: []configrepo.CapabilityRecord{
				{Type: "memory", Enabled: true},
				{Type: "knowledge", Enabled: true},
			},
		},
		{
			name: "11_one_cap_enabled_other_disabled",
			agent: configrepo.AgentRecord{
				ID:           "agent-11",
				BuiltinTools: []string{"show_structured_output"},
			},
			caps: []configrepo.CapabilityRecord{
				{Type: "memory", Enabled: true},
				{Type: "knowledge", Enabled: false},
			},
		},
		{
			name: "12_memory_overlap_with_builtin",
			agent: configrepo.AgentRecord{
				ID:           "agent-12",
				BuiltinTools: []string{"memory_store"},
			},
			caps: []configrepo.CapabilityRecord{
				{Type: "memory", Enabled: true},
			},
		},
		{
			name: "13_unknown_cap_type_skipped",
			agent: configrepo.AgentRecord{
				ID:           "agent-13",
				BuiltinTools: []string{"read_file"},
			},
			caps: []configrepo.CapabilityRecord{
				{Type: "non_existent_capability", Enabled: true},
			},
		},
		{
			name: "14_unknown_cap_disabled_skipped",
			agent: configrepo.AgentRecord{
				ID:           "agent-14",
				BuiltinTools: []string{"read_file"},
			},
			caps: []configrepo.CapabilityRecord{
				{Type: "non_existent_capability", Enabled: false},
			},
		},
		{
			name: "15_spawn_and_caps_combined",
			agent: configrepo.AgentRecord{
				ID:           "agent-15",
				BuiltinTools: []string{"show_structured_output"},
				CanSpawn:     []string{"worker"},
			},
			caps: []configrepo.CapabilityRecord{
				{Type: "knowledge", Enabled: true},
			},
		},
		{
			name: "16_full_kitchen_sink",
			agent: configrepo.AgentRecord{
				ID:           "agent-16",
				BuiltinTools: []string{"show_structured_output", "read_file"},
				CustomTools:  []configrepo.CustomToolRecord{{Name: "custom_a"}},
				CanSpawn:     []string{"worker", "checker"},
			},
			caps: []configrepo.CapabilityRecord{
				{Type: "memory", Enabled: true},
				{Type: "knowledge", Enabled: true},
			},
		},
		{
			name: "17_duplicates_across_all_sources",
			agent: configrepo.AgentRecord{
				ID:           "agent-17",
				BuiltinTools: []string{"knowledge_search", "memory_recall"},
				CustomTools:  []configrepo.CustomToolRecord{{Name: "memory_store"}},
				CanSpawn:     []string{"worker"},
			},
			caps: []configrepo.CapabilityRecord{
				{Type: "memory", Enabled: true},
				{Type: "knowledge", Enabled: true},
			},
		},
		{
			name: "18_no_capability_records_explicit_nil",
			agent: configrepo.AgentRecord{
				ID:           "agent-18",
				BuiltinTools: []string{"a", "b", "c"},
			},
			caps: nil,
		},
		{
			name: "19_empty_capability_slice",
			agent: configrepo.AgentRecord{
				ID:           "agent-19",
				BuiltinTools: []string{"read_file"},
			},
			caps: []configrepo.CapabilityRecord{},
		},
		{
			name: "20_only_spawn",
			agent: configrepo.AgentRecord{
				ID:       "agent-20",
				CanSpawn: []string{"worker", "summarizer", "reviewer"},
			},
		},
	}
}

// TestDeriverParity_LegacyVsNew asserts that the new Deriver returns exactly
// the same tool list as the legacy DeriveRuntimeTools free function across
// all 20 fixtures. This is the zero-regression gate for Этап 0 capability
// strategy refactor.
//
// Once the legacy function is removed (Step 5), this test will be deleted
// alongside it. While both exist, this test must remain green.
func TestDeriverParity_LegacyVsNew(t *testing.T) {
	t.Parallel()

	deriver := newTestDeriver()
	ctx := context.Background()

	for _, f := range parityFixtures() {
		f := f
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()

			legacy := DeriveRuntimeTools(f.agent, f.caps)

			got, err := deriver.DeriveRuntimeTools(ctx, f.agent, f.caps)
			require.NoError(t, err, "deriver returned unexpected error")

			assert.Equal(t, legacy, got,
				"legacy and Deriver outputs must match for fixture %q", f.name)
		})
	}
}

// Per-scenario assertions on the Deriver alone (independent of legacy parity).
// These pin down the contract so the legacy function can be deleted in Step 5
// without weakening test coverage.

func TestDeriver_EmptyAgent(t *testing.T) {
	t.Parallel()
	got, err := newTestDeriver().DeriveRuntimeTools(context.Background(),
		configrepo.AgentRecord{}, nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestDeriver_BuiltinsSorted(t *testing.T) {
	t.Parallel()
	got, err := newTestDeriver().DeriveRuntimeTools(context.Background(),
		configrepo.AgentRecord{BuiltinTools: []string{"zebra", "alpha"}}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"alpha", "zebra"}, got)
}

func TestDeriver_MemoryEnabled(t *testing.T) {
	t.Parallel()
	agent := configrepo.AgentRecord{BuiltinTools: []string{"show_structured_output"}}
	caps := []configrepo.CapabilityRecord{{Type: "memory", Enabled: true}}
	got, err := newTestDeriver().DeriveRuntimeTools(context.Background(), agent, caps)
	require.NoError(t, err)
	assert.Equal(t, []string{"memory_recall", "memory_store", "show_structured_output"}, got)
}

func TestDeriver_UnknownCapabilitySkipped(t *testing.T) {
	t.Parallel()
	agent := configrepo.AgentRecord{BuiltinTools: []string{"read_file"}}
	caps := []configrepo.CapabilityRecord{
		{Type: "memory", Enabled: true},
		{Type: "no_such_thing", Enabled: true},
	}
	got, err := newTestDeriver().DeriveRuntimeTools(context.Background(), agent, caps)
	require.NoError(t, err)
	assert.Equal(t, []string{"memory_recall", "memory_store", "read_file"}, got)
}
