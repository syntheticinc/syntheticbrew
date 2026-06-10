package domain

import "fmt"

// LifecyclePolicy defines when a flow should suspend and where to report
type LifecyclePolicy struct {
	SuspendOn []string // events that cause suspension, e.g. "final_answer"
	ReportTo  string   // "user" or "parent_agent"
}

// SpawnPolicy defines which flows can be spawned by this flow
type SpawnPolicy struct {
	AllowedFlows  []string
	MaxConcurrent int // 0 = no limit (backward compatibility)
}

// Flow represents a flow configuration (agent behavior template)
type Flow struct {
	Type           string
	Name           string
	SystemPrompt   string
	ToolNames      []string
	MaxSteps       int
	MaxContextSize  int
	MaxTurnDuration int // max seconds for a single LLM stream turn (0 = use default 120s)
	MaxStepDuration int // max seconds for a single ReAct step (0 = disabled, opt-in)
	ToolExecution   string // "sequential" or "parallel"
	Lifecycle      LifecyclePolicy
	Spawn          SpawnPolicy
	MCPServers     []string // MCP server names configured for this agent
	ConfirmBefore  []string  // tools requiring user confirmation before execution
	Temperature    *float64 // per-agent LLM temperature override (nil = use model default)
	TopP           *float64 // per-agent top_p override (nil = use model default)
	MaxTokens      *int     // per-agent max_tokens override (nil = use model default)
	StopSequences  []string // per-agent stop sequences override (nil = use model default)
}

// Validate validates the Flow configuration
func (f *Flow) Validate() error {
	if f.Type == "" {
		return fmt.Errorf("flow type is required")
	}
	if f.Name == "" {
		return fmt.Errorf("flow name is required")
	}
	if f.SystemPrompt == "" {
		return fmt.Errorf("system prompt is required")
	}
	if f.MaxSteps < 0 {
		return fmt.Errorf("max_steps must be non-negative (0 = unlimited)")
	}
	if f.MaxContextSize < 0 {
		return fmt.Errorf("max_context_size must be non-negative (0 = unlimited)")
	}
	return nil
}

// CanSpawn returns true if this flow can spawn the specified flow type
func (f *Flow) CanSpawn(flowType string) bool {
	for _, allowed := range f.Spawn.AllowedFlows {
		if allowed == flowType {
			return true
		}
	}
	return false
}

// ShouldSuspendOn returns true if this flow should suspend on the specified event
func (f *Flow) ShouldSuspendOn(event string) bool {
	for _, e := range f.Lifecycle.SuspendOn {
		if e == event {
			return true
		}
	}
	return false
}
