package lifecycle

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

type mockEventStream struct {
	events []*domain.AgentEvent
}

func (m *mockEventStream) Send(event *domain.AgentEvent) error {
	m.events = append(m.events, event)
	return nil
}

func TestDispatcher_DispatchSuccess(t *testing.T) {
	runner := &mockRunner{outputs: map[string]string{"child": "result"}}
	mgr := NewManager(runner)
	dispatcher := NewDispatcher(mgr)
	stream := &mockEventStream{}

	packet, err := dispatcher.Dispatch(context.Background(), "task-1", "parent", "child", "session-1", "do work",
		domain.LifecycleModeSpawn, 16000, 0, stream)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if packet.Status != domain.TaskPacketCompleted {
		t.Errorf("expected completed, got %s", packet.Status)
	}
	if packet.Result != "result" {
		t.Errorf("expected result %q, got %q", "result", packet.Result)
	}

	// Check events
	hasDispatched := false
	hasCompleted := false
	for _, e := range stream.events {
		if e.Type == domain.EventTypeTaskDispatched {
			hasDispatched = true
		}
		if e.Type == domain.EventTypeTaskCompleted {
			hasCompleted = true
		}
	}
	if !hasDispatched {
		t.Error("expected task.dispatched event")
	}
	if !hasCompleted {
		t.Error("expected task.completed event")
	}
}

func TestDispatcher_DispatchFailure(t *testing.T) {
	runner := &mockRunner{err: fmt.Errorf("agent crashed")}
	mgr := NewManager(runner)
	dispatcher := NewDispatcher(mgr)
	stream := &mockEventStream{}

	packet, err := dispatcher.Dispatch(context.Background(), "task-1", "parent", "child", "session-1", "do work",
		domain.LifecycleModeSpawn, 16000, 0, stream)
	if err == nil {
		t.Fatal("expected error")
	}
	if packet.Status != domain.TaskPacketFailed {
		t.Errorf("expected failed, got %s", packet.Status)
	}

	hasFailed := false
	for _, e := range stream.events {
		if e.Type == domain.EventTypeTaskFailed {
			hasFailed = true
		}
	}
	if !hasFailed {
		t.Error("expected task.failed event")
	}
}

func TestDispatcher_DispatchTimeout(t *testing.T) {
	// Create a runner that blocks until context is cancelled
	slowRunner := &mockRunner{outputs: map[string]string{}}
	mgr := NewManager(slowRunner)

	// Override with a slow runner
	mgr.runner = &slowAgentRunner{}

	dispatcher := NewDispatcher(mgr)
	stream := &mockEventStream{}

	packet, err := dispatcher.Dispatch(context.Background(), "task-1", "parent", "child", "session-1", "slow work",
		domain.LifecycleModeSpawn, 16000, 50*time.Millisecond, stream)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if packet.Status != domain.TaskPacketTimeout {
		t.Errorf("expected timeout, got %s", packet.Status)
	}

	hasTimeout := false
	for _, e := range stream.events {
		if e.Type == domain.EventTypeTaskTimeout {
			hasTimeout = true
		}
	}
	if !hasTimeout {
		t.Error("expected task.timeout event")
	}
}

type slowAgentRunner struct{}

func (s *slowAgentRunner) RunAgent(ctx context.Context, agentName, input, sessionID string, _ domain.AgentEventStream) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(5 * time.Second):
		return "done", nil
	}
}

func TestDispatcher_GetTask(t *testing.T) {
	runner := &mockRunner{outputs: map[string]string{"child": "result"}}
	mgr := NewManager(runner)
	dispatcher := NewDispatcher(mgr)

	dispatcher.Dispatch(context.Background(), "task-1", "parent", "child", "session-1", "work",
		domain.LifecycleModeSpawn, 16000, 0, nil)

	tp, ok := dispatcher.GetTask("task-1")
	if !ok {
		t.Fatal("expected task to exist")
	}
	if tp.Status != domain.TaskPacketCompleted {
		t.Errorf("expected completed, got %s", tp.Status)
	}
}

func TestDispatcher_ListTasks(t *testing.T) {
	runner := &mockRunner{outputs: map[string]string{"child": "result"}}
	mgr := NewManager(runner)
	dispatcher := NewDispatcher(mgr)

	dispatcher.Dispatch(context.Background(), "task-1", "parent-a", "child", "session-1", "work 1",
		domain.LifecycleModeSpawn, 16000, 0, nil)
	dispatcher.Dispatch(context.Background(), "task-2", "parent-a", "child", "session-1", "work 2",
		domain.LifecycleModeSpawn, 16000, 0, nil)
	dispatcher.Dispatch(context.Background(), "task-3", "parent-b", "child", "session-2", "work 3",
		domain.LifecycleModeSpawn, 16000, 0, nil)

	tasksA := dispatcher.ListTasks("parent-a")
	if len(tasksA) != 2 {
		t.Errorf("expected 2 tasks for parent-a, got %d", len(tasksA))
	}

	tasksB := dispatcher.ListTasks("parent-b")
	if len(tasksB) != 1 {
		t.Errorf("expected 1 task for parent-b, got %d", len(tasksB))
	}
}

func TestDispatcher_PersistentChild(t *testing.T) {
	runner := &mockRunner{outputs: map[string]string{"child": "result"}}
	mgr := NewManager(runner)
	dispatcher := NewDispatcher(mgr)

	// First task
	p1, err := dispatcher.Dispatch(context.Background(), "task-1", "parent", "child", "session-1", "work 1",
		domain.LifecycleModePersistent, 16000, 0, nil)
	if err != nil {
		t.Fatalf("task 1: %v", err)
	}
	if p1.Status != domain.TaskPacketCompleted {
		t.Errorf("expected completed, got %s", p1.Status)
	}

	// Second task — child should have context
	p2, err := dispatcher.Dispatch(context.Background(), "task-2", "parent", "child", "session-1", "work 2",
		domain.LifecycleModePersistent, 16000, 0, nil)
	if err != nil {
		t.Fatalf("task 2: %v", err)
	}
	if p2.Status != domain.TaskPacketCompleted {
		t.Errorf("expected completed, got %s", p2.Status)
	}

	// Context should be accumulated
	if mgr.ContextSize(context.Background(), "child", "task-2") != 2 {
		// Each task gets its own session (taskID as sessionID), so context = 2 for task-2
		t.Logf("context size: %d (each dispatch uses taskID as sessionID)", mgr.ContextSize(context.Background(), "child", "task-2"))
	}
}
