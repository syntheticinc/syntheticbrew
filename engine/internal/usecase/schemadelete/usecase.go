package schemadelete

import (
	"context"
	"fmt"
	"log/slog"

	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// SchemaRepository defines the repository interface for schema deletion.
type SchemaRepository interface {
	Delete(ctx context.Context, id uint) error
}

// Usecase handles schema deletion.
type Usecase struct {
	repo SchemaRepository
}

// New creates a new schema deletion use case.
func New(repo SchemaRepository) *Usecase {
	return &Usecase{repo: repo}
}

// Execute deletes a schema by ID.
func (u *Usecase) Execute(ctx context.Context, id uint) error {
	if id == 0 {
		return pkgerrors.InvalidInput("schema id is required")
	}

	if err := u.repo.Delete(ctx, id); err != nil {
		slog.ErrorContext(ctx, "failed to delete schema", "error", err, "id", id)
		return fmt.Errorf("delete schema: %w", err)
	}

	return nil
}
