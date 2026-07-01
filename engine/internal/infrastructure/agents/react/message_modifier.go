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

// MessageModifier builds the turn's FROZEN HEAD as TWO leading system messages,
// both captured ONCE on the turn's first model call and byte-frozen for the whole turn:
//   - a STABLE head (system prompt + available-tools whitelist + HITL halt directive)
//     that is turn-invariant, so the cache_control breakpoint marking the first system
//     message is byte-identical across turns and the provider caches it cross-turn;
//   - a VOLATILE head (task-focus restatement + every context reminder) that is frozen
//     within the turn but differs between turns, kept SEPARATE from the cache-marked
//     stable head so per-turn changes don't re-bill the whole prompt every turn.
//
// Both are frozen for the turn, so the explicit-cache prefix never shifts mid-turn; the
// owned loop appends only natural user/assistant/tool turns after them, making each
// step's request a strict append-only extension of the previous one.
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

	// The frozen head is two leading system messages, both captured once on the turn's
	// first Modify and reused byte-identical for every later step (reset by StartTurn):
	//   - frozenStableHead: system prompt + tool whitelist + HITL directive. Turn-
	//     INVARIANT, so the cache_control breakpoint (which marks the first system
	//     message) stays byte-identical across turns and the provider caches it cross-turn.
	//   - frozenVolatileHead: CURRENT TASK + reminders. Frozen within the turn (no mid-
	//     turn collapse) but differs between turns, so it is kept OUT of the cache-marked
	//     stable head — folding it in re-bills the whole prompt on every turn.
	frozenStableHead   string
	frozenVolatileHead string
	headBuilt          bool

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

// Modify wraps the (append-only) input transcript with the turn's two frozen heads: the
// turn-INVARIANT stable head at the FRONT (index 0, the cache_control breakpoint) and the
// per-turn VOLATILE head at the TAIL (after the whole conversation). Both are built once
// per turn and never changed within it.
//
// The volatile head (CURRENT TASK + reminders) changes every turn because the question
// changes. Placing it at the front — between the stable head and the conversation — caps
// cross-turn caching at just the system prompt: the append-only history sits AFTER the
// changing block, so the provider's common prefix ends right after the stable head and the
// whole (often huge) history is re-billed every turn / every HITL form submission. Placing
// it at the TAIL keeps the stable head + the entire history as one byte-stable, growing
// prefix, so the provider caches the whole history cross-turn; only the tiny volatile tail
// is fresh. Empirically (qwen3.7-plus, 36k-token history, changed-question turn): front
// caches 11%, tail caches 100%. DashScope does NOT hoist the trailing system message.
func (m *MessageModifier) Modify(ctx context.Context, input []*schema.Message) []*schema.Message {
	m.mu.Lock()
	if !m.headBuilt {
		m.frozenStableHead = m.buildStableHead()
		m.frozenVolatileHead = m.buildVolatileHead(ctx, input)
		m.headBuilt = true
	}
	stable := m.frozenStableHead
	volatile := m.frozenVolatileHead
	m.mu.Unlock()

	result := make([]*schema.Message, 0, len(input)+2)
	result = append(result, schema.SystemMessage(stable))
	result = append(result, input...)
	if volatile != "" {
		result = append(result, schema.SystemMessage(volatile))
	}
	return result
}

// buildStableHead assembles the turn-INVARIANT head: configured system prompt + the
// available-tools whitelist + the HITL halt directive. It carries no per-turn or
// per-step content, so it stays byte-identical across the whole conversation and the
// cache_control modifier (which marks the first system message) caches it cross-turn.
func (m *MessageModifier) buildStableHead() string {
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

	return sb.String()
}

// buildVolatileHead assembles the per-turn head: the task-focus restatement of the
// user's current question + every context reminder's content, captured ONCE for the
// turn and ordered by priority (higher priority later, for recency). It is frozen
// within the turn (so no mid-turn change collapses the within-turn cache) but differs
// between turns, so Modify emits it as a SEPARATE system message at the TAIL — after the
// whole conversation, never inside the cacheable prefix and never the cache_control
// breakpoint — so a changed question does not evict the append-only history from the
// cross-turn cache. Returns "" when there is no task focus and no reminder, so Modify
// can skip emitting an empty message.
func (m *MessageModifier) buildVolatileHead(ctx context.Context, input []*schema.Message) string {
	var sb strings.Builder

	// Task focus: restate the user's question so a long tool loop stays anchored to
	// THIS turn's request. Stable within the turn (frozen at the first model call).
	if q := latestUserQuestion(input); q != "" {
		fmt.Fprintf(&sb, "**CURRENT TASK:** Answer the user's question: %q\nDo NOT get distracted - answer THIS question!",
			sanitizeForSystemPrompt(q, 500))
	}

	// Append every context reminder's content, captured ONCE for the turn.
	for _, content := range m.collectReminders(ctx) {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
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
	m.frozenStableHead = ""
	m.frozenVolatileHead = ""
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
