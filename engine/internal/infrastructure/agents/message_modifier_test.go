package agents

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/syntheticinc/syntheticbrew/pkg/config"
	"github.com/cloudwego/eino/schema"
)

// createTestMessageModifier creates a message modifier function for testing
// This mirrors the logic in react_agent.go NewReActAgent
func createTestMessageModifier(systemPrompt, urgencyWarning string, maxSteps int, stepContentStore *StepContentStore) (func(ctx context.Context, input []*schema.Message, stepCounter int) []*schema.Message, *int) {
	stepCounter := 0
	return func(ctx context.Context, input []*schema.Message, step int) []*schema.Message {
		stepCounter = step
		remainingSteps := maxSteps - stepCounter

		// Build system prompt with urgency warning if approaching max steps
		currentSystemPrompt := systemPrompt
		if remainingSteps <= 3 && remainingSteps > 0 && urgencyWarning != "" {
			currentSystemPrompt = currentSystemPrompt + fmt.Sprintf(urgencyWarning, remainingSteps)
		}

		// Find and extract LATEST user question for task reminder
		var userQuestion string
		for _, msg := range input {
			if msg.Role == schema.User && msg.Content != "" {
				userQuestion = msg.Content
				// Don't break - continue to find the LAST user message
			}
		}

		// Add task reminder to system prompt after several steps
		if stepCounter >= 2 && userQuestion != "" {
			currentSystemPrompt += fmt.Sprintf("\n\n**CURRENT TASK (Step %d):** Answer the user's question: \"%s\"\nDo NOT get distracted - answer THIS question!", stepCounter, userQuestion)
		}

		// Add system prompt at the beginning
		result := make([]*schema.Message, 0, len(input)+1)
		result = append(result, schema.SystemMessage(currentSystemPrompt))

		// CRITICAL FIX: Inject accumulated content into empty assistant messages
		stepContent := stepContentStore.GetAll()

		// Track which step each assistant message corresponds to
		assistantStepIdx := 0
		for _, msg := range input {
			if msg.Role == schema.Assistant {
				// If assistant message has tool_calls but empty content, try to fill it
				if msg.Content == "" && len(msg.ToolCalls) > 0 {
					if content, ok := stepContent[assistantStepIdx]; ok && content != "" {
						// Create a copy with filled content
						filledMsg := &schema.Message{
							Role:      msg.Role,
							Content:   content,
							ToolCalls: msg.ToolCalls,
							Name:      msg.Name,
						}
						result = append(result, filledMsg)
					} else {
						result = append(result, msg)
					}
				} else {
					result = append(result, msg)
				}
				assistantStepIdx++
			} else {
				result = append(result, msg)
			}
		}

		return result
	}, &stepCounter
}

func TestMessageModifier_SystemPromptInjection(t *testing.T) {
	systemPrompt := "You are a helpful assistant."
	store := NewStepContentStore()
	modifier, _ := createTestMessageModifier(systemPrompt, "", 10, store)

	input := []*schema.Message{
		{Role: schema.User, Content: "Hello"},
	}

	result := modifier(context.Background(), input, 0)

	// First message should be system prompt
	if len(result) == 0 {
		t.Fatal("result is empty")
	}
	if result[0].Role != schema.System {
		t.Errorf("first message role: got %v, want %v", result[0].Role, schema.System)
	}
	if result[0].Content != systemPrompt {
		t.Errorf("system prompt: got %q, want %q", result[0].Content, systemPrompt)
	}

	// Should have 2 messages total (system + user)
	if len(result) != 2 {
		t.Errorf("result length: got %d, want 2", len(result))
	}
}

func TestMessageModifier_UrgencyWarning_Step7(t *testing.T) {
	systemPrompt := "You are a helpful assistant."
	urgencyWarning := "\n\nWARNING: Only %d steps remaining!"
	maxSteps := 10
	store := NewStepContentStore()
	modifier, _ := createTestMessageModifier(systemPrompt, urgencyWarning, maxSteps, store)

	input := []*schema.Message{
		{Role: schema.User, Content: "Hello"},
	}

	// Step 7 means remainingSteps = 10 - 7 = 3, so warning should be added
	result := modifier(context.Background(), input, 7)

	if !strings.Contains(result[0].Content, "WARNING:") {
		t.Errorf("expected urgency warning at step 7, got: %s", result[0].Content)
	}
	if !strings.Contains(result[0].Content, "3 steps remaining") {
		t.Errorf("expected '3 steps remaining' in warning, got: %s", result[0].Content)
	}
}

func TestMessageModifier_UrgencyWarning_Step8(t *testing.T) {
	systemPrompt := "You are a helpful assistant."
	urgencyWarning := "\n\nWARNING: Only %d steps remaining!"
	maxSteps := 10
	store := NewStepContentStore()
	modifier, _ := createTestMessageModifier(systemPrompt, urgencyWarning, maxSteps, store)

	input := []*schema.Message{
		{Role: schema.User, Content: "Hello"},
	}

	// Step 8 means remainingSteps = 10 - 8 = 2, warning SHOULD be added
	result := modifier(context.Background(), input, 8)

	if !strings.Contains(result[0].Content, "WARNING:") {
		t.Errorf("expected urgency warning at step 8, got: %s", result[0].Content)
	}
}

func TestMessageModifier_UrgencyWarning_Step5(t *testing.T) {
	systemPrompt := "You are a helpful assistant."
	urgencyWarning := "\n\nWARNING: Only %d steps remaining!"
	maxSteps := 10
	store := NewStepContentStore()
	modifier, _ := createTestMessageModifier(systemPrompt, urgencyWarning, maxSteps, store)

	input := []*schema.Message{
		{Role: schema.User, Content: "Hello"},
	}

	// Step 5 means remainingSteps = 10 - 5 = 5 > 3, so NO warning
	result := modifier(context.Background(), input, 5)

	if strings.Contains(result[0].Content, "WARNING:") {
		t.Errorf("expected NO urgency warning at step 5 (5 remaining), got: %s", result[0].Content)
	}
}

func TestMessageModifier_FindsLatestUserQuestion(t *testing.T) {
	systemPrompt := "You are a helpful assistant."
	store := NewStepContentStore()
	modifier, _ := createTestMessageModifier(systemPrompt, "", 10, store)

	input := []*schema.Message{
		{Role: schema.User, Content: "First question"},
		{Role: schema.Assistant, Content: "First answer"},
		{Role: schema.User, Content: "Second question"},
		{Role: schema.Assistant, Content: "Second answer"},
		{Role: schema.User, Content: "Third and LATEST question"},
	}

	// At step 2, task reminder should be added with LATEST user message
	result := modifier(context.Background(), input, 2)

	// Check system prompt contains the LATEST user question
	if !strings.Contains(result[0].Content, "Third and LATEST question") {
		t.Errorf("expected LATEST user question in task reminder, got: %s", result[0].Content)
	}
	if strings.Contains(result[0].Content, "First question") {
		t.Errorf("should NOT contain first question in task reminder, got: %s", result[0].Content)
	}
}

func TestMessageModifier_TaskReminder_Step0(t *testing.T) {
	systemPrompt := "You are a helpful assistant."
	store := NewStepContentStore()
	modifier, _ := createTestMessageModifier(systemPrompt, "", 10, store)

	input := []*schema.Message{
		{Role: schema.User, Content: "My question"},
	}

	// At step 0, NO task reminder should be added (stepCounter < 2)
	result := modifier(context.Background(), input, 0)

	if strings.Contains(result[0].Content, "CURRENT TASK") {
		t.Errorf("expected NO task reminder at step 0, got: %s", result[0].Content)
	}
}

func TestMessageModifier_TaskReminder_Step1(t *testing.T) {
	systemPrompt := "You are a helpful assistant."
	store := NewStepContentStore()
	modifier, _ := createTestMessageModifier(systemPrompt, "", 10, store)

	input := []*schema.Message{
		{Role: schema.User, Content: "My question"},
	}

	// At step 1, NO task reminder should be added (stepCounter < 2)
	result := modifier(context.Background(), input, 1)

	if strings.Contains(result[0].Content, "CURRENT TASK") {
		t.Errorf("expected NO task reminder at step 1, got: %s", result[0].Content)
	}
}

func TestMessageModifier_TaskReminder_Step2(t *testing.T) {
	systemPrompt := "You are a helpful assistant."
	store := NewStepContentStore()
	modifier, _ := createTestMessageModifier(systemPrompt, "", 10, store)

	input := []*schema.Message{
		{Role: schema.User, Content: "My question"},
	}

	// At step 2, task reminder SHOULD be added
	result := modifier(context.Background(), input, 2)

	if !strings.Contains(result[0].Content, "CURRENT TASK") {
		t.Errorf("expected task reminder at step 2, got: %s", result[0].Content)
	}
	if !strings.Contains(result[0].Content, "My question") {
		t.Errorf("expected user question in task reminder, got: %s", result[0].Content)
	}
}

func TestMessageModifier_ContentRecovery(t *testing.T) {
	systemPrompt := "You are a helpful assistant."
	store := NewStepContentStore()

	// Pre-populate store with content for step 0
	store.Append(0, "Recovered reasoning content")

	modifier, _ := createTestMessageModifier(systemPrompt, "", 10, store)

	input := []*schema.Message{
		{Role: schema.User, Content: "Question"},
		// Assistant message with empty content but has tool_calls
		{
			Role:    schema.Assistant,
			Content: "", // Empty content that should be recovered
			ToolCalls: []schema.ToolCall{
				{ID: "call-1", Function: schema.FunctionCall{Name: "search_code"}},
			},
		},
		{Role: schema.Tool, Content: "Tool result", Name: "search_code", ToolCallID: "call-1"},
	}

	result := modifier(context.Background(), input, 0)

	// Find the assistant message
	var assistantMsg *schema.Message
	for _, msg := range result {
		if msg.Role == schema.Assistant {
			assistantMsg = msg
			break
		}
	}

	if assistantMsg == nil {
		t.Fatal("assistant message not found in result")
	}

	// Content should be recovered from store
	if assistantMsg.Content != "Recovered reasoning content" {
		t.Errorf("content recovery: got %q, want %q", assistantMsg.Content, "Recovered reasoning content")
	}

	// Tool calls should still be present
	if len(assistantMsg.ToolCalls) != 1 {
		t.Errorf("tool calls: got %d, want 1", len(assistantMsg.ToolCalls))
	}
}

func TestMessageModifier_NoContentRecoveryWhenContentExists(t *testing.T) {
	systemPrompt := "You are a helpful assistant."
	store := NewStepContentStore()

	// Pre-populate store with content
	store.Append(0, "Store content")

	modifier, _ := createTestMessageModifier(systemPrompt, "", 10, store)

	input := []*schema.Message{
		{Role: schema.User, Content: "Question"},
		// Assistant message with existing content
		{
			Role:    schema.Assistant,
			Content: "Existing content", // Already has content, should NOT be replaced
			ToolCalls: []schema.ToolCall{
				{ID: "call-1", Function: schema.FunctionCall{Name: "search_code"}},
			},
		},
	}

	result := modifier(context.Background(), input, 0)

	// Find the assistant message
	var assistantMsg *schema.Message
	for _, msg := range result {
		if msg.Role == schema.Assistant {
			assistantMsg = msg
			break
		}
	}

	if assistantMsg == nil {
		t.Fatal("assistant message not found in result")
	}

	// Content should NOT be replaced
	if assistantMsg.Content != "Existing content" {
		t.Errorf("content should not be replaced: got %q, want %q", assistantMsg.Content, "Existing content")
	}
}

func TestMessageModifier_PreservesMessageOrder(t *testing.T) {
	systemPrompt := "System"
	store := NewStepContentStore()
	modifier, _ := createTestMessageModifier(systemPrompt, "", 10, store)

	input := []*schema.Message{
		{Role: schema.User, Content: "Q1"},
		{Role: schema.Assistant, Content: "A1"},
		{Role: schema.User, Content: "Q2"},
		{Role: schema.Assistant, Content: "A2"},
	}

	result := modifier(context.Background(), input, 0)

	// Expected order: System, User(Q1), Assistant(A1), User(Q2), Assistant(A2)
	if len(result) != 5 {
		t.Fatalf("result length: got %d, want 5", len(result))
	}

	expected := []struct {
		role    schema.RoleType
		content string
	}{
		{schema.System, "System"},
		{schema.User, "Q1"},
		{schema.Assistant, "A1"},
		{schema.User, "Q2"},
		{schema.Assistant, "A2"},
	}

	for i, exp := range expected {
		if result[i].Role != exp.role {
			t.Errorf("message %d role: got %v, want %v", i, result[i].Role, exp.role)
		}
		if result[i].Content != exp.content {
			t.Errorf("message %d content: got %q, want %q", i, result[i].Content, exp.content)
		}
	}
}

// TestMessageModifierWithAgentConfig tests using actual config structures
func TestMessageModifierWithAgentConfig(t *testing.T) {
	prompts := &config.PromptsConfig{
		SystemPrompt:   "Test system prompt",
		UrgencyWarning: "\n\nURGENT: %d steps left!",
	}

	agentConfig := &config.AgentConfig{
		MaxSteps:       10,
		MaxContextSize: 16000,
		Prompts:        prompts,
	}

	store := NewStepContentStore()
	modifier, _ := createTestMessageModifier(
		agentConfig.Prompts.SystemPrompt,
		agentConfig.Prompts.UrgencyWarning,
		agentConfig.MaxSteps,
		store,
	)

	input := []*schema.Message{
		{Role: schema.User, Content: "Test question"},
	}

	// Test at step 0 (no warning, no reminder)
	result := modifier(context.Background(), input, 0)
	if !strings.Contains(result[0].Content, "Test system prompt") {
		t.Error("expected system prompt in result")
	}
	if strings.Contains(result[0].Content, "URGENT:") {
		t.Error("should not have urgency warning at step 0")
	}

	// Test at step 8 (should have warning, should have task reminder)
	result = modifier(context.Background(), input, 8)
	if !strings.Contains(result[0].Content, "URGENT:") {
		t.Error("expected urgency warning at step 8")
	}
	if !strings.Contains(result[0].Content, "2 steps left") {
		t.Error("expected '2 steps left' in warning")
	}
	if !strings.Contains(result[0].Content, "CURRENT TASK") {
		t.Error("expected task reminder at step 8")
	}
}
