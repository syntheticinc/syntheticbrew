package schematemplate

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

type stubTemplateReader struct {
	tmpl *domain.SchemaTemplate
	err  error
}

func (s *stubTemplateReader) GetByName(context.Context, string) (*domain.SchemaTemplate, error) {
	return s.tmpl, s.err
}

type stubGuard struct {
	err      error
	tenantID string
	n        int
	calls    int
}

func (g *stubGuard) OnSchemaCreate(_ context.Context, tenantID string, n int) error {
	g.calls++
	g.tenantID = tenantID
	g.n = n
	return g.err
}

func validTemplate() *domain.SchemaTemplate {
	return &domain.SchemaTemplate{
		Name: "starter",
		Definition: domain.SchemaTemplateDefinition{
			EntryAgentName: "helper",
			Agents:         []domain.SchemaTemplateAgent{{Name: "helper"}},
		},
	}
}

// TestFork_QuotaRejection_BeforeAnyWrite pins the fork half of the quota fix:
// a template fork creates a schema, so it must pass the same plugin quota
// seam as every other creation path — and a rejection must land before the
// transaction. The service is constructed with a nil DB handle: if the guard
// rejection did not short-circuit, the test would panic on DB access.
func TestFork_QuotaRejection_BeforeAnyWrite(t *testing.T) {
	guard := &stubGuard{err: fmt.Errorf("over cap: %w", plugin.ErrSchemaQuotaExceeded)}
	svc := NewForkService(nil, &stubTemplateReader{tmpl: validTemplate()}, guard)

	ctx := domain.WithTenantID(context.Background(), "tenant-x")
	_, err := svc.Fork(ctx, "starter", "my-fork")
	if err == nil {
		t.Fatal("expected quota rejection")
	}
	var domainErr *pkgerrors.DomainError
	if !errors.As(err, &domainErr) || domainErr.Code != pkgerrors.CodeUsageLimited {
		t.Fatalf("expected CodeUsageLimited (402 at delivery), got %v", err)
	}
	if guard.calls != 1 || guard.n != 1 || guard.tenantID != "tenant-x" {
		t.Fatalf("guard must be consulted once with n=1 and the ctx tenant; got calls=%d n=%d tenant=%q",
			guard.calls, guard.n, guard.tenantID)
	}
}

// TestFork_GuardNonQuotaError_Propagates pins that a non-quota admission
// failure aborts the fork without being mislabelled as a quota rejection.
func TestFork_GuardNonQuotaError_Propagates(t *testing.T) {
	guard := &stubGuard{err: errors.New("admission backend down")}
	svc := NewForkService(nil, &stubTemplateReader{tmpl: validTemplate()}, guard)

	_, err := svc.Fork(context.Background(), "starter", "my-fork")
	if err == nil {
		t.Fatal("expected error")
	}
	var domainErr *pkgerrors.DomainError
	if errors.As(err, &domainErr) && domainErr.Code == pkgerrors.CodeUsageLimited {
		t.Fatalf("non-quota guard error must not surface as usage-limited: %v", err)
	}
}

// TestFork_GuardRunsAfterValidation pins the ordering: invalid input and
// missing templates fail before the quota seam is consulted, so a rejected
// tenant still gets accurate 404/validation errors.
func TestFork_GuardRunsAfterValidation(t *testing.T) {
	guard := &stubGuard{err: fmt.Errorf("over cap: %w", plugin.ErrSchemaQuotaExceeded)}

	svc := NewForkService(nil, &stubTemplateReader{tmpl: nil}, guard)
	if _, err := svc.Fork(context.Background(), "missing", "x"); !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("expected ErrTemplateNotFound, got %v", err)
	}
	if guard.calls != 0 {
		t.Fatal("guard must not run for a missing template")
	}

	if _, err := svc.Fork(context.Background(), "starter", ""); err == nil {
		t.Fatal("expected name validation error")
	}
	if guard.calls != 0 {
		t.Fatal("guard must not run for invalid input")
	}
}
