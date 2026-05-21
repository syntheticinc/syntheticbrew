package capabilitylist

import (
	"context"
	"fmt"

	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// CapabilityRepository defines the repository interface for listing capabilities.
type CapabilityRepository interface {
	ListByAgent(ctx context.Context, agentName string) ([]CapabilityRecord, error)
}

// CapabilityRecord is a simplified record for the usecase boundary.
type CapabilityRecord struct {
	ID        uint
	AgentName string
	Type      string
	Config    map[string]interface{}
	Enabled   bool
}

// Output represents a single capability in the list.
type Output struct {
	ID      uint
	Type    string
	Config  map[string]interface{}
	Enabled bool
}

// Usecase handles capability listing for an agent.
type Usecase struct {
	repo CapabilityRepository
}

// New creates a new capability listing use case.
func New(repo CapabilityRepository) *Usecase {
	return &Usecase{repo: repo}
}

// Execute returns all capabilities for an agent.
func (u *Usecase) Execute(ctx context.Context, agentName string) ([]Output, error) {
	if agentName == "" {
		return nil, pkgerrors.InvalidInput("agent name is required")
	}

	records, err := u.repo.ListByAgent(ctx, agentName)
	if err != nil {
		return nil, fmt.Errorf("list capabilities: %w", err)
	}

	result := make([]Output, 0, len(records))
	for _, r := range records {
		result = append(result, Output{
			ID:      r.ID,
			Type:    r.Type,
			Config:  r.Config,
			Enabled: r.Enabled,
		})
	}
	return result, nil
}
