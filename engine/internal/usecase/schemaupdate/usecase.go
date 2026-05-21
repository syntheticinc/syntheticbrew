package schemaupdate

import (
	"context"
	"fmt"
	"log/slog"

	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// SchemaRepository defines the repository interface for schema update.
type SchemaRepository interface {
	Update(ctx context.Context, id uint, record *SchemaRecord) error
}

// SchemaRecord is a simplified record for the usecase boundary.
type SchemaRecord struct {
	Name        string
	Description string
}

// Input represents input for update schema use case.
type Input struct {
	ID          uint
	Name        string
	Description string
}

// Usecase handles schema update.
type Usecase struct {
	repo SchemaRepository
}

// New creates a new schema update use case.
func New(repo SchemaRepository) *Usecase {
	return &Usecase{repo: repo}
}

// Execute updates an existing schema.
func (u *Usecase) Execute(ctx context.Context, input Input) error {
	if input.ID == 0 {
		return pkgerrors.InvalidInput("schema id is required")
	}
	if input.Name == "" {
		return pkgerrors.InvalidInput("schema name is required")
	}

	record := &SchemaRecord{
		Name:        input.Name,
		Description: input.Description,
	}
	if err := u.repo.Update(ctx, input.ID, record); err != nil {
		slog.ErrorContext(ctx, "failed to update schema", "error", err, "id", input.ID)
		return fmt.Errorf("update schema: %w", err)
	}

	return nil
}
