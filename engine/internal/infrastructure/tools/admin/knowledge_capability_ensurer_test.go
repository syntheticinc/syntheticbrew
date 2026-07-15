package admin

import (
	"context"
	"errors"
	"testing"
)

type fakeCapRepo struct {
	list      []CapabilityRecord
	listErr   error
	created   []*CapabilityRecord
	updatedID string
	updated   *CapabilityRecord
	writeErr  error
}

func (f *fakeCapRepo) ListByAgent(_ context.Context, _ string) ([]CapabilityRecord, error) {
	return f.list, f.listErr
}

func (f *fakeCapRepo) Create(_ context.Context, r *CapabilityRecord) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.created = append(f.created, r)
	return nil
}

func (f *fakeCapRepo) Update(_ context.Context, id string, r *CapabilityRecord) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.updatedID, f.updated = id, r
	return nil
}

func (f *fakeCapRepo) Delete(_ context.Context, _ string) error { return nil }

func TestCapabilityEnsurer_CreatesWhenAbsent(t *testing.T) {
	repo := &fakeCapRepo{}
	reloaded := false
	e := NewCapabilityEnsurer(repo, func(context.Context) { reloaded = true })

	if err := e.EnsureKnowledgeEnabled(context.Background(), "support"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.created) != 1 {
		t.Fatalf("expected 1 create, got %d", len(repo.created))
	}
	if repo.created[0].Type != "knowledge" || !repo.created[0].Enabled || repo.created[0].AgentName != "support" {
		t.Fatalf("created wrong record: %+v", repo.created[0])
	}
	if !reloaded {
		t.Fatal("registry must reload after enabling the capability")
	}
}

func TestCapabilityEnsurer_IdempotentWhenAlreadyEnabled(t *testing.T) {
	repo := &fakeCapRepo{list: []CapabilityRecord{{ID: "c1", Type: "knowledge", Enabled: true}}}
	reloaded := false
	e := NewCapabilityEnsurer(repo, func(context.Context) { reloaded = true })

	if err := e.EnsureKnowledgeEnabled(context.Background(), "support"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.created) != 0 || repo.updated != nil {
		t.Fatalf("already-enabled capability must be a no-op, got create=%d update=%v", len(repo.created), repo.updated)
	}
	if reloaded {
		t.Fatal("no reload when nothing changed")
	}
}

func TestCapabilityEnsurer_EnablesWhenDisabled(t *testing.T) {
	repo := &fakeCapRepo{list: []CapabilityRecord{{ID: "c1", Type: "knowledge", Enabled: false}}}
	e := NewCapabilityEnsurer(repo, nil)

	if err := e.EnsureKnowledgeEnabled(context.Background(), "support"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.updatedID != "c1" || repo.updated == nil || !repo.updated.Enabled {
		t.Fatalf("disabled capability must be enabled via Update, got id=%q rec=%+v", repo.updatedID, repo.updated)
	}
	if len(repo.created) != 0 {
		t.Fatal("must not create a duplicate when one already exists")
	}
}

func TestCapabilityEnsurer_ListErrorPropagates(t *testing.T) {
	repo := &fakeCapRepo{listErr: errors.New("db down")}
	e := NewCapabilityEnsurer(repo, nil)
	if err := e.EnsureKnowledgeEnabled(context.Background(), "support"); err == nil {
		t.Fatal("expected the list error to propagate")
	}
}
