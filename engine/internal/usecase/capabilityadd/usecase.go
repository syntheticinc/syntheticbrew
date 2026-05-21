package capabilityadd

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// CapabilityRepository defines the repository interface for adding capabilities.
type CapabilityRepository interface {
	Create(ctx context.Context, record *CapabilityRecord) error
}

// CapabilityRecord is a simplified record for the usecase boundary.
type CapabilityRecord struct {
	ID        uint
	AgentName string
	Type      string
	Config    map[string]interface{}
	Enabled   bool
}

// Input represents input for add capability use case.
type Input struct {
	AgentName string
	Type      string
	Config    map[string]interface{}
	Enabled   bool
}

// Output represents output from add capability use case.
type Output struct {
	ID        uint
	AgentName string
	Type      string
	Config    map[string]interface{}
	Enabled   bool
}

// Usecase handles capability creation.
type Usecase struct {
	repo CapabilityRepository
}

// New creates a new add capability use case.
func New(repo CapabilityRepository) *Usecase {
	return &Usecase{repo: repo}
}

// Execute adds a capability to an agent.
func (u *Usecase) Execute(ctx context.Context, input Input) (*Output, error) {
	cap, err := domain.NewCapability(input.AgentName, domain.CapabilityType(input.Type), input.Config)
	if err != nil {
		return nil, pkgerrors.InvalidInput(err.Error())
	}

	record := &CapabilityRecord{
		AgentName: cap.AgentName,
		Type:      string(cap.Type),
		Config:    cap.Config,
		Enabled:   input.Enabled,
	}
	if err := u.repo.Create(ctx, record); err != nil {
		slog.ErrorContext(ctx, "failed to add capability", "error", err, "agent", input.AgentName, "type", input.Type)
		return nil, fmt.Errorf("add capability: %w", err)
	}

	return &Output{
		ID:        record.ID,
		AgentName: record.AgentName,
		Type:      record.Type,
		Config:    record.Config,
		Enabled:   record.Enabled,
	}, nil
}
