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

	// Trailing nudge accumulation. Per-turn, the trailing system block (reminders +
	// dynamic directives) is built APPEND-ONLY with FREEZE-BY-SOURCE: each source (a
	// reminder provider, identified by its index, or a named directive) contributes its
	// FIRST non-empty text and is then frozen — later changed values from that same
	// source are ignored. This snapshots env-time / live task-state at first appearance
	// so the trailing block grows only by appending genuinely new sources' first values
	// and never rewrites already-emitted bytes. Explicit-cache providers (Qwen/DashScope,
	// Anthropic) keep the prefix cached — any change to already-sent content discards the
	// whole cache. Reset by StartTurn.
	emittedNudges []string
	frozenByKey   map[string]string

	mu sync.Mutex
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
		frozenByKey:       make(map[string]string),
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

	// Add system prompt at the beginning, then the conversation. The owned graph
	// preserves assistant content alongside tool_calls in state, so no content
	// recovery is needed here.
	result := make([]*schema.Message, 0, len(input)+4)
	result = append(result, schema.SystemMessage(currentSystemPrompt))
	result = append(result, input...)

	// Trailing nudge block: accumulated append-only across the turn so the bytes the
	// provider has already cached never change (only new nudges appended at the end).
	for _, nudge := range m.accumulateNudges(ctx, currentStep, turnStart, input) {
		result = append(result, &schema.Message{Role: schema.System, Content: nudge})
	}

	// Context logging moved to ContextRewriter to log post-compression state
	// (what LLM actually receives, not pre-compression snapshot)

	// Always increment step counter so urgency warnings and task reminders work
	m.mu.Lock()
	m.stepCounter++
	m.mu.Unlock()

	return result
}

// nudgeCandidate is a step's candidate trailing nudge tagged with a STABLE per-turn
// source key (a reminder provider's index, or a named directive). The key — not the
// content — decides freezing: a source contributes once, at its first non-empty value.
type nudgeCandidate struct {
	key     string
	content string
}

// accumulateNudges collects this step's candidate nudges (reminder-provider content +
// the dynamic directives), freezes each source at its FIRST non-empty value, appends
// newly-frozen values to the per-turn emittedNudges list in first-seen order, and
// returns the full accumulated list.
//
// Cache-stability invariant (freeze-by-source): once a source has contributed, its
// later — possibly changed — values are ignored, so already-emitted bytes are never
// rewritten. The trailing block grows only by appending a not-yet-seen source's first
// value, staying a byte-stable growing prefix call-to-call. Candidates are ordered
// low→high priority within a step (directives last) so that when several sources first
// appear together they land in recency order.
func (m *MessageModifier) accumulateNudges(ctx context.Context, currentStep int, turnStart time.Time, input []*schema.Message) []string {
	candidates := m.collectReminders(ctx)
	candidates = append(candidates, m.buildDynamicDirectives(currentStep, turnStart, input)...)

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range candidates {
		if c.content == "" {
			continue
		}
		if _, frozen := m.frozenByKey[c.key]; frozen {
			continue
		}
		m.frozenByKey[c.key] = c.content
		m.emittedNudges = append(m.emittedNudges, c.content)
	}
	// Return a copy so callers can't mutate the per-turn accumulator.
	return append([]string(nil), m.emittedNudges...)
}

// collectReminders returns each provider's current reminder content keyed by the
// provider's index in m.reminderProviders (stable per agent/turn → "r:0", "r:1", …),
// sorted low→high priority so higher-priority reminders land later (recency bias) on
// first appearance. The provider index, not its content, is the freeze key, so a
// volatile provider freezes at its first value regardless of how the text later changes.
func (m *MessageModifier) collectReminders(ctx context.Context) []nudgeCandidate {
	if len(m.reminderProviders) == 0 || m.sessionID == "" {
		return nil
	}
	type reminder struct {
		key      string
		content  string
		priority int
	}
	var reminders []reminder
	for i, provider := range m.reminderProviders {
		if content, priority, ok := provider.GetContextReminder(ctx, m.sessionID); ok && content != "" {
			reminders = append(reminders, reminder{key: fmt.Sprintf("r:%d", i), content: content, priority: priority})
		}
	}
	sort.SliceStable(reminders, func(i, j int) bool {
		return reminders[i].priority < reminders[j].priority
	})
	out := make([]nudgeCandidate, 0, len(reminders))
	for _, r := range reminders {
		out = append(out, nudgeCandidate{key: r.key, content: r.content})
		slog.DebugContext(ctx, "collected context reminder",
			"priority", r.priority, "content_length", len(r.content))
	}
	return out
}

// buildDynamicDirectives returns the per-step directives that must NOT live in the
// cacheable head: urgency warning, budget soft-landing, and task focus — each keyed by
// a stable per-turn source name ("d:urgency", "d:finalize", "d:task-focus") so it
// freezes to a single append-once item regardless of how its text might later change.
// Each directive's text is COUNT-FREE so it stays byte-identical every step it persists.
func (m *MessageModifier) buildDynamicDirectives(currentStep int, turnStart time.Time, input []*schema.Message) []nudgeCandidate {
	var directives []nudgeCandidate

	// Urgency warning when approaching the step budget. The configured text is emitted
	// verbatim (count-free) — embedding the remaining-step number would change it every
	// step and discard the provider cache.
	if m.maxSteps > 0 && m.urgencyWarning != "" {
		remainingSteps := m.maxSteps - currentStep
		if remainingSteps <= 3 && remainingSteps > 0 {
			directives = append(directives, nudgeCandidate{key: "d:urgency", content: strings.TrimSpace(m.urgencyWarning)})
		}
	}

	// Soft-landing: once the turn is about to exhaust its time or step budget, force the
	// model to finalize NOW (inside the budget) instead of hitting the hard wall with no
	// answer. Best-effort — the loop's terminal fallback still guarantees a graceful end.
	if m.shouldFinalize(currentStep, turnStart) {
		directives = append(directives, nudgeCandidate{key: "d:finalize", content: strings.TrimSpace(finalizeDirective)})
	}

	// Task focus after several steps, using the LATEST user message. Count-free (no step
	// number) and stable within the turn (same sanitized question) so it appends once.
	if currentStep >= 2 {
		if userQuestion := latestUserQuestion(input); userQuestion != "" {
			directives = append(directives, nudgeCandidate{
				key:     "d:task-focus",
				content: fmt.Sprintf("**CURRENT TASK:** Answer the user's question: \"%s\"\nDo NOT get distracted - answer THIS question!", sanitizeForSystemPrompt(userQuestion, 500)),
			})
		}
	}

	return directives
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
// per-turn model-call counter and trailing-nudge accumulation. Called by the agent
// at the start of each turn.
func (m *MessageModifier) StartTurn() {
	m.mu.Lock()
	m.turnStart = time.Now()
	m.stepCounter = 0
	m.emittedNudges = nil
	m.frozenByKey = make(map[string]string)
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
