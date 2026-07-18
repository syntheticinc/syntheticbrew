package agentrelationcreate

import (
	"context"
	"errors"
	"testing"

	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// fakeRepo is an in-memory RelationRepository for the create/cycle scenarios.
type fakeRepo struct {
	byID   map[string]Relation
	nextID int
}

func newFakeRepo() *fakeRepo { return &fakeRepo{byID: map[string]Relation{}} }

func (f *fakeRepo) List(_ context.Context, schemaID string) ([]Relation, error) {
	out := make([]Relation, 0, len(f.byID))
	for _, r := range f.byID {
		if r.SchemaID == schemaID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeRepo) Create(_ context.Context, r *Relation) error {
	f.nextID++
	r.ID = string(rune('0'+f.nextID)) + "-rel"
	f.byID[r.ID] = *r
	return nil
}

const (
	schemaID = "11111111-1111-1111-1111-111111111111"
	agentA   = "00000000-0000-0000-0000-00000000000a"
	agentB   = "00000000-0000-0000-0000-00000000000b"
	agentC   = "00000000-0000-0000-0000-00000000000c"
	agentD   = "00000000-0000-0000-0000-00000000000d"
)

func in(src, tgt string) Input {
	return Input{SchemaID: schemaID, SourceAgentID: src, TargetAgentID: tgt}
}

func requireInvalidInput(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error, got nil")
	}
	var de *pkgerrors.DomainError
	if !errors.As(err, &de) || de.Code != pkgerrors.CodeInvalidInput {
		t.Fatalf("expected InvalidInput DomainError, got %T: %v", err, err)
	}
}

func TestExecute_SelfLoopRejected(t *testing.T) {
	uc := New(newFakeRepo())
	_, err := uc.Execute(context.Background(), in(agentA, agentA))
	requireInvalidInput(t, err)
}

func TestExecute_TwoCycleRejected(t *testing.T) {
	uc := New(newFakeRepo())
	if _, err := uc.Execute(context.Background(), in(agentA, agentB)); err != nil {
		t.Fatalf("A→B should be accepted: %v", err)
	}
	_, err := uc.Execute(context.Background(), in(agentB, agentA))
	requireInvalidInput(t, err)
}

func TestExecute_ThreeCycleRejected(t *testing.T) {
	uc := New(newFakeRepo())
	for _, e := range [][2]string{{agentA, agentB}, {agentB, agentC}} {
		if _, err := uc.Execute(context.Background(), in(e[0], e[1])); err != nil {
			t.Fatalf("%s→%s should be accepted: %v", e[0], e[1], err)
		}
	}
	_, err := uc.Execute(context.Background(), in(agentC, agentA))
	requireInvalidInput(t, err)
}

func TestExecute_TreeNoFalsePositive(t *testing.T) {
	uc := New(newFakeRepo())
	// A→B, A→C, A→D branch out; then B→C, B→D deepen. No cycle anywhere.
	for _, e := range [][2]string{{agentA, agentB}, {agentA, agentC}, {agentA, agentD}, {agentB, agentC}, {agentB, agentD}} {
		if _, err := uc.Execute(context.Background(), in(e[0], e[1])); err != nil {
			t.Fatalf("%s→%s should be accepted (no cycle): %v", e[0], e[1], err)
		}
	}
}

func TestExecute_LongerChainShortcutAllowed(t *testing.T) {
	uc := New(newFakeRepo())
	for _, e := range [][2]string{{agentA, agentB}, {agentB, agentC}, {agentC, agentD}} {
		if _, err := uc.Execute(context.Background(), in(e[0], e[1])); err != nil {
			t.Fatalf("chain edge %s→%s should be accepted: %v", e[0], e[1], err)
		}
	}
	// A→D shortcut closes no cycle (no path D→A).
	if _, err := uc.Execute(context.Background(), in(agentA, agentD)); err != nil {
		t.Fatalf("A→D shortcut should be accepted: %v", err)
	}
}

func TestExecute_PersistsAndReturnsID(t *testing.T) {
	repo := newFakeRepo()
	uc := New(repo)
	rel, err := uc.Execute(context.Background(), in(agentA, agentB))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if rel.ID == "" {
		t.Fatalf("expected a persisted ID")
	}
	if _, ok := repo.byID[rel.ID]; !ok {
		t.Fatalf("relation not persisted")
	}
}

func TestCheckNoCycle_ExcludesSelfOnUpdate(t *testing.T) {
	repo := newFakeRepo()
	uc := New(repo)
	rel, err := uc.Execute(context.Background(), in(agentA, agentB))
	if err != nil {
		t.Fatalf("seed A→B: %v", err)
	}
	// Re-checking the same edge while excluding its own ID must NOT self-report.
	if err := uc.CheckNoCycle(context.Background(), schemaID, agentA, agentB, rel.ID); err != nil {
		t.Fatalf("excluding self must not report a cycle: %v", err)
	}
	// Without excluding it, B→A would still close a 2-cycle against A→B.
	if err := uc.CheckNoCycle(context.Background(), schemaID, agentB, agentA, ""); err == nil {
		t.Fatalf("B→A must be reported as a cycle")
	}
}

func TestReachable(t *testing.T) {
	tests := []struct {
		name string
		adj  map[string][]string
		src  string
		dst  string
		want bool
	}{
		{"src==dst", map[string][]string{}, "A", "A", true},
		{"direct edge", map[string][]string{"A": {"B"}}, "A", "B", true},
		{"no back edge", map[string][]string{"A": {"B"}}, "B", "A", false},
		{"transitive", map[string][]string{"A": {"B"}, "B": {"C"}}, "A", "C", true},
		{"reverse absent", map[string][]string{"A": {"B"}, "B": {"C"}}, "C", "A", false},
		{"disconnected", map[string][]string{"A": {"B"}, "X": {"Y"}}, "A", "Y", false},
		{"branching", map[string][]string{"A": {"B", "C"}, "B": {"D"}}, "A", "D", true},
		{"existing cycle no infinite loop", map[string][]string{"A": {"B"}, "B": {"A"}}, "A", "C", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := reachable(tt.adj, tt.src, tt.dst); got != tt.want {
				t.Fatalf("reachable(%v,%s,%s)=%v want %v", tt.adj, tt.src, tt.dst, got, tt.want)
			}
		})
	}
}
