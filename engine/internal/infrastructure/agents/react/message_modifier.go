package react

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// finalizeDirective is injected before a model call once the turn is about to
// exhaust its time or step budget. It forces the model to stop calling tools and
// produce its final answer from what it already has — a graceful soft-landing
// inside the budget rather than running into the hard wall with no answer.
const finalizeDirective = "\n\n**BUDGET REACHED — FINAL ANSWER REQUIRED NOW.** You are out of time/steps for this turn. Do NOT call any more tools. Using only the information you have already gathered, write your best, complete final answer to the user right now. If you could not fully finish, briefly state what you found and what still remains so the user can follow up."

// softLandingTimeNumerator/Denominator express the fraction of max_turn_duration
// at which the finalize directive begins firing (9/10 = 90%).
const (
	softLandingTimeNumerator   = 9
	softLandingTimeDenominator = 10
)

// MessageModifier modifies messages before sending to the model
// It handles system prompt injection, urgency warnings, task reminders,
// and context reminders from providers
type MessageModifier struct {
	systemPrompt      string
	urgencyWarning    string
	maxSteps          int // configured per-turn step budget; 0 = unlimited
	maxTurnDuration   int // configured per-turn time budget in seconds; 0 = none
	contextLogger     ContextLoggerInterface
	reminderProviders []ContextReminderProvider
	sessionID         string
	toolNames         []string // Available tool names for dynamic injection
	stepCounter       int
	turnStart         time.Time // stamped by StartTurn; drives time soft-landing
	mu                sync.Mutex
}

// MessageModifierConfig holds configuration for MessageModifier
type MessageModifierConfig struct {
	SystemPrompt      string
	UrgencyWarning    string
	MaxSteps          int // configured per-turn step budget; 0 = unlimited
	MaxTurnDuration   int // configured per-turn time budget in seconds; 0 = none
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
		maxTurnDuration:   cfg.MaxTurnDuration,
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
	turnStart := m.turnStart
	m.mu.Unlock()

	// The head system message holds ONLY content that is stable across the turn's
	// model calls (system prompt + tool list + HITL directive). Per-step dynamic
	// directives (urgency, finalize, task focus) are emitted as a trailing system
	// message below instead of being concatenated here, so the head stays
	// byte-identical call-to-call and the provider can prompt-cache it. The same
	// text reaches the model — positioned last for recency, not in the head.
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

	// Per-step dynamic directives (urgency / finalize / task focus) are built
	// separately and emitted as a trailing system message below — kept OUT of the
	// head so the cacheable prefix stays stable across model calls.
	directive := m.buildDynamicDirective(currentStep, turnStart, input)

	// Add system prompt at the beginning, then the conversation. The owned graph
	// preserves assistant content alongside tool_calls in state, so no content
	// recovery is needed here.
	result := make([]*schema.Message, 0, len(input)+2)
	result = append(result, schema.SystemMessage(currentSystemPrompt))
	result = append(result, input...)
	result = m.appendContextReminders(ctx, result)

	// Trailing dynamic directive (urgency / finalize / task focus) — appended last
	// for recency, kept off the cacheable head. Same text the model would have seen
	// concatenated into the head before.
	if directive != "" {
		result = append(result, &schema.Message{
			Role:    schema.System,
			Content: directive,
		})
	}

	// Context logging moved to ContextRewriter to log post-compression state
	// (what LLM actually receives, not pre-compression snapshot)

	// Always increment step counter so urgency warnings and task reminders work
	m.mu.Lock()
	m.stepCounter++
	m.mu.Unlock()

	return result
}

// buildDynamicDirective assembles the per-step directives that must NOT live in the
// cacheable head system message: urgency warning, budget soft-landing, and task
// focus. Returns the combined text (trimmed) for a single trailing system message,
// or "" when none apply.
func (m *MessageModifier) buildDynamicDirective(currentStep int, turnStart time.Time, input []*schema.Message) string {
	var b strings.Builder

	// Urgency warning if approaching max steps (only when a step limit is set).
	if m.maxSteps > 0 {
		remainingSteps := m.maxSteps - currentStep
		if remainingSteps <= 3 && remainingSteps > 0 && m.urgencyWarning != "" {
			warning := fmt.Sprintf(m.urgencyWarning, remainingSteps)
			b.WriteString(warning)
		}
	}

	// Soft-landing: once the turn is about to exhaust its time or step budget, force
	// the model to finalize NOW (inside the budget) instead of running into the hard
	// wall with no answer. Best-effort — the agent loop's terminal fallback still
	// guarantees a graceful answer if the model ignores this.
	if m.shouldFinalize(currentStep, turnStart) {
		b.WriteString(finalizeDirective)
	}

	// Task reminder after several steps, using the LATEST user message to keep focus
	// on the current question.
	if currentStep >= 2 {
		if userQuestion := latestUserQuestion(input); userQuestion != "" {
			fmt.Fprintf(&b, "\n\n**CURRENT TASK (Step %d):** Answer the user's question: \"%s\"\nDo NOT get distracted - answer THIS question!", currentStep, sanitizeForSystemPrompt(userQuestion, 500))
		}
	}

	return strings.TrimSpace(b.String())
}

// appendContextReminders appends each provider's reminder as a trailing system
// message, sorted so higher-priority reminders land last (recency bias). Returns
// the extended slice. No-op when no providers or no session id.
func (m *MessageModifier) appendContextReminders(ctx context.Context, result []*schema.Message) []*schema.Message {
	if len(m.reminderProviders) == 0 || m.sessionID == "" {
		return result
	}
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
	sort.Slice(reminders, func(i, j int) bool {
		return reminders[i].priority < reminders[j].priority
	})
	for _, r := range reminders {
		result = append(result, &schema.Message{Role: schema.System, Content: r.content})
		slog.DebugContext(ctx, "injected context reminder",
			"priority", r.priority, "content_length", len(r.content))
	}
	return result
}

// latestUserQuestion returns the content of the last non-empty user message.
func latestUserQuestion(input []*schema.Message) string {
	var q string
	for _, msg := range input {
		if msg.Role == schema.User && msg.Content != "" {
			q = msg.Content
		}
	}
	return q
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

// StartTurn stamps the turn start time (for time soft-landing) and resets the
// per-turn model-call counter. Called by the agent at the start of each turn.
func (m *MessageModifier) StartTurn() {
	m.mu.Lock()
	m.turnStart = time.Now()
	m.stepCounter = 0
	m.mu.Unlock()
}

// shouldFinalize reports whether the turn is close enough to its time or step
// budget that the model should be forced to produce its final answer now.
//
// Step soft-landing: currentStep counts model calls (one per Modify). Eino spends
// up to 2 graph steps per model call (the model node + the tools node), so the
// max_steps wall can be reached after as few as maxSteps/2 model calls; firing one
// model call before that bound reserves a final answer-only turn inside the budget.
func (m *MessageModifier) shouldFinalize(currentStep int, turnStart time.Time) bool {
	if m.maxTurnDuration > 0 && !turnStart.IsZero() {
		softDeadline := time.Duration(m.maxTurnDuration) * time.Second * softLandingTimeNumerator / softLandingTimeDenominator
		if time.Since(turnStart) >= softDeadline {
			return true
		}
	}
	if m.maxSteps > 0 {
		softModelCallBudget := m.maxSteps / 2
		if softModelCallBudget < 1 {
			softModelCallBudget = 1
		}
		if currentStep >= softModelCallBudget-1 {
			return true
		}
	}
	return false
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
