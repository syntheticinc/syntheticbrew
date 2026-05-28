package capabilities_test

import (
	"context"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain/capabilities"
)

func TestNewRegistry_EmptyConstructor(t *testing.T) {
	t.Parallel()

	r := capabilities.NewRegistry()
	if got := r.AllTypes(); len(got) != 0 {
		t.Fatalf("expected empty registry, got %v", got)
	}
}

func TestNewRegistry_WithCapabilities(t *testing.T) {
	t.Parallel()

	r := capabilities.NewRegistry(
		capabilities.MemoryCapability{},
		capabilities.KnowledgeCapability{},
	)

	got := r.AllTypes()
	want := []string{"knowledge", "memory"} // sorted alphabetically

	if len(got) != len(want) {
		t.Fatalf("AllTypes length: got %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("AllTypes[%d]: got %q, want %q", i, got[i], w)
		}
	}
}

func TestRegistry_GetExisting(t *testing.T) {
	t.Parallel()

	r := capabilities.NewRegistry(capabilities.MemoryCapability{})

	cap, ok := r.Get("memory")
	if !ok {
		t.Fatal("expected memory capability to be registered")
	}
	if cap.Type() != "memory" {
		t.Errorf("Type(): got %q, want \"memory\"", cap.Type())
	}
}

func TestRegistry_GetMissing(t *testing.T) {
	t.Parallel()

	r := capabilities.NewRegistry(capabilities.MemoryCapability{})

	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("expected missing capability lookup to return false")
	}
}

func TestRegistry_RegisterReplacesExisting(t *testing.T) {
	t.Parallel()

	r := capabilities.NewRegistry(capabilities.MemoryCapability{})

	// Replace memory with a no-op stub that returns no tools.
	r.Register(stubCapability{typ: "memory"})

	cap, _ := r.Get("memory")
	tools, err := cap.Tools(context.Background(), "agent-x", nil)
	if err != nil {
		t.Fatalf("Tools error: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("expected replacement capability to return no tools, got %v", tools)
	}
}

func TestRegistry_AllTypesDeterministicOrder(t *testing.T) {
	t.Parallel()

	r := capabilities.NewRegistry(
		stubCapability{typ: "zeta"},
		stubCapability{typ: "alpha"},
		stubCapability{typ: "mu"},
	)

	got := r.AllTypes()
	want := []string{"alpha", "mu", "zeta"}

	for i, w := range want {
		if got[i] != w {
			t.Errorf("AllTypes[%d]: got %q, want %q", i, got[i], w)
		}
	}
}

// stubCapability is a test-only capability that returns no tools.
type stubCapability struct {
	typ string
}

func (s stubCapability) Type() string                  { return s.typ }
func (stubCapability) Validate(_ map[string]any) error { return nil }
func (stubCapability) Tools(_ context.Context, _ string, _ map[string]any) ([]string, error) {
	return nil, nil
}
