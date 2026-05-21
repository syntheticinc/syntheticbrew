package lifecycle

import (
	"context"
	"fmt"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

type mockRunner struct {
	outputs map[string]string
	err     error
}

func (m *mockRunner) RunAgent(_ context.Context, agentName, input, sessionID string, _ domain.AgentEventStream) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	if out, ok := m.outputs[agentName]; ok {
		return out, nil
	}
	return fmt.Sprintf("output from %s", agentName), nil
}

func TestManager_SpawnAgent_ContextDestroyed(t *testing.T) {
	runner := &mockRunner{outputs: map[string]string{"agent-a": "result-1"}}
	mgr := NewManager(runner)

	// First execution
	out, err := mgr.ExecuteTask(context.Background(), "agent-a", "session-1", "task 1",
		domain.LifecycleModeSpawn, 16000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "result-1" {
		t.Errorf("expected result-1, got %q", out)
	}

	// After spawn, instance should be cleaned up
	_, exists := mgr.GetInstance(context.Background(), "agent-a", "session-1")
	if exists {
		t.Error("expected spawn instance to be cleaned up after task")
	}

	// Context should be zero
	if mgr.ContextSize(context.Background(), "agent-a", "session-1") != 0 {
		t.Error("expected zero context after spawn")
	}
}

func TestManager_PersistentAgent_ContextPreserved(t *testing.T) {
	runner := &mockRunner{outputs: map[string]string{"agent-a": "result-1"}}
	mgr := NewManager(runner)

	// First execution
	_, err := mgr.ExecuteTask(context.Background(), "agent-a", "session-1", "task 1",
		domain.LifecycleModePersistent, 16000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Instance should still exist
	inst, exists := mgr.GetInstance(context.Background(), "agent-a", "session-1")
	if !exists {
		t.Fatal("expected persistent instance to exist")
	}
	if inst.State() != domain.LifecycleReady {
		t.Errorf("expected ready state, got %s", inst.State())
	}

	// Context should be preserved
	if mgr.ContextSize(context.Background(), "agent-a", "session-1") != 2 { // "User: task 1" + "Agent: result-1"
		t.Errorf("expected 2 context entries, got %d", mgr.ContextSize(context.Background(), "agent-a", "session-1"))
	}

	// Second execution — should have context
	runner.outputs["agent-a"] = "result-2"
	_, err = mgr.ExecuteTask(context.Background(), "agent-a", "session-1", "task 2",
		domain.LifecycleModePersistent, 16000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mgr.ContextSize(context.Background(), "agent-a", "session-1") != 4 {
		t.Errorf("expected 4 context entries, got %d", mgr.ContextSize(context.Background(), "agent-a", "session-1"))
	}
}

func TestManager_PersistentAgent_MultiTask(t *testing.T) {
	runner := &mockRunner{outputs: map[string]string{"agent-a": "ok"}}
	mgr := NewManager(runner)

	for i := 0; i < 3; i++ {
		_, err := mgr.ExecuteTask(context.Background(), "agent-a", "session-1", fmt.Sprintf("task %d", i),
			domain.LifecycleModePersistent, 16000, nil)
		if err != nil {
			t.Fatalf("task %d: unexpected error: %v", i, err)
		}
	}

	inst, _ := mgr.GetInstance(context.Background(), "agent-a", "session-1")
	if inst.TasksHandled != 3 {
		t.Errorf("expected 3 tasks handled, got %d", inst.TasksHandled)
	}
}

func TestManager_ResetAgent(t *testing.T) {
	runner := &mockRunner{outputs: map[string]string{"agent-a": "ok"}}
	mgr := NewManager(runner)

	mgr.ExecuteTask(context.Background(), "agent-a", "session-1", "task 1",
		domain.LifecycleModePersistent, 16000, nil)

	mgr.ResetAgent(context.Background(), "agent-a", "session-1")

	if mgr.ContextSize(context.Background(), "agent-a", "session-1") != 0 {
		t.Error("expected zero context after reset")
	}
}

func TestManager_SpawnAgent_ReSpawn(t *testing.T) {
	callCount := 0
	runner := &mockRunner{outputs: map[string]string{"agent-a": "ok"}}
	_ = callCount

	mgr := NewManager(runner)

	// First spawn
	mgr.ExecuteTask(context.Background(), "agent-a", "session-1", "task 1",
		domain.LifecycleModeSpawn, 16000, nil)

	// Second spawn — fresh instance
	mgr.ExecuteTask(context.Background(), "agent-a", "session-1", "task 2",
		domain.LifecycleModeSpawn, 16000, nil)

	// Instance should be gone (spawn cleans up)
	_, exists := mgr.GetInstance(context.Background(), "agent-a", "session-1")
	if exists {
		t.Error("expected no instance after spawn task")
	}
}

func TestManager_AgentFailure(t *testing.T) {
	runner := &mockRunner{err: fmt.Errorf("LLM error")}
	mgr := NewManager(runner)

	_, err := mgr.ExecuteTask(context.Background(), "agent-a", "session-1", "task",
		domain.LifecycleModeSpawn, 16000, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

// inputCapturingRunner records the full input passed to RunAgent.
type inputCapturingRunner struct {
	capturedInputs []string
}

func (r *inputCapturingRunner) RunAgent(_ context.Context, _, input, _ string, _ domain.AgentEventStream) (string, error) {
	r.capturedInputs = append(r.capturedInputs, input)
	return "ok", nil
}

// mockUUIDResolver maps agent names to fixed UUIDs.
type mockUUIDResolver struct {
	mapping map[string]string
}

func (r *mockUUIDResolver) ResolveAgentUUID(_ context.Context, name string) string {
	return r.mapping[name]
}

// TestManager_UUIDStability_ContextSurvivesRename verifies that renaming a persistent
// agent (same UUID, new name) does not reset the accumulated context.
func TestManager_UUIDStability_ContextSurvivesRename(t *testing.T) {
	runner := &inputCapturingRunner{}
	mgr := NewManager(runner)

	// "agent-v1" and "agent-v2" share the same UUID (simulating a rename).
	mgr.SetUUIDResolver(&mockUUIDResolver{mapping: map[string]string{
		"agent-v1": "uuid-stable-abc",
		"agent-v2": "uuid-stable-abc",
	}})

	ctx := context.Background()
	const sess = "sess-rename"

	// Turn 1: call under original name → context stored under uuid-stable-abc:sess-rename.
	_, err := mgr.ExecuteTask(ctx, "agent-v1", sess, "hello from turn 1", domain.LifecycleModePersistent, 16000, nil)
	if err != nil {
		t.Fatalf("turn 1: %v", err)
	}

	// Turn 2: call under new name (post-rename) with same session.
	_, err = mgr.ExecuteTask(ctx, "agent-v2", sess, "hello from turn 2", domain.LifecycleModePersistent, 16000, nil)
	if err != nil {
		t.Fatalf("turn 2: %v", err)
	}

	// Turn 2 input must contain the previous context (not bare).
	turn2Input := runner.capturedInputs[1]
	if turn2Input == "hello from turn 2" {
		t.Fatal("context was reset after rename: expected 'Previous context:' prefix, got bare input")
	}

	const prefix = "Previous context:"
	if len(turn2Input) < len(prefix) || turn2Input[:len(prefix)] != prefix {
		t.Fatalf("expected input to start with %q after rename, got: %q", prefix, turn2Input)
	}
}

// TestManager_SessionIsolation verifies two sessions of the same agent never share context.
func TestManager_SessionIsolation(t *testing.T) {
	runner := &inputCapturingRunner{}
	mgr := NewManager(runner)

	ctx := context.Background()

	// Session A: build context.
	_, err := mgr.ExecuteTask(ctx, "agent-x", "sess-A", "msg from A", domain.LifecycleModePersistent, 16000, nil)
	if err != nil {
		t.Fatalf("sess-A: %v", err)
	}

	// Session B: first message must arrive bare (no sess-A context).
	_, err = mgr.ExecuteTask(ctx, "agent-x", "sess-B", "msg from B", domain.LifecycleModePersistent, 16000, nil)
	if err != nil {
		t.Fatalf("sess-B: %v", err)
	}

	sessBInput := runner.capturedInputs[1]
	if sessBInput != "msg from B" {
		t.Fatalf("session isolation broken: sess-B received context from sess-A, input=%q", sessBInput)
	}
}

// TestManager_UUIDFallbackToName verifies that when the resolver returns an empty string,
// the key falls back to the agent name so behaviour is unchanged.
func TestManager_UUIDFallbackToName(t *testing.T) {
	runner := &mockRunner{outputs: map[string]string{"agent-a": "ok"}}
	mgr := NewManager(runner)

	// Resolver that always returns empty → fallback path.
	mgr.SetUUIDResolver(&mockUUIDResolver{mapping: map[string]string{}})

	ctx := context.Background()

	_, err := mgr.ExecuteTask(ctx, "agent-a", "sess", "task", domain.LifecycleModePersistent, 16000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mgr.ContextSize(context.Background(), "agent-a", "sess") != 2 {
		t.Errorf("expected 2 context entries under name-based fallback key, got %d", mgr.ContextSize(context.Background(), "agent-a", "sess"))
	}
}
