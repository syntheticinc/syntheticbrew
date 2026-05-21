package schemaget

import (
	"context"
	"fmt"

	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// SchemaRepository defines the repository interface for schema retrieval.
type SchemaRepository interface {
	GetByID(ctx context.Context, id uint) (*SchemaRecord, error)
}

// SchemaRecord is a simplified record for the usecase boundary.
type SchemaRecord struct {
	ID          uint
	Name        string
	Description string
	AgentNames  []string
}

// Output represents output from get schema use case.
type Output struct {
	ID          uint
	Name        string
	Description string
	AgentNames  []string
}

// Usecase handles schema retrieval by ID.
type Usecase struct {
	repo SchemaRepository
}

// New creates a new get schema use case.
func New(repo SchemaRepository) *Usecase {
	return &Usecase{repo: repo}
}

// Execute retrieves a schema by ID.
func (u *Usecase) Execute(ctx context.Context, id uint) (*Output, error) {
	if id == 0 {
		return nil, pkgerrors.InvalidInput("schema id is required")
	}

	record, err := u.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get schema: %w", err)
	}

	return &Output{
		ID:          record.ID,
		Name:        record.Name,
		Description: record.Description,
		AgentNames:  record.AgentNames,
	}, nil
}
