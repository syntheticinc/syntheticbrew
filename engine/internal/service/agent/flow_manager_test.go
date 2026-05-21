package agent

import (
	"context"
	"testing"

	"github.com/syntheticinc/syntheticbrew/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlowManager_GetFlow_AllTypes(t *testing.T) {
	// Create FlowsConfig with all 4 flow types
	flowsCfg := &config.FlowsConfig{
		Flows: map[string]config.FlowDefinition{
			"supervisor": {
				Name:            "Supervisor Agent",
				SystemPromptRef: "supervisor_prompt",
				Tools:           []string{"read_file", "spawn_agent"},
				MaxSteps:        50,
				MaxContextSize:  16000,
				Lifecycle: config.LifecycleConfig{
					SuspendOn: []string{"final_answer"},
					ReportTo:  "user",
				},
				SpawnPolicy: config.SpawnConfig{
					AllowedFlows: []string{"coder", "reviewer", "researcher"},
				},
			},
			"coder": {
				Name:            "Code Agent",
				SystemPromptRef: "code_agent_prompt",
				Tools:           []string{"read_file", "write_file"},
				MaxSteps:        30,
				MaxContextSize:  16000,
				Lifecycle: config.LifecycleConfig{
					SuspendOn: []string{"final_answer"},
					ReportTo:  "parent_agent",
				},
				SpawnPolicy: config.SpawnConfig{
					AllowedFlows: []string{},
				},
			},
			"reviewer": {
				Name:            "Code Reviewer",
				SystemPromptRef: "system_prompt",
				Tools:           []string{"read_file", "search_code"},
				MaxSteps:        20,
				MaxContextSize:  16000,
				Lifecycle: config.LifecycleConfig{
					SuspendOn: []string{"final_answer"},
					ReportTo:  "parent_agent",
				},
				SpawnPolicy: config.SpawnConfig{
					AllowedFlows: []string{},
				},
			},
			"researcher": {
				Name:            "Researcher",
				SystemPromptRef: "system_prompt",
				Tools:           []string{"read_file", "web_search"},
				MaxSteps:        25,
				MaxContextSize:  16000,
				Lifecycle: config.LifecycleConfig{
					SuspendOn: []string{"final_answer"},
					ReportTo:  "parent_agent",
				},
				SpawnPolicy: config.SpawnConfig{
					AllowedFlows: []string{},
				},
			},
		},
	}

	prompts := &config.PromptsConfig{
		SystemPrompt:     "Default system prompt",
		SupervisorPrompt: "You are a supervisor agent",
		CodeAgentPrompt:  "You are a code agent",
	}

	manager, err := NewFlowManager(flowsCfg, prompts)
	require.NoError(t, err)
	require.NotNil(t, manager)

	ctx := context.Background()

	// Test all 4 flow types
	tests := []struct {
		agentName string
		wantName  string
	}{
		{"supervisor", "Supervisor Agent"},
		{"coder", "Code Agent"},
		{"reviewer", "Code Reviewer"},
		{"researcher", "Researcher"},
	}

	for _, tt := range tests {
		t.Run(tt.agentName, func(t *testing.T) {
			flow, err := manager.GetFlow(ctx, tt.agentName)
			require.NoError(t, err)
			require.NotNil(t, flow)
			assert.Equal(t, tt.agentName, flow.Type)
			assert.Equal(t, tt.wantName, flow.Name)
		})
	}
}

func TestFlowManager_GetFlow_UnknownType(t *testing.T) {
	flowsCfg := &config.FlowsConfig{
		Flows: map[string]config.FlowDefinition{
			"supervisor": {
				Name:            "Supervisor Agent",
				SystemPromptRef: "supervisor_prompt",
				Tools:           []string{"read_file"},
				MaxSteps:        50,
				MaxContextSize:  16000,
				Lifecycle: config.LifecycleConfig{
					SuspendOn: []string{"final_answer"},
					ReportTo:  "user",
				},
				SpawnPolicy: config.SpawnConfig{
					AllowedFlows: []string{},
				},
			},
		},
	}

	prompts := &config.PromptsConfig{
		SystemPrompt:     "Default system prompt",
		SupervisorPrompt: "You are a supervisor agent",
	}

	manager, err := NewFlowManager(flowsCfg, prompts)
	require.NoError(t, err)

	ctx := context.Background()
	flow, err := manager.GetFlow(ctx, "unknown_flow")
	assert.Error(t, err)
	assert.Nil(t, flow)
	assert.Contains(t, err.Error(), "unknown flow type")
}

func TestFlowManager_NilConfig(t *testing.T) {
	prompts := &config.PromptsConfig{
		SystemPrompt: "Default system prompt",
	}

	manager, err := NewFlowManager(nil, prompts)
	assert.Error(t, err)
	assert.Nil(t, manager)
	assert.Contains(t, err.Error(), "flows config is required")
}

func TestFlowManager_SupervisorCanSpawn(t *testing.T) {
	flowsCfg := &config.FlowsConfig{
		Flows: map[string]config.FlowDefinition{
			"supervisor": {
				Name:            "Supervisor Agent",
				SystemPromptRef: "supervisor_prompt",
				Tools:           []string{"read_file"},
				MaxSteps:        50,
				MaxContextSize:  16000,
				Lifecycle: config.LifecycleConfig{
					SuspendOn: []string{"final_answer"},
					ReportTo:  "user",
				},
				SpawnPolicy: config.SpawnConfig{
					AllowedFlows: []string{"coder", "reviewer", "researcher"},
				},
			},
		},
	}

	prompts := &config.PromptsConfig{
		SystemPrompt:     "Default system prompt",
		SupervisorPrompt: "You are a supervisor agent",
	}

	manager, err := NewFlowManager(flowsCfg, prompts)
	require.NoError(t, err)

	ctx := context.Background()
	supervisorFlow, err := manager.GetFlow(ctx, "supervisor")
	require.NoError(t, err)

	// Supervisor should be able to spawn coder, reviewer, researcher
	assert.True(t, supervisorFlow.CanSpawn("coder"))
	assert.True(t, supervisorFlow.CanSpawn("reviewer"))
	assert.True(t, supervisorFlow.CanSpawn("researcher"))

	// Supervisor cannot spawn another supervisor
	assert.False(t, supervisorFlow.CanSpawn("supervisor"))
}
