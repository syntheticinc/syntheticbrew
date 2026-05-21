package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools"
	"github.com/syntheticinc/syntheticbrew/internal/service/lifecycle"
)

// newTestManager creates a lifecycle.Manager with a poolBasedRunner wrapping the mock spawner.
func newTestManager(pool *mockAgentSpawner) *lifecycle.Manager {
	return lifecycle.NewManager(&poolBasedRunner{pool: pool})
}

// mockAgentSpawner implements agentSpawnerWaiter for testing.
type mockAgentSpawner struct {
	spawnCalls  []tools.SpawnParams
	spawnErr    error
	spawnID     string
	waitResult  string
	waitErr     error
}

func (m *mockAgentSpawner) SpawnAgent(_ context.Context, params tools.SpawnParams) (string, error) {
	m.spawnCalls = append(m.spawnCalls, params)
	if m.spawnErr != nil {
		return "", m.spawnErr
	}
	return m.spawnID, nil
}

func (m *mockAgentSpawner) WaitForAgent(_ context.Context, _, _ string) (tools.AgentCompletionInfo, error) {
	return tools.AgentCompletionInfo{Result: m.waitResult}, m.waitErr
}

func (m *mockAgentSpawner) WaitForAllSessionAgents(_ context.Context, _ string) (tools.WaitResult, error) {
	return tools.WaitResult{AllDone: true}, nil
}

func (m *mockAgentSpawner) HasBlockingWait(_ string) bool { return false }
func (m *mockAgentSpawner) NotifyUserMessage(_, _ string) {}
func (m *mockAgentSpawner) StopAgent(_ string) error       { return nil }

// mockLifecycleReader implements AgentLifecycleReader for testing.
type mockLifecycleReader struct {
	modes       map[string]domain.LifecycleMode
	maxContexts map[string]int
}

func (m *mockLifecycleReader) GetLifecycleMode(_ context.Context, agentName string) domain.LifecycleMode {
	if mode, ok := m.modes[agentName]; ok {
		return mode
	}
	return domain.LifecycleModeSpawn
}

func (m *mockLifecycleReader) GetMaxContextSize(_ context.Context, agentName string) int {
	if size, ok := m.maxContexts[agentName]; ok {
		return size
	}
	return 16000
}

// TestCompositeAgentSpawner_SpawnMode_CodeAgent_DelegatesToPool verifies that
// spawn-mode code agents (coder/researcher/reviewer) bypass lifecycle.Manager
// and go directly to the gRPC pool.
func TestCompositeAgentSpawner_SpawnMode_CodeAgent_DelegatesToPool(t *testing.T) {
	pool := &mockAgentSpawner{spawnID: "agent-123"}
	reader := &mockLifecycleReader{
		modes: map[string]domain.LifecycleMode{
			"coder": domain.LifecycleModeSpawn,
		},
	}
	manager := newTestManager(pool)
	spawner := NewCompositeAgentSpawner(pool, manager, reader)

	ctx := context.Background()
	params := tools.SpawnParams{
		SessionID:   "sess-1",
		AgentName:   "coder",
		Description: "do something",
		Blocking:    true,
	}

	result, err := spawner.SpawnAgent(ctx, params)
	require.NoError(t, err)
	// Code agents: CompositeAgentSpawner calls pool.SpawnAgent directly → returns spawnID.
	assert.Equal(t, "agent-123", result)
	assert.Len(t, pool.spawnCalls, 1)
	assert.Equal(t, "coder", pool.spawnCalls[0].AgentName)
}

// TestCompositeAgentSpawner_UnknownChatAgent_UsesManager verifies that unknown
// agents (treated as chat agents) are routed through lifecycle.Manager even in
// spawn mode, ensuring they don't require a gRPC session proxy.
func TestCompositeAgentSpawner_UnknownChatAgent_UsesManager(t *testing.T) {
	pool := &mockAgentSpawner{spawnID: "agent-456", waitResult: "chat-output"}
	reader := &mockLifecycleReader{
		modes: map[string]domain.LifecycleMode{},
	}
	manager := newTestManager(pool)
	spawner := NewCompositeAgentSpawner(pool, manager, reader)

	ctx := context.Background()
	params := tools.SpawnParams{
		SessionID:   "sess-1",
		AgentName:   "unknown-agent",
		Description: "task",
	}

	result, err := spawner.SpawnAgent(ctx, params)
	require.NoError(t, err)
	// Chat agents go through lifecycle.Manager → poolBasedRunner.runCodeAgent (no chatFactory wired).
	// runCodeAgent: SpawnAgent (returns spawnID) + WaitForAgent (returns waitResult).
	assert.Equal(t, "chat-output", result)
	assert.Len(t, pool.spawnCalls, 1)
}

func TestCompositeAgentSpawner_PersistentMode_UsesManager(t *testing.T) {
	pool := &mockAgentSpawner{spawnID: "agent-789", waitResult: "agent-789-output"}
	reader := &mockLifecycleReader{
		modes: map[string]domain.LifecycleMode{
			"persistent-agent": domain.LifecycleModePersistent,
		},
		maxContexts: map[string]int{
			"persistent-agent": 32000,
		},
	}
	manager := newTestManager(pool)
	spawner := NewCompositeAgentSpawner(pool, manager, reader)

	ctx := context.Background()
	params := tools.SpawnParams{
		SessionID:   "sess-2",
		AgentName:   "persistent-agent",
		Description: "persistent task",
		Blocking:    true,
	}

	result, err := spawner.SpawnAgent(ctx, params)
	require.NoError(t, err)
	assert.Equal(t, "agent-789-output", result)

	// Verify the manager tracked the instance
	instance, ok := manager.GetInstance(ctx, "persistent-agent", "sess-2")
	require.True(t, ok)
	assert.Equal(t, domain.LifecycleModePersistent, instance.Mode)
	assert.Equal(t, 1, instance.TasksHandled)
}

func TestCompositeAgentSpawner_PersistentMode_AccumulatesContext(t *testing.T) {
	pool := &mockAgentSpawner{spawnID: "agent-acc", waitResult: "some output"}
	reader := &mockLifecycleReader{
		modes: map[string]domain.LifecycleMode{
			"persistent-agent": domain.LifecycleModePersistent,
		},
	}
	manager := newTestManager(pool)
	spawner := NewCompositeAgentSpawner(pool, manager, reader)

	ctx := context.Background()

	// First task
	_, err := spawner.SpawnAgent(ctx, tools.SpawnParams{
		SessionID:   "sess-3",
		AgentName:   "persistent-agent",
		Description: "task 1",
	})
	require.NoError(t, err)

	// Second task — context should accumulate
	_, err = spawner.SpawnAgent(ctx, tools.SpawnParams{
		SessionID:   "sess-3",
		AgentName:   "persistent-agent",
		Description: "task 2",
	})
	require.NoError(t, err)

	instance, ok := manager.GetInstance(ctx, "persistent-agent", "sess-3")
	require.True(t, ok)
	assert.Equal(t, 2, instance.TasksHandled)
	assert.Greater(t, instance.ContextTokens, 0)
}

func TestCompositeAgentSpawner_DelegateMethods(t *testing.T) {
	pool := &mockAgentSpawner{spawnID: "x"}
	reader := &mockLifecycleReader{modes: map[string]domain.LifecycleMode{}}
	manager := newTestManager(pool)
	spawner := NewCompositeAgentSpawner(pool, manager, reader)

	// WaitForAllSessionAgents
	result, err := spawner.WaitForAllSessionAgents(context.Background(), "sess-1")
	require.NoError(t, err)
	assert.True(t, result.AllDone)

	// HasBlockingWait
	assert.False(t, spawner.HasBlockingWait("sess-1"))

	// StopAgent
	assert.NoError(t, spawner.StopAgent("agent-1"))
}

func TestPoolBasedRunner_RunAgent(t *testing.T) {
	pool := &mockAgentSpawner{spawnID: "run-123", waitResult: "agent-output"}
	runner := &poolBasedRunner{pool: pool}

	result, err := runner.RunAgent(context.Background(), "agent-a", "task input", "sess-1", nil)
	require.NoError(t, err)
	assert.Equal(t, "agent-output", result) // returns actual output, not agentID
	require.Len(t, pool.spawnCalls, 1)
	assert.Equal(t, "agent-a", pool.spawnCalls[0].AgentName)
	assert.Equal(t, "task input", pool.spawnCalls[0].Description)
	assert.Equal(t, "sess-1", pool.spawnCalls[0].SessionID)
	assert.False(t, pool.spawnCalls[0].Blocking) // agent runs on session ctx; we wait via WaitForAgent
}
