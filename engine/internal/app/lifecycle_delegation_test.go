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

// idAwarePool models the real AgentPool contract: WaitForAgent looks an agent up
// by the ID that SpawnAgent handed out and returns its result only for that ID.
// An UNKNOWN id (e.g. a child's answer text mistaken for an agent ID) yields an
// empty AgentCompletionInfo — exactly what AgentPool.WaitForAgent does for an
// agent that never registered in the pool.
type idAwarePool struct {
	realID      string
	childOutput string
	spawnCalls  int
}

func (p *idAwarePool) SpawnAgent(_ context.Context, _ tools.SpawnParams) (string, error) {
	p.spawnCalls++
	return p.realID, nil
}

func (p *idAwarePool) WaitForAgent(_ context.Context, _, agentID string) (tools.AgentCompletionInfo, error) {
	if agentID == p.realID {
		return tools.AgentCompletionInfo{AgentID: agentID, Status: "completed", Result: p.childOutput}, nil
	}
	return tools.AgentCompletionInfo{}, nil
}

func (p *idAwarePool) WaitForAllSessionAgents(_ context.Context, _ string) (tools.WaitResult, error) {
	return tools.WaitResult{AllDone: true}, nil
}
func (p *idAwarePool) HasBlockingWait(_ string) bool { return false }
func (p *idAwarePool) NotifyUserMessage(_, _ string) {}
func (p *idAwarePool) StopAgent(_, _ string) error   { return nil }

// stubInspector satisfies tools.GenericAgentInspector; handleSpawn doesn't use it.
type stubInspector struct{}

func (stubInspector) GetStatusInfo(_, _ string) (*tools.AgentInfo, bool) { return nil, false }
func (stubInspector) GetAllAgentInfos(_ string) []tools.AgentInfo        { return nil }

// TestRelationSpawnTool_RelaysChildOutput reproduces the relation-delegation
// "no output" defect end-to-end: a chat agent spawned via spawn_<name> runs
// synchronously through lifecycle.Manager, which returns the child's ANSWER as
// the SpawnAgent return value. The spawn tool treats that return as an agent ID
// and calls WaitForAgent(<answer text>) — which finds no such agent and yields
// empty — so the delegator reports "completed (no output)" and the child's real
// answer is discarded.
//
// The user-visible symptom: an orchestrator delegates, the specialist answers,
// but the orchestrator relays nothing.
func TestRelationSpawnTool_RelaysChildOutput(t *testing.T) {
	pool := &idAwarePool{realID: "agent-real-1", childOutput: "Refund is 50% (subscribed 21 days ago, between 14 and 30 days)."}
	reader := &mockLifecycleReader{modes: map[string]domain.LifecycleMode{}} // unknown name → chat agent, spawn mode
	manager := lifecycle.NewManager(&poolBasedRunner{pool: pool})
	spawner := NewCompositeAgentSpawner(pool, manager, reader)

	spawnTool := tools.NewSpawnTool("billing-child", "sess-deleg", spawner, stubInspector{})

	out, err := spawnTool.InvokableRun(context.Background(),
		`{"action":"spawn","description":"customer subscribed 21 days ago wants a refund — how much?"}`)
	require.NoError(t, err)

	assert.Contains(t, out, "Refund is 50%",
		"delegator must relay the specialist's actual answer, got: %q", out)
	assert.NotContains(t, out, "no output",
		"child produced an answer but the delegator dropped it: %q", out)
}

// TestCompositeAgentSpawner_ChatAgent_ResultRetrievableByID pins the async
// contract the spawn tool depends on: SpawnAgent returns an agent ID (not the
// answer), and WaitForAgent(id) returns the child's result.
func TestCompositeAgentSpawner_ChatAgent_ResultRetrievableByID(t *testing.T) {
	pool := &idAwarePool{realID: "agent-real-2", childOutput: "CHILD_ANSWER"}
	reader := &mockLifecycleReader{modes: map[string]domain.LifecycleMode{}}
	manager := lifecycle.NewManager(&poolBasedRunner{pool: pool})
	spawner := NewCompositeAgentSpawner(pool, manager, reader)

	ctx := context.Background()
	id, err := spawner.SpawnAgent(ctx, tools.SpawnParams{SessionID: "s1", AgentName: "billing-child", Description: "refund?"})
	require.NoError(t, err)
	assert.NotEqual(t, "CHILD_ANSWER", id, "SpawnAgent must return an agent ID, not the child's answer text")

	info, err := spawner.WaitForAgent(ctx, "s1", id)
	require.NoError(t, err)
	assert.Equal(t, "CHILD_ANSWER", info.Result, "child output must be retrievable via WaitForAgent(spawnID)")
}
