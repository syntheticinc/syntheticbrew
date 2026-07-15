package tools

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agentregistry"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
)

// stubTool is a minimal tool implementation for testing.
type stubTool struct {
	name string
}

func (t *stubTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name}, nil
}

func (t *stubTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	return "ok:" + t.name, nil
}

func TestBuiltinToolStore_RegisterAndGet(t *testing.T) {
	store := NewBuiltinToolStore()

	factory := func(deps ToolDependencies) tool.InvokableTool {
		return &stubTool{name: "test_tool"}
	}
	store.Register("test_tool", factory)

	got, ok := store.Get("test_tool")
	require.True(t, ok)
	require.NotNil(t, got)

	instance := got(ToolDependencies{})
	result, err := instance.InvokableRun(context.Background(), "")
	require.NoError(t, err)
	assert.Equal(t, "ok:test_tool", result)
}

func TestBuiltinToolStore_GetUnknown(t *testing.T) {
	store := NewBuiltinToolStore()

	_, ok := store.Get("nonexistent")
	assert.False(t, ok)
}

func TestBuiltinToolStore_Names(t *testing.T) {
	store := NewBuiltinToolStore()
	noopFactory := func(deps ToolDependencies) tool.InvokableTool { return &stubTool{} }

	store.Register("charlie", noopFactory)
	store.Register("alpha", noopFactory)
	store.Register("bravo", noopFactory)

	names := store.Names()
	assert.Equal(t, []string{"alpha", "bravo", "charlie"}, names)
}

func TestBuiltinToolStore_NamesEmpty(t *testing.T) {
	store := NewBuiltinToolStore()
	names := store.Names()
	assert.Empty(t, names)
}

func TestAgentToolResolver_ResolveForAgent_Whitelist(t *testing.T) {
	store := NewBuiltinToolStore()
	store.Register("tool_a", func(deps ToolDependencies) tool.InvokableTool {
		return &stubTool{name: "tool_a"}
	})
	store.Register("tool_b", func(deps ToolDependencies) tool.InvokableTool {
		return &stubTool{name: "tool_b"}
	})
	store.Register("tool_c", func(deps ToolDependencies) tool.InvokableTool {
		return &stubTool{name: "tool_c"}
	})

	resolver := NewAgentToolResolver(store)
	agent := &agentregistry.RegisteredAgent{
		Record: configrepo.AgentRecord{
			Name:         "test_agent",
			BuiltinTools: []string{"tool_a", "tool_c"},
		},
		// DerivedTools is the resolver's source of truth (pre-computed by DeriveRuntimeTools).
		DerivedTools: []string{"tool_a", "tool_c"},
	}

	tools, err := resolver.ResolveForAgent(context.Background(), ResolveContext{
		Agent: agent,
		Deps:  ToolDependencies{},
	})
	require.NoError(t, err)
	require.Len(t, tools, 2)

	// Verify tools are in DerivedTools order (sorted by DeriveRuntimeTools).
	info0, _ := tools[0].Info(context.Background())
	info1, _ := tools[1].Info(context.Background())
	assert.Equal(t, "tool_a", info0.Name)
	assert.Equal(t, "tool_c", info1.Name)
}

// TestAgentToolResolver_ResolveForAgent_ManagementToolGate proves the security
// gate: a non-system (user-provisioned) agent must NOT resolve management-plane
// tools even if its BuiltinTools list names them, while a system agent still can.
// This closes the privilege-escalation path where a provisioned agent granted
// admin_* tools would execute them server-side, bypassing scope checks.
func TestAgentToolResolver_ResolveForAgent_ManagementToolGate(t *testing.T) {
	store := NewBuiltinToolStore()
	store.Register("admin_delete_agent", func(deps ToolDependencies) tool.InvokableTool {
		return &stubTool{name: "admin_delete_agent"}
	})
	store.Register("read_file", func(deps ToolDependencies) tool.InvokableTool {
		return &stubTool{name: "read_file"}
	})
	resolver := NewAgentToolResolver(store)

	// Non-system agent: admin_delete_agent is skipped, read_file resolves.
	userAgent := &agentregistry.RegisteredAgent{
		Record: configrepo.AgentRecord{
			Name:         "user_agent",
			IsSystem:     false,
			BuiltinTools: []string{"admin_delete_agent", "read_file"},
		},
		DerivedTools: []string{"admin_delete_agent", "read_file"},
	}
	tools, err := resolver.ResolveForAgent(context.Background(), ResolveContext{Agent: userAgent, Deps: ToolDependencies{}})
	require.NoError(t, err)
	require.Len(t, tools, 1, "non-system agent must not resolve admin_delete_agent")
	info, _ := tools[0].Info(context.Background())
	assert.Equal(t, "read_file", info.Name)

	// System agent (e.g. builder-assistant): admin_delete_agent still resolves.
	sysAgent := &agentregistry.RegisteredAgent{
		Record: configrepo.AgentRecord{
			Name:         "builder-assistant",
			IsSystem:     true,
			BuiltinTools: []string{"admin_delete_agent", "read_file"},
		},
		DerivedTools: []string{"admin_delete_agent", "read_file"},
	}
	sysTools, err := resolver.ResolveForAgent(context.Background(), ResolveContext{Agent: sysAgent, Deps: ToolDependencies{}})
	require.NoError(t, err)
	require.Len(t, sysTools, 2, "system agent must keep admin tools")
}

func TestAgentToolResolver_ResolveForAgent_UnknownTool(t *testing.T) {
	store := NewBuiltinToolStore()
	store.Register("tool_a", func(deps ToolDependencies) tool.InvokableTool {
		return &stubTool{name: "tool_a"}
	})

	resolver := NewAgentToolResolver(store)
	agent := &agentregistry.RegisteredAgent{
		Record: configrepo.AgentRecord{
			Name:         "test_agent",
			BuiltinTools: []string{"tool_a", "unknown_tool"},
		},
		// DerivedTools is the resolver's source of truth. Unregistered tools in
		// DerivedTools are warned and skipped (not returned as errors) because
		// capability-injected names (memory_recall, etc.) may appear before their
		// factory is registered.
		DerivedTools: []string{"tool_a", "unknown_tool"},
	}

	tools, err := resolver.ResolveForAgent(context.Background(), ResolveContext{
		Agent: agent,
		Deps:  ToolDependencies{},
	})
	require.NoError(t, err)
	require.Len(t, tools, 1)
	info, _ := tools[0].Info(context.Background())
	assert.Equal(t, "tool_a", info.Name)
}

func TestAgentToolResolver_ResolveForAgent_EmptyWhitelist(t *testing.T) {
	store := NewBuiltinToolStore()
	store.Register("tool_a", func(deps ToolDependencies) tool.InvokableTool {
		return &stubTool{name: "tool_a"}
	})

	resolver := NewAgentToolResolver(store)
	agent := &agentregistry.RegisteredAgent{
		Record: configrepo.AgentRecord{
			Name:         "test_agent",
			BuiltinTools: nil,
		},
	}

	tools, err := resolver.ResolveForAgent(context.Background(), ResolveContext{
		Agent: agent,
		Deps:  ToolDependencies{},
	})
	require.NoError(t, err)
	assert.Empty(t, tools)
}

func TestAgentToolResolver_ResolveForAgent_PassesDeps(t *testing.T) {
	store := NewBuiltinToolStore()

	var capturedSessionID string
	store.Register("dep_tool", func(deps ToolDependencies) tool.InvokableTool {
		capturedSessionID = deps.SessionID
		return &stubTool{name: "dep_tool"}
	})

	resolver := NewAgentToolResolver(store)
	agent := &agentregistry.RegisteredAgent{
		Record: configrepo.AgentRecord{
			Name:         "test_agent",
			BuiltinTools: []string{"dep_tool"},
		},
		// DerivedTools is the resolver's source of truth.
		DerivedTools: []string{"dep_tool"},
	}

	_, err := resolver.ResolveForAgent(context.Background(), ResolveContext{
		Agent: agent,
		Deps:  ToolDependencies{SessionID: "session-42"},
	})
	require.NoError(t, err)
	assert.Equal(t, "session-42", capturedSessionID)
}

// resolvedNames extracts the tool names from a resolved tool slice.
func resolvedNames(t *testing.T, tools []tool.InvokableTool) []string {
	t.Helper()
	names := make([]string, 0, len(tools))
	for _, tl := range tools {
		info, err := tl.Info(context.Background())
		require.NoError(t, err)
		names = append(names, info.Name)
	}
	return names
}

// TestAgentToolResolver_Resolve_SpawnAgentGatedOnCanSpawn proves that the generic
// spawn_agent delegation tool (legacy Resolve path) is only offered to agents that
// declare spawn targets. A leaf agent (empty CanSpawn) has nothing to delegate to,
// so offering spawn_agent only tempts the model into inventing phantom agents
// instead of using its own tools — the exact break on a grounded support agent.
func TestAgentToolResolver_Resolve_SpawnAgentGatedOnCanSpawn(t *testing.T) {
	newResolver := func() *AgentToolResolver {
		r := NewAgentToolResolver(NewBuiltinToolStore())
		r.SetSpawner(&mockGenericSpawner{}, &mockGenericInspector{})
		return r
	}

	// Leaf agent: spawner wired but no spawn targets → no spawn_agent.
	leaf, err := newResolver().Resolve(context.Background(), nil, ToolDependencies{
		AgentName: "acme-support",
		SessionID: "s1",
	})
	require.NoError(t, err)
	assert.NotContains(t, resolvedNames(t, leaf), "spawn_agent",
		"leaf agent with empty CanSpawn must NOT be offered the generic spawn_agent tool")

	// Delegator agent: declares a spawn target → gets spawn_agent + spawn_<target>.
	delegator, err := newResolver().Resolve(context.Background(), nil, ToolDependencies{
		AgentName: "orchestrator",
		SessionID: "s2",
		CanSpawn:  []string{"coder"},
	})
	require.NoError(t, err)
	names := resolvedNames(t, delegator)
	assert.Contains(t, names, "spawn_agent",
		"agent with CanSpawn targets must keep the generic spawn_agent tool")
	assert.Contains(t, names, "spawn_coder",
		"agent with CanSpawn targets must keep per-target spawn tools")
}

// TestAgentToolResolver_ResolveForAgent_SpawnAgentGatedOnCanSpawn mirrors the gate
// on the modern ResolveForAgent path: spawn_agent listed in the agent's builtin
// tools is still withheld unless the agent declares CanSpawn targets.
func TestAgentToolResolver_ResolveForAgent_SpawnAgentGatedOnCanSpawn(t *testing.T) {
	resolver := NewAgentToolResolver(NewBuiltinToolStore())
	spawner := &mockGenericSpawner{}
	inspector := &mockGenericInspector{}

	// Leaf agent: spawn_agent in DerivedTools but empty CanSpawn → withheld.
	leaf := &agentregistry.RegisteredAgent{
		Record:       configrepo.AgentRecord{Name: "acme-support"},
		DerivedTools: []string{"spawn_agent"},
	}
	leafTools, err := resolver.ResolveForAgent(context.Background(), ResolveContext{
		Agent:     leaf,
		Deps:      ToolDependencies{SessionID: "s1"},
		Spawner:   spawner,
		Inspector: inspector,
	})
	require.NoError(t, err)
	assert.NotContains(t, resolvedNames(t, leafTools), "spawn_agent",
		"leaf agent with empty CanSpawn must NOT resolve spawn_agent even if in DerivedTools")

	// Delegator agent: CanSpawn declared → spawn_agent resolves.
	delegator := &agentregistry.RegisteredAgent{
		Record:       configrepo.AgentRecord{Name: "orchestrator", CanSpawn: []string{"coder"}},
		DerivedTools: []string{"spawn_agent"},
	}
	delegatorTools, err := resolver.ResolveForAgent(context.Background(), ResolveContext{
		Agent:     delegator,
		Deps:      ToolDependencies{SessionID: "s2"},
		Spawner:   spawner,
		Inspector: inspector,
	})
	require.NoError(t, err)
	assert.Contains(t, resolvedNames(t, delegatorTools), "spawn_agent",
		"agent with CanSpawn targets must resolve the generic spawn_agent tool")
}
