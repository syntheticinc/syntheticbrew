package schemacreate

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

type mockRepo struct {
	schemas map[string]*SchemaRecord
	nextID  int
	err     error
}

func newMockRepo() *mockRepo {
	return &mockRepo{schemas: make(map[string]*SchemaRecord), nextID: 1}
}

func (m *mockRepo) Create(_ context.Context, record *SchemaRecord) error {
	if m.err != nil {
		return m.err
	}
	if _, exists := m.schemas[record.Name]; exists {
		// Mirrors the app-layer adapter contract: duplicates arrive as a
		// typed DomainError, not a raw driver string.
		return pkgerrors.AlreadyExists(fmt.Sprintf("schema with name %q already exists", record.Name))
	}
	record.ID = fmt.Sprintf("id-%d", m.nextID)
	m.nextID++
	m.schemas[record.Name] = record
	return nil
}

// mockGuard records the admission call and returns a configured error.
type mockGuard struct {
	err      error
	tenantID string
	n        int
	calls    int
}

func (g *mockGuard) OnSchemaCreate(_ context.Context, tenantID string, n int) error {
	g.calls++
	g.tenantID = tenantID
	g.n = n
	return g.err
}

func TestExecute_Success(t *testing.T) {
	repo := newMockRepo()
	guard := &mockGuard{}
	uc := New(repo, guard)

	out, err := uc.Execute(context.Background(), Input{Name: "test-schema", Description: "desc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ID == "" {
		t.Error("expected non-empty ID")
	}
	if out.Name != "test-schema" {
		t.Errorf("expected name %q, got %q", "test-schema", out.Name)
	}
	if guard.calls != 1 || guard.n != 1 {
		t.Errorf("expected guard called once with n=1, got calls=%d n=%d", guard.calls, guard.n)
	}
}

func TestExecute_GuardReceivesTenantFromContext(t *testing.T) {
	repo := newMockRepo()
	guard := &mockGuard{}
	uc := New(repo, guard)

	ctx := domain.WithTenantID(context.Background(), "tenant-abc")
	if _, err := uc.Execute(ctx, Input{Name: "scoped"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if guard.tenantID != "tenant-abc" {
		t.Errorf("expected guard to receive tenant from ctx, got %q", guard.tenantID)
	}
}

func TestExecute_QuotaExceeded_MapsToUsageLimited_NothingPersisted(t *testing.T) {
	repo := newMockRepo()
	guard := &mockGuard{err: fmt.Errorf("tenant over cap: %w", plugin.ErrSchemaQuotaExceeded)}
	uc := New(repo, guard)

	_, err := uc.Execute(context.Background(), Input{Name: "blocked"})
	if err == nil {
		t.Fatal("expected quota error")
	}
	var domainErr *pkgerrors.DomainError
	if !errors.As(err, &domainErr) || domainErr.Code != pkgerrors.CodeUsageLimited {
		t.Fatalf("expected CodeUsageLimited (402 at delivery), got %v", err)
	}
	if len(repo.schemas) != 0 {
		t.Error("guard rejection must abort before any row is written")
	}
}

func TestExecute_GuardNonQuotaError_Internal(t *testing.T) {
	repo := newMockRepo()
	guard := &mockGuard{err: errors.New("admission backend down")}
	uc := New(repo, guard)

	_, err := uc.Execute(context.Background(), Input{Name: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	var domainErr *pkgerrors.DomainError
	if !errors.As(err, &domainErr) || domainErr.Code != pkgerrors.CodeInternal {
		t.Fatalf("expected CodeInternal for non-quota guard error, got %v", err)
	}
	if len(repo.schemas) != 0 {
		t.Error("guard failure must abort creation")
	}
}

func TestExecute_EmptyName(t *testing.T) {
	guard := &mockGuard{}
	uc := New(newMockRepo(), guard)

	_, err := uc.Execute(context.Background(), Input{Name: ""})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
	if guard.calls != 0 {
		t.Error("invalid input must be rejected before the quota seam")
	}
}

func TestExecute_DuplicateName_PassesThroughAlreadyExists(t *testing.T) {
	uc := New(newMockRepo(), &mockGuard{})

	if _, err := uc.Execute(context.Background(), Input{Name: "dup"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err := uc.Execute(context.Background(), Input{Name: "dup"})
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
	var domainErr *pkgerrors.DomainError
	if !errors.As(err, &domainErr) || domainErr.Code != pkgerrors.CodeAlreadyExists {
		t.Fatalf("expected CodeAlreadyExists to pass through untouched, got %v", err)
	}
}

func TestExecute_RepoError(t *testing.T) {
	repo := newMockRepo()
	repo.err = fmt.Errorf("db failure")
	uc := New(repo, &mockGuard{})

	_, err := uc.Execute(context.Background(), Input{Name: "test"})
	if err == nil {
		t.Fatal("expected error")
	}
}
