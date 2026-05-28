package capability

import (
	"context"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain/capabilities"
)

// testRegistry returns the production capability registry used in tests:
// memory + knowledge. New capabilities added in code automatically appear here
// because the registry is constructed via the production constructor.
func testRegistry() *capabilities.Registry {
	return capabilities.NewRegistry(
		capabilities.MemoryCapability{},
		capabilities.KnowledgeCapability{},
	)
}

type mockCapReader struct {
	caps map[string][]CapabilityRecord
	err  error
}

func (m *mockCapReader) ListEnabledByAgent(_ context.Context, agentName string) ([]CapabilityRecord, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.caps[agentName], nil
}

func TestInjectedTools_Memory(t *testing.T) {
	reader := &mockCapReader{
		caps: map[string][]CapabilityRecord{
			"agent-a": {
				{ID: "1", AgentName: "agent-a", Type: "memory", Enabled: true},
			},
		},
	}
	inj := NewInjector(reader, testRegistry())

	tools, err := inj.InjectedTools(context.Background(), "agent-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d: %v", len(tools), tools)
	}
	if tools[0] != "memory_recall" || tools[1] != "memory_store" {
		t.Errorf("expected [memory_recall, memory_store], got %v", tools)
	}
}

func TestInjectedTools_Multiple(t *testing.T) {
	reader := &mockCapReader{
		caps: map[string][]CapabilityRecord{
			"agent-a": {
				{ID: "1", AgentName: "agent-a", Type: "memory", Enabled: true},
				{ID: "2", AgentName: "agent-a", Type: "knowledge", Enabled: true},
			},
		},
	}
	inj := NewInjector(reader, testRegistry())

	tools, err := inj.InjectedTools(context.Background(), "agent-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// memory_recall, memory_store, knowledge_search = 3
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d: %v", len(tools), tools)
	}
}

func TestInjectedTools_NoDuplicates(t *testing.T) {
	reader := &mockCapReader{
		caps: map[string][]CapabilityRecord{
			"agent-a": {
				{ID: "1", AgentName: "agent-a", Type: "memory", Enabled: true},
				{ID: "2", AgentName: "agent-a", Type: "memory", Enabled: true}, // duplicate type
			},
		},
	}
	inj := NewInjector(reader, testRegistry())

	tools, err := inj.InjectedTools(context.Background(), "agent-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools (no duplicates), got %d: %v", len(tools), tools)
	}
}

func TestInjectedTools_NoCapabilities(t *testing.T) {
	reader := &mockCapReader{caps: map[string][]CapabilityRecord{}}
	inj := NewInjector(reader, testRegistry())

	tools, err := inj.InjectedTools(context.Background(), "agent-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("expected 0 tools, got %d: %v", len(tools), tools)
	}
}

func TestInjectedTools_UnknownCapabilityNoTools(t *testing.T) {
	reader := &mockCapReader{
		caps: map[string][]CapabilityRecord{
			"agent-a": {
				{ID: "1", AgentName: "agent-a", Type: "unknown_cap", Enabled: true},
			},
		},
	}
	inj := NewInjector(reader, testRegistry())

	tools, err := inj.InjectedTools(context.Background(), "agent-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("expected 0 tools for unknown capability type, got %d: %v", len(tools), tools)
	}
}
