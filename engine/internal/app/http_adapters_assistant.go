package app

import (
	"context"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	"github.com/syntheticinc/syntheticbrew/internal/service/capability"
)

// capabilityInjectorAdapter bridges GORMCapabilityRepository to capability.CapabilityReader.
type capabilityInjectorAdapter struct {
	repo *configrepo.GORMCapabilityRepository
}

func (a *capabilityInjectorAdapter) ListEnabledByAgent(ctx context.Context, agentName string) ([]capability.CapabilityRecord, error) {
	records, err := a.repo.ListEnabledByAgent(ctx, agentName)
	if err != nil {
		return nil, err
	}
	result := make([]capability.CapabilityRecord, 0, len(records))
	for _, r := range records {
		result = append(result, capability.CapabilityRecord{
			ID:        r.ID,
			AgentName: r.AgentName,
			Type:      r.Type,
			Config:    r.Config,
			Enabled:   r.Enabled,
		})
	}
	return result, nil
}
