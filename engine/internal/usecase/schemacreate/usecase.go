package schemacreate

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// SchemaRepository defines the repository interface for schema creation.
type SchemaRepository interface {
	Create(ctx context.Context, record *SchemaRecord) error
}

// SchemaRecord is a simplified record for the usecase boundary.
type SchemaRecord struct {
	ID          uint
	Name        string
	Description string
}

// Input represents input for create schema use case.
type Input struct {
	Name        string
	Description string
}

// Output represents output from create schema use case.
type Output struct {
	ID          uint
	Name        string
	Description string
}

// Usecase handles schema creation.
type Usecase struct {
	repo SchemaRepository
}

// New creates a new schema creation use case.
func New(repo SchemaRepository) *Usecase {
	return &Usecase{repo: repo}
}

// Execute creates a new schema.
func (u *Usecase) Execute(ctx context.Context, input Input) (*Output, error) {
	schema, err := domain.NewSchema(input.Name, input.Description)
	if err != nil {
		return nil, pkgerrors.InvalidInput(err.Error())
	}

	record := &SchemaRecord{
		Name:        schema.Name,
		Description: schema.Description,
	}
	if err := u.repo.Create(ctx, record); err != nil {
		slog.ErrorContext(ctx, "failed to create schema", "error", err, "name", input.Name)
		return nil, fmt.Errorf("create schema: %w", err)
	}

	return &Output{
		ID:          record.ID,
		Name:        record.Name,
		Description: record.Description,
	}, nil
}
