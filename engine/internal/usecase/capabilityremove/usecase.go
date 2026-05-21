package capabilityremove

import (
	"context"
	"fmt"
	"log/slog"

	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// CapabilityRepository defines the repository interface for removing capabilities.
type CapabilityRepository interface {
	Delete(ctx context.Context, id uint) error
}

// Usecase handles capability removal.
type Usecase struct {
	repo CapabilityRepository
}

// New creates a new remove capability use case.
func New(repo CapabilityRepository) *Usecase {
	return &Usecase{repo: repo}
}

// Execute removes a capability by ID.
func (u *Usecase) Execute(ctx context.Context, id uint) error {
	if id == 0 {
		return pkgerrors.InvalidInput("capability id is required")
	}

	if err := u.repo.Delete(ctx, id); err != nil {
		slog.ErrorContext(ctx, "failed to remove capability", "error", err, "id", id)
		return fmt.Errorf("remove capability: %w", err)
	}

	return nil
}
