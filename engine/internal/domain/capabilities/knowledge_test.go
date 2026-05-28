package capabilities_test

import (
	"context"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain/capabilities"
)

func TestKnowledgeCapability_Type(t *testing.T) {
	t.Parallel()

	if got := (capabilities.KnowledgeCapability{}).Type(); got != "knowledge" {
		t.Errorf("Type(): got %q, want \"knowledge\"", got)
	}
}

func TestKnowledgeCapability_Validate_AcceptsAnyConfig(t *testing.T) {
	t.Parallel()

	cap := capabilities.KnowledgeCapability{}

	for _, cfg := range []map[string]any{
		nil,
		{},
		{"unknown_key": "value"},
	} {
		if err := cap.Validate(cfg); err != nil {
			t.Errorf("Validate(%v) returned unexpected error: %v", cfg, err)
		}
	}
}

func TestKnowledgeCapability_Tools_ReturnsKnowledgeSearch(t *testing.T) {
	t.Parallel()

	tools, err := (capabilities.KnowledgeCapability{}).Tools(context.Background(), "agent-x", nil)
	if err != nil {
		t.Fatalf("Tools error: %v", err)
	}

	want := []string{"knowledge_search"}
	if len(tools) != len(want) {
		t.Fatalf("Tools length: got %d, want %d (%v vs %v)", len(tools), len(want), tools, want)
	}
	if tools[0] != want[0] {
		t.Errorf("Tools[0]: got %q, want %q", tools[0], want[0])
	}
}
