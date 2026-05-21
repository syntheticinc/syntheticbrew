package memorystore

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// MemoryRepository persists memory entries.
type MemoryRepository interface {
	Store(ctx context.Context, mem *domain.Memory, maxEntries int) error
}

// Input holds the input for storing a memory entry.
type Input struct {
	SchemaID   string
	UserSub    string
	Content    string
	Metadata   map[string]string
	MaxEntries int // 0 = unlimited
}

// Usecase stores a memory entry.
type Usecase struct {
	repo MemoryRepository
}

// New creates a new memory_store usecase.
func New(repo MemoryRepository) *Usecase {
	return &Usecase{repo: repo}
}

// Execute stores a memory entry.
func (u *Usecase) Execute(ctx context.Context, input Input) (*domain.Memory, error) {
	mem, err := domain.NewMemory(input.SchemaID, input.UserSub, input.Content)
	if err != nil {
		return nil, fmt.Errorf("create memory: %w", err)
	}

	for k, v := range input.Metadata {
		mem.AddMetadata(k, v)
	}

	if err := u.repo.Store(ctx, mem, input.MaxEntries); err != nil {
		return nil, fmt.Errorf("store memory: %w", err)
	}

	slog.InfoContext(ctx, "memory stored",
		"schema_id", input.SchemaID, "user_id", input.UserSub)

	return mem, nil
}
