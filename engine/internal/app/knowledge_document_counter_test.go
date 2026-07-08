package app

import (
	"context"
	"errors"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// fakeKnowledgeDocumentCounterRepo records the tenant stamped into the
// context so the counter's scoping can be asserted without a database.
type fakeKnowledgeDocumentCounterRepo struct {
	count int64
	err   error

	called       bool
	tenantOnCall string
}

func (f *fakeKnowledgeDocumentCounterRepo) CountDocuments(ctx context.Context) (int64, error) {
	f.called = true
	f.tenantOnCall = domain.TenantIDFromContext(ctx)
	return f.count, f.err
}

func TestKnowledgeDocumentCounter_EmptyTenantShortCircuits(t *testing.T) {
	repo := &fakeKnowledgeDocumentCounterRepo{count: 7}
	counter := NewKnowledgeDocumentCounter(repo)

	got, err := counter.CountKnowledgeDocuments(context.Background(), "")
	if err != nil {
		t.Fatalf("CountKnowledgeDocuments: unexpected error: %v", err)
	}
	if got != 0 {
		t.Fatalf("expected 0 for empty tenant, got %d", got)
	}
	if repo.called {
		t.Fatal("no repository access expected without a tenant")
	}
}

func TestKnowledgeDocumentCounter_ScopesTenantAndConverts(t *testing.T) {
	repo := &fakeKnowledgeDocumentCounterRepo{count: 42}
	counter := NewKnowledgeDocumentCounter(repo)

	got, err := counter.CountKnowledgeDocuments(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("CountKnowledgeDocuments: unexpected error: %v", err)
	}
	if got != 42 {
		t.Fatalf("expected int64 42 converted to int, got %d", got)
	}
	if repo.tenantOnCall != "tenant-a" {
		t.Fatalf("expected tenant scoping on CountDocuments, got %q", repo.tenantOnCall)
	}
}

func TestKnowledgeDocumentCounter_PropagatesRepoError(t *testing.T) {
	repo := &fakeKnowledgeDocumentCounterRepo{err: errors.New("db down")}
	counter := NewKnowledgeDocumentCounter(repo)

	if _, err := counter.CountKnowledgeDocuments(context.Background(), "tenant-a"); err == nil {
		t.Fatal("expected the repository error to propagate")
	}
}
