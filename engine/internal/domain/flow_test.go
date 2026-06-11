package domain

import "testing"

func TestFlow_Validate(t *testing.T) {
	flow := &Flow{
		Type:           "supervisor",
		Name:           "main-supervisor",
		SystemPrompt:   "You are a supervisor agent",
		ToolNames:      []string{"manage_stories", "spawn_agent"},
		MaxSteps:       50,
		MaxContextSize: 100000,
		Lifecycle: LifecyclePolicy{
			SuspendOn: []string{"final_answer"},
			ReportTo:  "user",
		},
		Spawn: SpawnPolicy{
			AllowedFlows: []string{"coder"},
		},
	}

	if err := flow.Validate(); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestFlow_Validate_MissingType(t *testing.T) {
	flow := &Flow{
		Name:           "test",
		SystemPrompt:   "test",
		MaxSteps:       10,
		MaxContextSize: 1000,
	}

	err := flow.Validate()
	if err == nil {
		t.Error("expected error for missing type, got nil")
	}
	if err.Error() != "flow type is required" {
		t.Errorf("expected 'flow type is required', got: %v", err)
	}
}

func TestFlow_Validate_MissingName(t *testing.T) {
	flow := &Flow{
		Type:           "coder",
		SystemPrompt:   "test",
		MaxSteps:       10,
		MaxContextSize: 1000,
	}

	err := flow.Validate()
	if err == nil {
		t.Error("expected error for missing name, got nil")
	}
	if err.Error() != "flow name is required" {
		t.Errorf("expected 'flow name is required', got: %v", err)
	}
}

func TestFlow_Validate_MissingPrompt(t *testing.T) {
	flow := &Flow{
		Type:           "coder",
		Name:           "test",
		MaxSteps:       10,
		MaxContextSize: 1000,
	}

	err := flow.Validate()
	if err == nil {
		t.Error("expected error for missing prompt, got nil")
	}
	if err.Error() != "system prompt is required" {
		t.Errorf("expected 'system prompt is required', got: %v", err)
	}
}

func TestFlow_Validate_ZeroMaxSteps_IsValid(t *testing.T) {
	flow := &Flow{
		Type:           "coder",
		Name:           "test",
		SystemPrompt:   "test",
		MaxSteps:       0, // 0 = unlimited, should be valid
		MaxContextSize: 1000,
	}

	err := flow.Validate()
	if err != nil {
		t.Errorf("expected no error for zero max_steps (unlimited), got: %v", err)
	}
}

func TestFlow_Validate_NegativeMaxSteps(t *testing.T) {
	flow := &Flow{
		Type:           "coder",
		Name:           "test",
		SystemPrompt:   "test",
		MaxSteps:       -1,
		MaxContextSize: 1000,
	}

	err := flow.Validate()
	if err == nil {
		t.Error("expected error for negative max_steps, got nil")
	}
}

func TestFlow_Validate_ZeroMaxContextSize(t *testing.T) {
	flow := &Flow{
		Type:           "coder",
		Name:           "test",
		SystemPrompt:   "test",
		MaxSteps:       10,
		MaxContextSize: 0,
	}

	if err := flow.Validate(); err != nil {
		t.Errorf("expected no error for zero max_context_size (0 = unlimited), got: %v", err)
	}
}

func TestFlow_Validate_NegativeMaxContextSize(t *testing.T) {
	flow := &Flow{
		Type:           "coder",
		Name:           "test",
		SystemPrompt:   "test",
		MaxSteps:       10,
		MaxContextSize: -1,
	}

	err := flow.Validate()
	if err == nil {
		t.Error("expected error for negative max_context_size, got nil")
	}
	if err.Error() != "max_context_size must be non-negative (0 = unlimited)" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestFlow_Validate_MaxContextSizeAboveCeiling(t *testing.T) {
	flow := &Flow{
		Type:           "coder",
		Name:           "test",
		SystemPrompt:   "test",
		MaxSteps:       10,
		MaxContextSize: MaxContextSizeCeiling + 1,
	}

	if err := flow.Validate(); err == nil {
		t.Error("expected error for max_context_size above the ceiling, got nil")
	}

	atCeiling := &Flow{Type: "coder", Name: "test", SystemPrompt: "test", MaxSteps: 10, MaxContextSize: MaxContextSizeCeiling}
	if err := atCeiling.Validate(); err != nil {
		t.Errorf("expected no error at the ceiling, got: %v", err)
	}
}

func TestFlow_CanSpawn(t *testing.T) {
	flow := &Flow{
		Type:           "supervisor",
		Name:           "supervisor",
		SystemPrompt:   "test",
		MaxSteps:       10,
		MaxContextSize: 1000,
		Spawn: SpawnPolicy{
			AllowedFlows: []string{"coder", "reviewer"},
		},
	}

	if !flow.CanSpawn("coder") {
		t.Error("expected supervisor to be able to spawn coder")
	}

	if !flow.CanSpawn("reviewer") {
		t.Error("expected supervisor to be able to spawn reviewer")
	}
}

func TestFlow_CanSpawn_NotAllowed(t *testing.T) {
	flow := &Flow{
		Type:           "coder",
		Name:           "coder",
		SystemPrompt:   "test",
		MaxSteps:       10,
		MaxContextSize: 1000,
		Spawn: SpawnPolicy{
			AllowedFlows: []string{},
		},
	}

	if flow.CanSpawn("supervisor") {
		t.Error("expected coder not to be able to spawn supervisor")
	}

	if flow.CanSpawn("coder") {
		t.Error("expected coder not to be able to spawn coder")
	}
}

func TestFlow_ShouldSuspendOn(t *testing.T) {
	flow := &Flow{
		Type:           "supervisor",
		Name:           "supervisor",
		SystemPrompt:   "test",
		MaxSteps:       10,
		MaxContextSize: 1000,
		Lifecycle: LifecyclePolicy{
			SuspendOn: []string{"final_answer"},
			ReportTo:  "user",
		},
	}

	if !flow.ShouldSuspendOn("final_answer") {
		t.Error("expected flow to suspend on final_answer")
	}
}

func TestFlow_ShouldSuspendOn_Unknown(t *testing.T) {
	flow := &Flow{
		Type:           "supervisor",
		Name:           "supervisor",
		SystemPrompt:   "test",
		MaxSteps:       10,
		MaxContextSize: 1000,
		Lifecycle: LifecyclePolicy{
			SuspendOn: []string{"final_answer"},
			ReportTo:  "user",
		},
	}

	if flow.ShouldSuspendOn("unknown_event") {
		t.Error("expected flow not to suspend on unknown_event")
	}
}
