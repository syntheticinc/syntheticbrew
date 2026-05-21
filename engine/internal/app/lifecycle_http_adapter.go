package app

import (
	"context"

	deliveryhttp "github.com/syntheticinc/syntheticbrew/internal/delivery/http"
	"github.com/syntheticinc/syntheticbrew/internal/service/lifecycle"
)

// lifecycleHTTPAdapter bridges lifecycle.Manager to the HTTP LifecycleProvider interface.
type lifecycleHTTPAdapter struct {
	manager *lifecycle.Manager
	agents  AgentLifecycleReader
}

// newLifecycleHTTPAdapter creates a new adapter.
func newLifecycleHTTPAdapter(manager *lifecycle.Manager, agents AgentLifecycleReader) *lifecycleHTTPAdapter {
	return &lifecycleHTTPAdapter{
		manager: manager,
		agents:  agents,
	}
}

// GetLifecycleStatus implements deliveryhttp.LifecycleProvider.
func (a *lifecycleHTTPAdapter) GetLifecycleStatus(ctx context.Context, agentName, sessionID string) (*deliveryhttp.LifecycleStatus, error) {
	mode := a.agents.GetLifecycleMode(ctx, agentName)
	maxContext := a.agents.GetMaxContextSize(ctx, agentName)

	// If no sessionID provided, return agent config without instance state
	if sessionID == "" {
		return &deliveryhttp.LifecycleStatus{
			Mode:       string(mode),
			State:      "no_session",
			MaxContext: maxContext,
		}, nil
	}

	instance, ok := a.manager.GetInstance(ctx, agentName, sessionID)
	if !ok {
		return &deliveryhttp.LifecycleStatus{
			Mode:       string(mode),
			State:      "idle",
			MaxContext: maxContext,
		}, nil
	}

	return &deliveryhttp.LifecycleStatus{
		Mode:          string(instance.Mode),
		State:         string(instance.State()),
		TasksHandled:  instance.TasksHandled,
		ContextTokens: instance.ContextTokens,
		MaxContext:    instance.MaxContext,
	}, nil
}
