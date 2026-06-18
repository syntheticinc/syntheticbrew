package react

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/cloudwego/eino/schema"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// MessageModifier builds the turn's FROZEN HEAD: a single leading system message
// carrying everything stable for the turn — the system prompt, the available-tools
// whitelist, the HITL halt directive, the task-focus restatement, and every context
// reminder's content captured ONCE on the turn's first model call. The head is byte-
// frozen for the whole turn, so the explicit-cache prefix never shifts; the owned
// loop appends only natural user/assistant/tool turns after it, making each step's
// request a strict append-only extension of the previous one.
//
// What the modifier deliberately does NOT do: it never injects a standalone
// schema.System message into the MIDDLE of the conversation, and it never rewrites
// or re-derives already-formed messages. A system message materialising mid-stream
// makes Qwen/DashScope re-render its chat template and discard the whole explicit-
// cache prefix for that step — the root cause of the mid-cycle cache collapses.
// Dynamic mid-turn feedback (loop correction, budget soft-landing) is therefore
// folded into the tool-result the owned loop already appends (see owned_loop.go),
// not surfaced here. Compaction (ContextRewriter) is the only component allowed to
// re-assemble formed context.
type MessageModifier struct {
	systemPrompt      string
	reminderProviders []ContextReminderProvider
	sessionID         string
	toolNames         []string // available tool names injected into the head

	// frozenHead is the leading system message text, built once on the turn's first
	// Modify and reused byte-identical for every later step. Reset by StartTurn.
	frozenHead string
	headBuilt  bool

	mu sync.Mutex
}

// MessageModifierConfig holds configuration for MessageModifier.
type MessageModifierConfig struct {
	SystemPrompt      string
	ReminderProviders []ContextReminderProvider
	SessionID         string
	ToolNames         []string // available tool names to inject into the head
}

// NewMessageModifier creates a new MessageModifier.
func NewMessageModifier(cfg MessageModifierConfig) *MessageModifier {
	return &MessageModifier{
		systemPrompt:      cfg.SystemPrompt,
		reminderProviders: cfg.ReminderProviders,
		sessionID:         cfg.SessionID,
		toolNames:         cfg.ToolNames,
	}
}

// Modify prepends the frozen head to the (append-only) input transcript. The head
// is built once per turn and never changes, so [head]+input stays a strict append-
// only extension across the turn's steps and the explicit-cache prefix keeps growing
// instead of collapsing.
func (m *MessageModifier) Modify(ctx context.Context, input []*schema.Message) []*schema.Message {
	m.mu.Lock()
	if !m.headBuilt {
		m.frozenHead = m.buildHead(ctx, input)
		m.headBuilt = true
	}
	head := m.frozenHead
	m.mu.Unlock()

	result := make([]*schema.Message, 0, len(input)+1)
	result = append(result, schema.SystemMessage(head))
	result = append(result, input...)
	return result
}

// buildHead assembles the frozen head: configured system prompt + the available-
// tools whitelist + the HITL halt directive + the task-focus restatement + every
// context reminder's content, captured once and ordered by priority (higher
// priority later, for recency within the head). It carries no per-step dynamic
// content, so it stays byte-identical across the turn's model calls and the provider
// can prompt-cache it.
func (m *MessageModifier) buildHead(ctx context.Context, input []*schema.Message) string {
	var sb strings.Builder
	sb.WriteString(m.systemPrompt)

	// Inject available tool names so the model can't invent non-existent tools.
	if len(m.toolNames) > 0 {
		fmt.Fprintf(&sb, "\n\n**Available tools:** %s\nOnly use these tools. Do not invent or call tools that are not listed.",
			strings.Join(m.toolNames, ", "))
	}

	// HITL halt-point directive when any HITL tool is available.
	if domain.HasAnyHITLTool(m.toolNames) {
		sb.WriteString(hitlPromptDirective)
	}

	// Task focus: restate the user's question so a long tool loop stays anchored to
	// THIS turn's request. Stable for the turn (frozen at the first model call).
	if q := latestUserQuestion(input); q != "" {
		fmt.Fprintf(&sb, "\n\n**CURRENT TASK:** Answer the user's question: %q\nDo NOT get distracted - answer THIS question!",
			sanitizeForSystemPrompt(q, 500))
	}

	// Fold every context reminder's content into the head, captured ONCE for the turn.
	for _, content := range m.collectReminders(ctx) {
		sb.WriteString("\n\n")
		sb.WriteString(content)
	}

	return sb.String()
}

// collectReminders returns each provider's current reminder content, sorted
// low→high priority so higher-priority reminders land later in the head (recency
// bias). An empty reminder is skipped.
//
// Captured ONCE per turn (the head is frozen) by design: re-polling a provider
// whose value changes mid-turn — task state evolving, a loop warning appearing —
// would mutate the cached head and collapse the explicit-cache prefix, the exact
// bug this refactor removes. Providers that are already stable (security, env,
// testing, capability) freeze losslessly. The two that can evolve mid-turn are
// covered by append-only channels instead: a developing loop is caught by the
// owned loop's own breaker and its nudge folded into the offending tool result
// (owned_loop_breakers.go), and task state the agent itself changes mid-turn is
// surfaced by the tool results of the manage_tasks/spawn_agent calls that changed
// it — never by re-writing the head.
func (m *MessageModifier) collectReminders(ctx context.Context) []string {
	if len(m.reminderProviders) == 0 || m.sessionID == "" {
		return nil
	}
	type reminder struct {
		content  string
		priority int
	}
	var reminders []reminder
	for _, provider := range m.reminderProviders {
		if content, priority, ok := provider.GetContextReminder(ctx, m.sessionID); ok && content != "" {
			reminders = append(reminders, reminder{content: content, priority: priority})
		}
	}
	sort.SliceStable(reminders, func(i, j int) bool {
		return reminders[i].priority < reminders[j].priority
	})
	out := make([]string, 0, len(reminders))
	for _, r := range reminders {
		out = append(out, r.content)
		slog.DebugContext(ctx, "folded context reminder into frozen head",
			"priority", r.priority, "content_length", len(r.content))
	}
	return out
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

// StartTurn discards the previous turn's frozen head so the next turn rebuilds it
// (capturing that turn's reminders and user question). Called by the agent at the
// start of each turn.
func (m *MessageModifier) StartTurn() {
	m.mu.Lock()
	m.frozenHead = ""
	m.headBuilt = false
	m.mu.Unlock()
}

// BuildModifierFunc returns a function suitable for use as AgentConfig.MessageModifier.
func (m *MessageModifier) BuildModifierFunc() func(ctx context.Context, input []*schema.Message) []*schema.Message {
	return m.Modify
}

// sanitizeForSystemPrompt cleans user input before injecting into the system prompt.
// Truncates to maxLen runes and replaces newlines to prevent format injection.
func sanitizeForSystemPrompt(input string, maxLen int) string {
	result := strings.NewReplacer("\n", " ", "\r", " ").Replace(input)
	runes := []rune(result)
	if len(runes) > maxLen {
		result = string(runes[:maxLen]) + "..."
	}
	return result
}
