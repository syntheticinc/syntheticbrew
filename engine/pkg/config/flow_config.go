package config

import (
	"fmt"
	"os"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"gopkg.in/yaml.v3"
)

// FlowsConfig holds all flow definitions from flows.yaml
type FlowsConfig struct {
	Flows map[string]FlowDefinition `yaml:"flows"`
}

// FlowDefinition defines a single flow configuration
type FlowDefinition struct {
	Name            string          `yaml:"name"`
	SystemPromptRef string          `yaml:"system_prompt_ref"`
	Tools           []string        `yaml:"tools"`
	MaxSteps        int             `yaml:"max_steps"`
	MaxContextSize  int             `yaml:"max_context_size"`
	Lifecycle       LifecycleConfig `yaml:"lifecycle"`
	SpawnPolicy     SpawnConfig     `yaml:"spawn_policy"`
}

// LifecycleConfig defines when a flow should suspend and where to report
type LifecycleConfig struct {
	SuspendOn []string `yaml:"suspend_on"`
	ReportTo  string   `yaml:"report_to"`
}

// SpawnConfig defines which flows can be spawned by this flow
type SpawnConfig struct {
	AllowedFlows  []string `yaml:"allowed_flows"`
	MaxConcurrent int      `yaml:"max_concurrent"`
}

// LoadFlowsConfig loads flows configuration from YAML file
func LoadFlowsConfig(path string) (*FlowsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read flows config: %w", err)
	}

	var cfg FlowsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse flows config: %w", err)
	}

	if len(cfg.Flows) == 0 {
		return nil, fmt.Errorf("no flows defined in %s", path)
	}

	return &cfg, nil
}

// ToDomainFlow converts a flow definition to a domain Flow using prompts for system prompt resolution
func (fc *FlowsConfig) ToDomainFlow(flowType string, prompts *PromptsConfig) (*domain.Flow, error) {
	def, ok := fc.Flows[flowType]
	if !ok {
		return nil, fmt.Errorf("unknown flow type: %s", flowType)
	}

	// Resolve system prompt from reference
	systemPrompt, err := resolvePromptRef(def.SystemPromptRef, prompts)
	if err != nil {
		return nil, fmt.Errorf("resolve prompt for flow %s: %w", flowType, err)
	}

	// Convert allowed flows
	allowedFlows := make([]string, 0, len(def.SpawnPolicy.AllowedFlows))
	for _, f := range def.SpawnPolicy.AllowedFlows {
		allowedFlows = append(allowedFlows, string(f))
	}

	flow := &domain.Flow{
		Type:           string(flowType),
		Name:           def.Name,
		SystemPrompt:   systemPrompt,
		ToolNames:      def.Tools,
		MaxSteps:       def.MaxSteps,
		MaxContextSize: def.MaxContextSize,
		Lifecycle: domain.LifecyclePolicy{
			SuspendOn: def.Lifecycle.SuspendOn,
			ReportTo:  def.Lifecycle.ReportTo,
		},
		Spawn: domain.SpawnPolicy{
			AllowedFlows:  allowedFlows,
			MaxConcurrent: def.SpawnPolicy.MaxConcurrent,
		},
	}

	if err := flow.Validate(); err != nil {
		return nil, fmt.Errorf("validate flow %s: %w", flowType, err)
	}

	return flow, nil
}

// resolvePromptRef resolves a prompt reference to actual prompt text
func resolvePromptRef(ref string, prompts *PromptsConfig) (string, error) {
	if prompts == nil {
		return "", fmt.Errorf("prompts config is nil")
	}

	switch ref {
	case "system_prompt":
		if prompts.SystemPrompt == "" {
			return "", fmt.Errorf("system_prompt is empty")
		}
		return prompts.SystemPrompt, nil
	case "supervisor_prompt":
		if prompts.SupervisorPrompt == "" {
			return "", fmt.Errorf("supervisor_prompt is empty")
		}
		return prompts.SupervisorPrompt, nil
	case "code_agent_prompt":
		if prompts.CodeAgentPrompt == "" {
			// Fallback to system prompt
			return prompts.SystemPrompt, nil
		}
		return prompts.CodeAgentPrompt, nil
	case "reviewer_prompt":
		if prompts.ReviewerPrompt == "" {
			return prompts.SystemPrompt, nil
		}
		return prompts.ReviewerPrompt, nil
	case "researcher_prompt":
		if prompts.ResearcherPrompt == "" {
			return prompts.SystemPrompt, nil
		}
		return prompts.ResearcherPrompt, nil
	default:
		return "", fmt.Errorf("unknown prompt reference: %s", ref)
	}
}
