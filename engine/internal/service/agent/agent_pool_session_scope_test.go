package agent

import (
	"context"
	"testing"
)

// putRunningAgent registers a minimal running agent directly in the pool for
// session-isolation tests, bypassing the full spawn path.
func putRunningAgent(p *AgentPool, id, sessionID string) {
	_, cancel := context.WithCancel(context.Background())
	p.mu.Lock()
	p.agents[id] = &RunningAgent{
		ID:           id,
		SessionID:    sessionID,
		Status:       "running",
		Cancel:       cancel,
		completionCh: make(chan struct{}),
	}
	p.mu.Unlock()
}

// TestAgentPool_SessionScope_Isolation pins the cloud-first fix: the pool is
// process-global across tenants, so list/status/stop must never reach an agent
// from another session (and thus, in Cloud, another tenant).
func TestAgentPool_SessionScope_Isolation(t *testing.T) {
	p := NewAgentPool(AgentPoolConfig{})
	putRunningAgent(p, "agent-A", "session-A")
	putRunningAgent(p, "agent-B", "session-B")

	// list is session-scoped
	aList := p.GetSessionAgents("session-A")
	if len(aList) != 1 || aList[0].ID != "agent-A" {
		t.Fatalf("session-A must see only agent-A, got %+v", aList)
	}

	// status cannot cross sessions
	if _, ok := p.GetStatus("session-A", "agent-B"); ok {
		t.Fatal("session-A must NOT be able to inspect agent-B (cross-tenant leak)")
	}
	if _, ok := p.GetStatus("session-A", "agent-A"); !ok {
		t.Fatal("session-A must be able to inspect its own agent-A")
	}

	// stop cannot cross sessions
	if err := p.StopAgent("session-A", "agent-B"); err == nil {
		t.Fatal("session-A must NOT be able to stop agent-B (cross-tenant leak)")
	}
	// agent-B must remain untouched (still running)
	if snap, ok := p.GetStatus("session-B", "agent-B"); !ok || snap.Status != "running" {
		t.Fatalf("agent-B must remain running after a cross-session stop attempt, got ok=%v status=%q", ok, snap.Status)
	}

	// stop within the same session works
	if err := p.StopAgent("session-A", "agent-A"); err != nil {
		t.Fatalf("session-A must be able to stop its own agent-A: %v", err)
	}
}
