package capabilities_test

import (
	"context"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain/capabilities"
)

func TestMemoryCapability_Type(t *testing.T) {
	t.Parallel()

	if got := (capabilities.MemoryCapability{}).Type(); got != "memory" {
		t.Errorf("Type(): got %q, want \"memory\"", got)
	}
}

func TestMemoryCapability_Validate_AcceptsAnyConfig(t *testing.T) {
	t.Parallel()

	cap := capabilities.MemoryCapability{}

	for _, cfg := range []map[string]any{
		nil,
		{},
		{"unknown_key": "value"},
		{"retention_days": 30, "scope": "session"},
	} {
		if err := cap.Validate(cfg); err != nil {
			t.Errorf("Validate(%v) returned unexpected error: %v", cfg, err)
		}
	}
}

func TestMemoryCapability_Tools_ReturnsRecallAndStore(t *testing.T) {
	t.Parallel()

	tools, err := (capabilities.MemoryCapability{}).Tools(context.Background(), "agent-x", nil)
	if err != nil {
		t.Fatalf("Tools error: %v", err)
	}

	want := []string{"memory_recall", "memory_store"}
	if len(tools) != len(want) {
		t.Fatalf("Tools length: got %d, want %d (%v vs %v)", len(tools), len(want), tools, want)
	}
	for i, w := range want {
		if tools[i] != w {
			t.Errorf("Tools[%d]: got %q, want %q", i, tools[i], w)
		}
	}
}
