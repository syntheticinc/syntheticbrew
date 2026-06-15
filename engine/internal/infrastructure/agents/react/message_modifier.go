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

// injectedReminder is one reminder system message captured at the input position it
// was first appended after. afterIndex pins it BETWEEN input messages so later steps,
// which only grow input at the tail, never shift it — the explicit-cache prefix keeps
// growing instead of collapsing. afterIndex == -1 means "input was empty when added"
// → emit right after the head, before input[0].
type injectedReminder struct {
	afterIndex int
	content    string
}

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

	// Append-increment reminder injection. Per-turn, each candidate source (a reminder
	// provider keyed "r:<index>", or a named directive "d:urgency"/"d:finalize"/
	// "d:task-focus") is tracked by its LAST emitted value. When a source's value
	// CHANGES (or first appears) a NEW reminder is appended at the current input tail
	// and never rewritten or removed; an unchanged value is skipped. Reminders are then
	// interleaved back into the message stream at their captured input position, so every
	// prior request stays a clean prefix of the next — old content keeps its position,
	// only new content lands at the end. This keeps LIVE values (e.g. a step countdown,
	// which appends a fresh reminder each step) AND lets explicit-cache providers
	// (Qwen/DashScope, Anthropic) grow the cached prefix. Reset by StartTurn.
	injected       []injectedReminder
	lastValueByKey map[string]string

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
		lastValueByKey:    make(map[string]string),
	}
}

// reminderCandidate is a step's candidate reminder tagged with a STABLE per-turn source
// key (a reminder provider's index, or a named directive). The key identifies the
// source across steps; a NEW reminder is appended only when this key's content differs
// from the last value emitted for it.
type reminderCandidate struct {
	key     string
	content string
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
	// directives (urgency, finalize, task focus) and provider reminders are emitted as
	// interleaved system messages below instead of being concatenated here, so the head
	// stays byte-identical call-to-call and the provider can prompt-cache it.
	head := schema.SystemMessage(m.buildHead())

	// Gather this step's candidates (providers called OUTSIDE the lock), then append any
	// genuinely-new content under the lock and interleave the accumulated reminders back
	// into the stream at their captured input positions.
	candidates := m.gatherCandidates(ctx, currentStep, turnStart, input)

	m.mu.Lock()
	for _, c := range candidates {
		if c.content == "" {
			continue
		}
		if m.lastValueByKey[c.key] == c.content {
			continue // unchanged value → do not duplicate
		}
		m.lastValueByKey[c.key] = c.content
		m.injected = append(m.injected, injectedReminder{afterIndex: len(input) - 1, content: c.content})
	}
	injected := append([]injectedReminder(nil), m.injected...)
	m.stepCounter++
	m.mu.Unlock()

	// Context logging moved to ContextRewriter to log post-compression state
	// (what LLM actually receives, not pre-compression snapshot)

	return interleave(head, input, injected)
}

// interleave rebuilds the message stream as [head] + input with the accumulated
// reminders spliced back in at their captured input positions. A reminder is emitted
// immediately AFTER the input message at its afterIndex (in insertion order when several
// share an index); afterIndex == -1 reminders (input was empty when added) land right
// after the head, before input[0]. Reminders added this step carry afterIndex ==
// len(input)-1 and therefore land after the last input message — at the very end for
// recency. Because input only grows at the tail, every prior reminder keeps the exact
// position it had, so call N's sequence stays a prefix of call N+1's.
func interleave(head *schema.Message, input []*schema.Message, injected []injectedReminder) []*schema.Message {
	result := make([]*schema.Message, 0, len(input)+len(injected)+1)
	result = append(result, head)

	emitAt := func(idx int) {
		for _, r := range injected {
			if r.afterIndex == idx {
				result = append(result, &schema.Message{Role: schema.System, Content: r.content})
			}
		}
	}

	emitAt(-1) // reminders captured when input was empty
	for i := range input {
		result = append(result, input[i])
		emitAt(i)
	}
	return result
}

// gatherCandidates returns this step's candidate reminders in low→high priority order
// (reminder-provider content first, then dynamic directives), so when several sources
// first appear together they land in recency order. Providers are invoked here, outside
// the modifier lock.
func (m *MessageModifier) gatherCandidates(ctx context.Context, currentStep int, turnStart time.Time, input []*schema.Message) []reminderCandidate {
	candidates := m.collectReminders(ctx)
	candidates = append(candidates, m.buildDynamicDirectives(currentStep, turnStart, input)...)
	return candidates
}

// buildHead returns the stable head system prompt: configured system prompt + the
// available-tools whitelist + the HITL halt-point directive. It carries no per-step
// dynamic content so it stays byte-identical across the turn's model calls and the
// provider can prompt-cache it.
func (m *MessageModifier) buildHead() string {
	head := m.systemPrompt

	// Inject available tool names so the model can't invent non-existent tools.
	if len(m.toolNames) > 0 {
		head += fmt.Sprintf("\n\n**Available tools:** %s\nOnly use these tools. Do not invent or call tools that are not listed.",
			strings.Join(m.toolNames, ", "))
	}

	// Inject the HITL halt-point directive when any HITL tool is available.
	// Constrains models that emit prose alongside the HITL tool_call.
	if domain.HasAnyHITLTool(m.toolNames) {
		head += hitlPromptDirective
	}

	return head
}

// collectReminders returns each provider's current reminder content keyed by the
// provider's index in m.reminderProviders (stable per agent/turn → "r:0", "r:1", …),
// sorted low→high priority so higher-priority reminders land later (recency bias).
// A new reminder is appended for a provider only when its content changes from the
// value last emitted for that key.
func (m *MessageModifier) collectReminders(ctx context.Context) []reminderCandidate {
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
	out := make([]reminderCandidate, 0, len(reminders))
	for _, r := range reminders {
		out = append(out, reminderCandidate{key: r.key, content: r.content})
		slog.DebugContext(ctx, "collected context reminder",
			"priority", r.priority, "content_length", len(r.content))
	}
	return out
}

// buildDynamicDirectives returns the per-step directives that must NOT live in the
// cacheable head: urgency warning, budget soft-landing, and task focus — each keyed by
// a stable per-turn source name ("d:urgency", "d:finalize", "d:task-focus"). The urgency
// directive embeds the LIVE remaining-step count, so its value changes each step and
// append-increment appends a fresh reminder per step (a live countdown). Finalize and
// task-focus are static, so they append once.
func (m *MessageModifier) buildDynamicDirectives(currentStep int, turnStart time.Time, input []*schema.Message) []reminderCandidate {
	var directives []reminderCandidate

	// Urgency warning when approaching the step budget. The configured text carries the
	// live remaining-step count when it contains a %d verb, so it changes each step and
	// append-increment trails a fresh countdown reminder.
	if m.maxSteps > 0 && m.urgencyWarning != "" {
		remainingSteps := m.maxSteps - currentStep
		if remainingSteps <= 3 && remainingSteps > 0 {
			warning := strings.TrimSpace(m.urgencyWarning)
			if strings.Contains(warning, "%d") {
				warning = fmt.Sprintf(warning, remainingSteps)
			}
			directives = append(directives, reminderCandidate{key: "d:urgency", content: warning})
		}
	}

	// Soft-landing: once the turn is about to exhaust its time or step budget, force the
	// model to finalize NOW (inside the budget) instead of hitting the hard wall with no
	// answer. Best-effort — the loop's terminal fallback still guarantees a graceful end.
	if m.shouldFinalize(currentStep, turnStart) {
		directives = append(directives, reminderCandidate{key: "d:finalize", content: strings.TrimSpace(finalizeDirective)})
	}

	// Task focus after several steps, using the LATEST user message. Static within the
	// turn (same sanitized question, no step number) so it appends once.
	if currentStep >= 2 {
		if userQuestion := latestUserQuestion(input); userQuestion != "" {
			directives = append(directives, reminderCandidate{
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
// per-turn model-call counter and reminder-injection accumulation. Called by the agent
// at the start of each turn.
func (m *MessageModifier) StartTurn() {
	m.mu.Lock()
	m.turnStart = time.Now()
	m.stepCounter = 0
	m.injected = nil
	m.lastValueByKey = make(map[string]string)
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
