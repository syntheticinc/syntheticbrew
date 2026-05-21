package agent

import (
	"context"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
)

// FlowManager manages flow configurations
type FlowManager struct {
	flows map[string]*domain.Flow
}

// NewFlowManager creates a FlowManager from config
func NewFlowManager(flowsCfg *config.FlowsConfig, prompts *config.PromptsConfig) (*FlowManager, error) {
	if flowsCfg == nil {
		return nil, fmt.Errorf("flows config is required")
	}

	flows := make(map[string]*domain.Flow)
	for flowType := range flowsCfg.Flows {
		flow, err := flowsCfg.ToDomainFlow(flowType, prompts)
		if err != nil {
			return nil, fmt.Errorf("create flow %s: %w", flowType, err)
		}
		flows[flowType] = flow
	}

	return &FlowManager{flows: flows}, nil
}

// GetFlow returns flow configuration by agent name
func (m *FlowManager) GetFlow(ctx context.Context, agentName string) (*domain.Flow, error) {
	flow, ok := m.flows[agentName]
	if !ok {
		return nil, fmt.Errorf("unknown flow type: %s", agentName)
	}
	return flow, nil
}
