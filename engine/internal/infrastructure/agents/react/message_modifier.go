package react

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/cloudwego/eino/schema"
	"github.com/syntheticinc/bytebrew/engine/internal/domain"
)

// MessageModifier modifies messages before sending to the model
// It handles system prompt injection, urgency warnings, task reminders,
// content recovery for streaming mode, and context reminders from providers
type MessageModifier struct {
	systemPrompt      string
	urgencyWarning    string
	maxSteps          int
	stepContentStore  StepContentStoreInterface
	contextLogger     ContextLoggerInterface
	reminderProviders []ContextReminderProvider
	sessionID         string
	toolNames         []string // Available tool names for dynamic injection
	stepCounter       int
	mu                sync.Mutex
}

// MessageModifierConfig holds configuration for MessageModifier
type MessageModifierConfig struct {
	SystemPrompt      string
	UrgencyWarning    string
	MaxSteps          int
	StepContentStore  StepContentStoreInterface
	ContextLogger     ContextLoggerInterface
	ReminderProviders []ContextReminderProvider
	SessionID         string
	ToolNames         []string // Available tool names to inject into system prompt
}

// NewMessageModifier creates a new MessageModifier
func NewMessageModifier(cfg MessageModifierConfig) *MessageModifier {
	return &MessageModifier{
		systemPrompt:      cfg.SystemPrompt,
		urgencyWarning:    cfg.UrgencyWarning,
		maxSteps:          cfg.MaxSteps,
		stepContentStore:  cfg.StepContentStore,
		contextLogger:     cfg.ContextLogger,
		reminderProviders: cfg.ReminderProviders,
		sessionID:         cfg.SessionID,
		toolNames:         cfg.ToolNames,
		stepCounter:       0,
	}
}

// Modify modifies the input messages according to the current step and configuration
// Returns modified messages with system prompt, urgency warnings, and task reminders
func (m *MessageModifier) Modify(ctx context.Context, input []*schema.Message) []*schema.Message {
	m.mu.Lock()
	currentStep := m.stepCounter
	m.mu.Unlock()

	// Build system prompt with urgency warning if approaching max steps
	// Only apply urgency warning if maxSteps > 0 (when limit is set)
	currentSystemPrompt := m.systemPrompt

	// Inject available tool names into system prompt
	// This prevents LLM from inventing non-existent tools
	if len(m.toolNames) > 0 {
		toolsNote := fmt.Sprintf("\n\n**Available tools:** %s\nOnly use these tools. Do not invent or call tools that are not listed.",
			strings.Join(m.toolNames, ", "))
		currentSystemPrompt += toolsNote
	}

	// Inject the HITL halt-point directive when any HITL tool is available.
	// Constrains models that emit prose alongside the HITL tool_call.
	if domain.HasAnyHITLTool(m.toolNames) {
		currentSystemPrompt += hitlPromptDirective
	}

	if m.maxSteps > 0 {
		remainingSteps := m.maxSteps - currentStep
		if remainingSteps <= 3 && remainingSteps > 0 && m.urgencyWarning != "" {
			urgencyMsg := fmt.Sprintf(m.urgencyWarning, remainingSteps)
			currentSystemPrompt = currentSystemPrompt + urgencyMsg
		}
	}

	// Find and extract LATEST user question for task reminder
	// In a conversation, the most recent user message is what needs to be answered
	var userQuestion string
	for _, msg := range input {
		if msg.Role == schema.User && msg.Content != "" {
			userQuestion = msg.Content
			// Don't break - continue to find the LAST user message
		}
	}

	// Add task reminder to system prompt after several steps
	// Uses the LATEST user message to keep focus on current question
	if currentStep >= 2 && userQuestion != "" {
		currentSystemPrompt += fmt.Sprintf("\n\n**CURRENT TASK (Step %d):** Answer the user's question: \"%s\"\nDo NOT get distracted - answer THIS question!", currentStep, sanitizeForSystemPrompt(userQuestion, 500))
	}

	// Add system prompt at the beginning
	result := make([]*schema.Message, 0, len(input)+1)
	result = append(result, schema.SystemMessage(currentSystemPrompt))

	// CRITICAL FIX: Inject accumulated content into empty assistant messages
	// Eino's ReAct agent doesn't preserve content when there are tool_calls in streaming mode
	// We recover the content from our shared store
	var stepContent map[int]string
	if m.stepContentStore != nil {
		stepContent = m.stepContentStore.GetAll()
	}

	// Track which step each assistant message corresponds to
	assistantStepIdx := 0
	for _, msg := range input {
		if msg.Role == schema.Assistant {
			// If assistant message has tool_calls but empty content, try to fill it
			if msg.Content == "" && len(msg.ToolCalls) > 0 && stepContent != nil {
				if content, ok := stepContent[assistantStepIdx]; ok && content != "" {
					// Create a copy with filled content
					filledMsg := &schema.Message{
						Role:      msg.Role,
						Content:   content,
						ToolCalls: msg.ToolCalls,
						Name:      msg.Name,
					}
					result = append(result, filledMsg)
					slog.DebugContext(ctx, "filled empty assistant message with accumulated content",
						"step", assistantStepIdx, "content_length", len(content))
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

	// Collect and inject context reminders from all providers
	// This allows tools/components to add their own reminders without coupling
	if len(m.reminderProviders) > 0 && m.sessionID != "" {
		type reminder struct {
			content  string
			priority int
		}
		var reminders []reminder

		for _, provider := range m.reminderProviders {
			if content, priority, ok := provider.GetContextReminder(ctx, m.sessionID); ok {
				reminders = append(reminders, reminder{content: content, priority: priority})
			}
		}

		// Sort by priority (lower first, so higher priority ends up at the end)
		sort.Slice(reminders, func(i, j int) bool {
			return reminders[i].priority < reminders[j].priority
		})

		// Inject reminders as system messages at end of context
		// This exploits recency bias - LLM sees these last
		for _, r := range reminders {
			result = append(result, &schema.Message{
				Role:    schema.System,
				Content: r.content,
			})
			slog.DebugContext(ctx, "injected context reminder",
				"priority", r.priority,
				"content_length", len(r.content))
		}
	}

	// Context logging moved to ContextRewriter to log post-compression state
	// (what LLM actually receives, not pre-compression snapshot)

	// Always increment step counter so urgency warnings and task reminders work
	m.mu.Lock()
	m.stepCounter++
	m.mu.Unlock()

	return result
}

// GetStep returns the current step counter (thread-safe)
func (m *MessageModifier) GetStep() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stepCounter
}

// ResetStep resets the step counter to 0
func (m *MessageModifier) ResetStep() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stepCounter = 0
}

// BuildModifierFunc returns a function suitable for use as AgentConfig.MessageModifier
func (m *MessageModifier) BuildModifierFunc() func(ctx context.Context, input []*schema.Message) []*schema.Message {
	return m.Modify
}

// sanitizeForSystemPrompt cleans user input before injecting into system prompt.
// Truncates to maxLen runes and replaces newlines to prevent format injection.
func sanitizeForSystemPrompt(input string, maxLen int) string {
	// Replace newlines with spaces to prevent format injection
	result := strings.NewReplacer("\n", " ", "\r", " ").Replace(input)

	// Truncate to maxLen runes
	runes := []rune(result)
	if len(runes) > maxLen {
		result = string(runes[:maxLen]) + "..."
	}

	return result
}
