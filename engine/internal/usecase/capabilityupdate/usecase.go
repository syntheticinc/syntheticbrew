package capabilityupdate

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// CapabilityRepository defines the repository interface for updating capabilities.
type CapabilityRepository interface {
	Update(ctx context.Context, id uint, record *CapabilityRecord) error
}

// CapabilityRecord is a simplified record for the usecase boundary.
type CapabilityRecord struct {
	Type    string
	Config  map[string]interface{}
	Enabled bool
}

// Input represents input for update capability use case.
type Input struct {
	ID      uint
	Type    string
	Config  map[string]interface{}
	Enabled bool
}

// Usecase handles capability update.
type Usecase struct {
	repo CapabilityRepository
}

// New creates a new update capability use case.
func New(repo CapabilityRepository) *Usecase {
	return &Usecase{repo: repo}
}

// Execute updates a capability.
func (u *Usecase) Execute(ctx context.Context, input Input) error {
	if input.ID == 0 {
		return pkgerrors.InvalidInput("capability id is required")
	}
	if input.Type != "" && !domain.CapabilityType(input.Type).IsValid() {
		return pkgerrors.InvalidInput(fmt.Sprintf("invalid capability type: %s", input.Type))
	}

	record := &CapabilityRecord{
		Type:    input.Type,
		Config:  input.Config,
		Enabled: input.Enabled,
	}
	if err := u.repo.Update(ctx, input.ID, record); err != nil {
		slog.ErrorContext(ctx, "failed to update capability", "error", err, "id", input.ID)
		return fmt.Errorf("update capability: %w", err)
	}

	return nil
}
