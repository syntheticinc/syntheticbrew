// Package schematemplate wires the "browse catalog + use template" usecase
// for the V2 schema template catalog (§2.2).
//
// Reads go directly through a Repository surface (List / ListByCategory /
// GetByName / Search) that the HTTP handler injects. The Fork operation is
// delegated to a Forker (implemented by service/schematemplate.ForkService)
// because transactional DB handling belongs in the service layer, not in
// the usecase.
package schematemplate

import (
	"context"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// Repository is the consumer-side interface for reading catalog rows.
// Implemented by configrepo.GORMSchemaTemplateRepository.
type Repository interface {
	List(ctx context.Context) ([]domain.SchemaTemplate, error)
	ListByCategory(ctx context.Context, category domain.SchemaTemplateCategory) ([]domain.SchemaTemplate, error)
	GetByName(ctx context.Context, name string) (*domain.SchemaTemplate, error)
	Search(ctx context.Context, query string) ([]domain.SchemaTemplate, error)
}

// ForkResult mirrors service/schematemplate.ForkedSchema at the usecase
// boundary — the usecase layer does not import the service package's
// concrete type so HTTP handlers + tests can use a small, stable shape.
type ForkResult struct {
	SchemaID   string
	SchemaName string
	AgentIDs   map[string]string
}

// Forker runs the transactional fork. Implemented by
// service/schematemplate.ForkService. The implementation reads tenant_id
// from ctx (`domain.TenantIDFromContext`) — callers don't pass it
// separately, matching the rest of the engine's tenant-aware services.
type Forker interface {
	Fork(ctx context.Context, templateName, newSchemaName string) (*ForkResult, error)
}

// Usecase bundles catalog reads + the Fork action so delivery handlers see
// a single dependency.
type Usecase struct {
	repo   Repository
	forker Forker
}

// New constructs a Usecase wired to the given repository and forker.
func New(repo Repository, forker Forker) *Usecase {
	return &Usecase{repo: repo, forker: forker}
}

// List returns every catalog template. If `category` is empty all
// templates are returned, otherwise filtered by category. If `query` is
// set (mutually exclusive with category — query wins, to match the
// MCP catalog `GET /api/v1/mcp/catalog?q=...` surface), the repo search
// is used instead.
func (u *Usecase) List(ctx context.Context, category, query string) ([]domain.SchemaTemplate, error) {
	if query != "" {
		return u.repo.Search(ctx, query)
	}
	if category != "" {
		c := domain.SchemaTemplateCategory(category)
		if !c.IsValid() {
			return nil, fmt.Errorf("unknown category %q", category)
		}
		return u.repo.ListByCategory(ctx, c)
	}
	return u.repo.List(ctx)
}

// GetByName returns the named template, or (nil, nil) when absent.
func (u *Usecase) GetByName(ctx context.Context, name string) (*domain.SchemaTemplate, error) {
	return u.repo.GetByName(ctx, name)
}

// ForkTemplate clones the named template into a new tenant-owned schema
// graph. Tenant scope is taken from ctx by the underlying Forker.
// Delegates to the injected Forker — see service/schematemplate.ForkService
// for the transactional implementation.
func (u *Usecase) ForkTemplate(ctx context.Context, templateName, newSchemaName string) (*ForkResult, error) {
	return u.forker.Fork(ctx, templateName, newSchemaName)
}
