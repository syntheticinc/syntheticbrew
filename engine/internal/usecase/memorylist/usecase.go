package memorylist

import (
	"context"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// MemoryRepository retrieves memory entries for admin view.
type MemoryRepository interface {
	ListBySchema(ctx context.Context, schemaID string) ([]*domain.Memory, error)
}

// Usecase lists all memories for a schema (admin view, AC-MEM-03).
type Usecase struct {
	repo MemoryRepository
}

// New creates a new memory_list usecase.
func New(repo MemoryRepository) *Usecase {
	return &Usecase{repo: repo}
}

// Execute retrieves all memories for a schema.
func (u *Usecase) Execute(ctx context.Context, schemaID string) ([]*domain.Memory, error) {
	if schemaID == "" {
		return nil, fmt.Errorf("schema_id is required")
	}

	memories, err := u.repo.ListBySchema(ctx, schemaID)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}

	return memories, nil
}
