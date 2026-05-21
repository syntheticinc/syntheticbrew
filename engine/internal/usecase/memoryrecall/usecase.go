package memoryrecall

import (
	"context"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// MemoryRepository retrieves memory entries.
type MemoryRepository interface {
	ListBySchemaAndUser(ctx context.Context, schemaID, userID string) ([]*domain.Memory, error)
}

// Input holds the input for recalling memories.
type Input struct {
	SchemaID string
	UserID   string
}

// Usecase recalls memories for a schema+user pair.
type Usecase struct {
	repo MemoryRepository
}

// New creates a new memory_recall usecase.
func New(repo MemoryRepository) *Usecase {
	return &Usecase{repo: repo}
}

// Execute retrieves memories for the given schema+user.
func (u *Usecase) Execute(ctx context.Context, input Input) ([]*domain.Memory, error) {
	if input.SchemaID == "" {
		return nil, fmt.Errorf("schema_id is required")
	}
	if input.UserID == "" {
		return nil, fmt.Errorf("user_id is required")
	}

	memories, err := u.repo.ListBySchemaAndUser(ctx, input.SchemaID, input.UserID)
	if err != nil {
		return nil, fmt.Errorf("recall memories: %w", err)
	}

	return memories, nil
}
