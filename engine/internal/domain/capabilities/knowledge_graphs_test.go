package capabilities_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain/capabilities"
)

// mockResolver records what the capability passed in and returns canned data.
type mockResolver struct {
	gotAgentID string
	gotBundles []string
	calls      int
	out        []string
	err        error
}

func (m *mockResolver) ResolveToolsForBundles(_ context.Context, agentID string, bundles []string) ([]string, error) {
	m.calls++
	m.gotAgentID = agentID
	m.gotBundles = bundles
	if m.err != nil {
		return nil, m.err
	}
	return m.out, nil
}

func TestKnowledgeGraphsCapability_Type(t *testing.T) {
	t.Parallel()

	if got := capabilities.NewKnowledgeGraphsCapability(nil).Type(); got != "knowledge_graphs" {
		t.Errorf("Type(): got %q, want %q", got, "knowledge_graphs")
	}
}

func TestKnowledgeGraphsCapability_Validate(t *testing.T) {
	t.Parallel()

	cap := capabilities.KnowledgeGraphsCapability{}

	cases := []struct {
		name    string
		cfg     map[string]any
		wantErr bool
	}{
		{"nil config", nil, true},
		{"empty config", map[string]any{}, true},
		{"bundles missing", map[string]any{"other": "x"}, true},
		{"bundles is string", map[string]any{"bundles": "not-array"}, true},
		{"bundles is map", map[string]any{"bundles": map[string]any{"a": 1}}, true},
		{"bundles is number", map[string]any{"bundles": 42}, true},
		{"bundles empty array", map[string]any{"bundles": []any{}}, true},
		{"bundles has number entry", map[string]any{"bundles": []any{"valid", 7}}, true},
		{"bundles has invalid name (uppercase)", map[string]any{"bundles": []any{"NotValid"}}, true},
		{"bundles has invalid name (leading hyphen)", map[string]any{"bundles": []any{"-leading"}}, true},
		{"bundles has invalid name (single char)", map[string]any{"bundles": []any{"a"}}, true},
		{"single valid bundle", map[string]any{"bundles": []any{"my-bundle"}}, false},
		{"multiple valid bundles", map[string]any{"bundles": []any{"alpha", "beta-gamma", "x1"}}, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := cap.Validate(tc.cfg)
			if tc.wantErr && err == nil {
				t.Fatalf("Validate(%v): expected error, got nil", tc.cfg)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Validate(%v): unexpected error: %v", tc.cfg, err)
			}
		})
	}
}

func TestKnowledgeGraphsCapability_Tools_NilResolver(t *testing.T) {
	t.Parallel()

	cap := capabilities.NewKnowledgeGraphsCapability(nil)
	_, err := cap.Tools(context.Background(), "agent-x", map[string]any{
		"bundles": []any{"valid"},
	})
	if err == nil {
		t.Fatal("expected error when resolver is nil")
	}
}

func TestKnowledgeGraphsCapability_Tools_InvalidConfig(t *testing.T) {
	t.Parallel()

	mock := &mockResolver{}
	cap := capabilities.NewKnowledgeGraphsCapability(mock)

	_, err := cap.Tools(context.Background(), "agent-x", map[string]any{
		"bundles": []any{}, // empty -> Validate fails
	})
	if err == nil {
		t.Fatal("expected validation error for empty bundles")
	}
	if mock.calls != 0 {
		t.Errorf("resolver should not be called when validation fails, got %d calls", mock.calls)
	}
}

func TestKnowledgeGraphsCapability_Tools_HappyPath(t *testing.T) {
	t.Parallel()

	mock := &mockResolver{
		out: []string{"get_user", "list_user", "list_user_ids"},
	}
	cap := capabilities.NewKnowledgeGraphsCapability(mock)

	tools, err := cap.Tools(context.Background(), "agent-42", map[string]any{
		"bundles": []any{"crm", "support"},
	})
	if err != nil {
		t.Fatalf("Tools error: %v", err)
	}
	if mock.calls != 1 {
		t.Fatalf("expected 1 resolver call, got %d", mock.calls)
	}
	if mock.gotAgentID != "agent-42" {
		t.Errorf("agentID: got %q, want %q", mock.gotAgentID, "agent-42")
	}
	wantBundles := []string{"crm", "support"}
	if !reflect.DeepEqual(mock.gotBundles, wantBundles) {
		t.Errorf("bundles: got %v, want %v", mock.gotBundles, wantBundles)
	}
	want := []string{"get_user", "list_user", "list_user_ids"}
	if !reflect.DeepEqual(tools, want) {
		t.Errorf("tools: got %v, want %v", tools, want)
	}
}

func TestKnowledgeGraphsCapability_Tools_ResolverError(t *testing.T) {
	t.Parallel()

	resolverErr := errors.New("resolver boom")
	mock := &mockResolver{err: resolverErr}
	cap := capabilities.NewKnowledgeGraphsCapability(mock)

	_, err := cap.Tools(context.Background(), "agent-x", map[string]any{
		"bundles": []any{"valid"},
	})
	if !errors.Is(err, resolverErr) {
		t.Fatalf("expected wrapped resolver error, got %v", err)
	}
}

func TestKnowledgeGraphsCapability_RegisterInRegistry(t *testing.T) {
	t.Parallel()

	mock := &mockResolver{out: []string{}}
	reg := capabilities.NewRegistry(capabilities.NewKnowledgeGraphsCapability(mock))

	cap, ok := reg.Get("knowledge_graphs")
	if !ok {
		t.Fatal("expected capability to be registered")
	}
	if cap.Type() != "knowledge_graphs" {
		t.Errorf("Type via registry: got %q", cap.Type())
	}
}
