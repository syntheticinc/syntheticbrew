// Package schemacreate is the single business-logic seam for creating
// user-facing schemas. Every facade — the REST handler, the admin/provision
// tools, any future entry point — routes through Execute so the creation
// invariants (validation, the plugin quota seam, duplicate mapping) hold on
// every path. System bootstrap (seeding) intentionally bypasses this usecase.
package schemacreate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// SchemaRepository persists new schema rows. Implemented by an app-layer
// adapter over the GORM schema repository; the adapter translates driver
// duplicate-key failures into pkgerrors.AlreadyExists.
type SchemaRepository interface {
	Create(ctx context.Context, record *SchemaRecord) error
}

// CreateGuard admits or rejects schema creation for a tenant before any row
// is written. Satisfied structurally by pkg/plugin.Plugin — CE's Noop always
// admits; an external plugin enforces the tenant's configured limit.
type CreateGuard interface {
	OnSchemaCreate(ctx context.Context, tenantID string, n int) error
}

// SchemaRecord is the usecase-boundary shape of a schema row. ID and
// CreatedAt are set by the repository on successful insert.
type SchemaRecord struct {
	ID           string
	Name         string
	Description  string
	EntryAgentID *string
	ChatEnabled  bool
	CreatedAt    time.Time
}

// Input represents input for the create-schema use case.
type Input struct {
	Name         string
	Description  string
	EntryAgentID *string
	ChatEnabled  bool
}

// Output represents output from the create-schema use case.
type Output struct {
	ID           string
	Name         string
	Description  string
	EntryAgentID *string
	ChatEnabled  bool
	CreatedAt    time.Time
}

// Usecase handles schema creation.
type Usecase struct {
	repo  SchemaRepository
	guard CreateGuard
}

// New creates a new schema-creation use case.
func New(repo SchemaRepository, guard CreateGuard) *Usecase {
	return &Usecase{repo: repo, guard: guard}
}

// Execute creates a new schema: validate → quota seam → persist.
func (u *Usecase) Execute(ctx context.Context, input Input) (*Output, error) {
	schema, err := domain.NewSchema(input.Name, input.Description)
	if err != nil {
		return nil, pkgerrors.InvalidInput(err.Error())
	}

	if err := u.guard.OnSchemaCreate(ctx, domain.TenantIDFromContext(ctx), 1); err != nil {
		if errors.Is(err, plugin.ErrSchemaQuotaExceeded) {
			return nil, pkgerrors.UsageLimited("schema limit reached: upgrade your plan or remove a schema to free a slot")
		}
		return nil, pkgerrors.Internal("schema creation admission", err)
	}

	record := &SchemaRecord{
		Name:         schema.Name,
		Description:  schema.Description,
		EntryAgentID: input.EntryAgentID,
		ChatEnabled:  input.ChatEnabled,
	}
	if err := u.repo.Create(ctx, record); err != nil {
		var domainErr *pkgerrors.DomainError
		if errors.As(err, &domainErr) {
			return nil, err
		}
		slog.ErrorContext(ctx, "failed to create schema", "error", err, "name", input.Name)
		return nil, fmt.Errorf("create schema: %w", err)
	}

	return &Output{
		ID:           record.ID,
		Name:         record.Name,
		Description:  record.Description,
		EntryAgentID: record.EntryAgentID,
		ChatEnabled:  record.ChatEnabled,
		CreatedAt:    record.CreatedAt,
	}, nil
}
