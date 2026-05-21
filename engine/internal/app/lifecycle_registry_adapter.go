package app

import (
	"context"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agentregistry"
)

// agentRegistryLifecycleAdapter adapts AgentRegistry to the AgentLifecycleReader interface.
type agentRegistryLifecycleAdapter struct {
	registry *agentregistry.AgentRegistry
}

// newAgentRegistryLifecycleAdapter creates a new adapter.
func newAgentRegistryLifecycleAdapter(registry *agentregistry.AgentRegistry) *agentRegistryLifecycleAdapter {
	return &agentRegistryLifecycleAdapter{registry: registry}
}

func (a *agentRegistryLifecycleAdapter) GetLifecycleMode(_ context.Context, agentName string) domain.LifecycleMode {
	agent, err := a.registry.Get(agentName)
	if err != nil {
		return domain.LifecycleModeSpawn
	}

	switch agent.Record.Lifecycle {
	case "persistent":
		return domain.LifecycleModePersistent
	default:
		return domain.LifecycleModeSpawn
	}
}

func (a *agentRegistryLifecycleAdapter) GetMaxContextSize(_ context.Context, agentName string) int {
	agent, err := a.registry.Get(agentName)
	if err != nil {
		return 16000
	}

	if agent.Record.MaxContextSize > 0 {
		return agent.Record.MaxContextSize
	}
	return 16000
}

// managerLifecycleAdapter adapts agentregistry.Manager to AgentLifecycleReader for multi-tenant mode.
// It resolves the per-tenant registry from the request context on each call.
type managerLifecycleAdapter struct {
	mgr *agentregistry.Manager
}

func newManagerLifecycleAdapter(mgr *agentregistry.Manager) *managerLifecycleAdapter {
	return &managerLifecycleAdapter{mgr: mgr}
}

func (a *managerLifecycleAdapter) GetLifecycleMode(ctx context.Context, agentName string) domain.LifecycleMode {
	reg, err := a.mgr.GetForContext(ctx)
	if err != nil {
		return domain.LifecycleModeSpawn
	}
	agent, err := reg.Get(agentName)
	if err != nil {
		return domain.LifecycleModeSpawn
	}
	switch agent.Record.Lifecycle {
	case "persistent":
		return domain.LifecycleModePersistent
	default:
		return domain.LifecycleModeSpawn
	}
}

func (a *managerLifecycleAdapter) GetMaxContextSize(ctx context.Context, agentName string) int {
	reg, err := a.mgr.GetForContext(ctx)
	if err != nil {
		return 16000
	}
	agent, err := reg.Get(agentName)
	if err != nil {
		return 16000
	}
	if agent.Record.MaxContextSize > 0 {
		return agent.Record.MaxContextSize
	}
	return 16000
}
