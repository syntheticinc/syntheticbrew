package agentregistry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
)

func TestBuildSystemPrompt_InlinePrompt(t *testing.T) {
	def := &config.AgentDefinition{
		Name:         "test-agent",
		SystemPrompt: "You are a helpful assistant.",
	}
	result, err := BuildSystemPrompt(def, "")
	require.NoError(t, err)
	assert.Equal(t, "You are a helpful assistant.", result)
}

func TestBuildSystemPrompt_FromFile(t *testing.T) {
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "prompt.md")
	err := os.WriteFile(promptFile, []byte("File-based prompt content."), 0644)
	require.NoError(t, err)

	def := &config.AgentDefinition{
		Name:             "test-agent",
		SystemPromptFile: "prompt.md",
	}
	result, err := BuildSystemPrompt(def, dir)
	require.NoError(t, err)
	assert.Equal(t, "File-based prompt content.", result)
}

func TestBuildSystemPrompt_FileOverridesInline(t *testing.T) {
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "prompt.md")
	err := os.WriteFile(promptFile, []byte("From file."), 0644)
	require.NoError(t, err)

	def := &config.AgentDefinition{
		Name:             "test-agent",
		SystemPrompt:     "Inline prompt.",
		SystemPromptFile: "prompt.md",
	}
	result, err := BuildSystemPrompt(def, dir)
	require.NoError(t, err)
	assert.Equal(t, "From file.", result)
}

func TestBuildSystemPrompt_WithFlowSteps(t *testing.T) {
	def := &config.AgentDefinition{
		Name:         "test-agent",
		SystemPrompt: "Base prompt.",
		Flow: &config.FlowConfig{
			Steps: []string{"Analyze the task", "Decompose into subtasks"},
		},
	}
	result, err := BuildSystemPrompt(def, "")
	require.NoError(t, err)
	assert.Contains(t, result, "Base prompt.")
	assert.Contains(t, result, "## Workflow")
	assert.Contains(t, result, "1. Analyze the task")
	assert.Contains(t, result, "2. Decompose into subtasks")
}

func TestBuildSystemPrompt_WithConfirmBefore(t *testing.T) {
	def := &config.AgentDefinition{
		Name:          "test-agent",
		SystemPrompt:  "Base prompt.",
		ConfirmBefore: []string{"create_order", "delete_user"},
	}
	result, err := BuildSystemPrompt(def, "")
	require.NoError(t, err)
	assert.Contains(t, result, "## Confirmation required")
	assert.Contains(t, result, "create_order, delete_user")
}

func TestBuildSystemPrompt_FullComposition(t *testing.T) {
	def := &config.AgentDefinition{
		Name:          "test-agent",
		SystemPrompt:  "You are a sales assistant.",
		ConfirmBefore: []string{"create_order"},
		Flow: &config.FlowConfig{
			Steps: []string{"Greet customer", "Process request"},
		},
	}
	result, err := BuildSystemPrompt(def, "")
	require.NoError(t, err)
	assert.Contains(t, result, "You are a sales assistant.")
	assert.Contains(t, result, "## Workflow")
	assert.Contains(t, result, "1. Greet customer")
	assert.Contains(t, result, "## Confirmation required")
	assert.Contains(t, result, "create_order")
}

func TestBuildSystemPrompt_NoPrompt_Error(t *testing.T) {
	def := &config.AgentDefinition{
		Name: "test-agent",
	}
	_, err := BuildSystemPrompt(def, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no system prompt content")
}

func TestBuildSystemPrompt_NonexistentFile_Error(t *testing.T) {
	def := &config.AgentDefinition{
		Name:             "test-agent",
		SystemPromptFile: "nonexistent.md",
	}
	_, err := BuildSystemPrompt(def, "/tmp/no-such-dir")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "load system_prompt_file")
}

func TestBuildSystemPrompt_EmptyFlowSteps_NoSection(t *testing.T) {
	def := &config.AgentDefinition{
		Name:         "test-agent",
		SystemPrompt: "Base prompt.",
		Flow:         &config.FlowConfig{Steps: []string{}},
	}
	result, err := BuildSystemPrompt(def, "")
	require.NoError(t, err)
	assert.NotContains(t, result, "## Workflow")
}
