package agents

import (
	"context"
	"log/slog"

	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

// charsPerToken is a rough chars/token ratio used ONLY for the context logger's
// display estimates. The compression decision uses the empirical TokenCalibrator
// ratio (or defaultCharsPerToken before a sample exists), never this constant.
const charsPerToken = 4

// charsToTokens converts character count to an approximate token count for display.
func charsToTokens(chars int) int {
	return chars / charsPerToken
}

// defaultCharsPerToken is the conservative chars-per-token ratio used to size the
// context budget before the TokenCalibrator has a real provider sample. Tool- and
// JSON-heavy multilingual traffic runs ~2.7 real chars/token; biasing slightly low
// makes the token estimate run high, so the budget guard trips early rather than
// shipping an over-limit request.
const defaultCharsPerToken = 2.5

// contextHeadroomFraction reserves a fraction of the configured budget as a safety
// margin. The calibration ratio lags one model call, so a turn can grow (a large
// tool result) after the last sample; the headroom absorbs that single-step growth
// rather than overflowing the model window.
const contextHeadroomFraction = 0.10

// ContextRewriterConfig parameterizes the context rewriter. Only MaxContextTokens
// is required; the rest refine the size estimate (Calibrator, fixed overheads) or
// add observability (ContextLogger, OnContextSize).
type ContextRewriterConfig struct {
	// MaxContextTokens is the hard ceiling for the full request, in tokens.
	MaxContextTokens int
	// SystemPromptChars and ToolSchemaChars are per-request payloads injected AFTER
	// this rewriter runs (system prompt by the modifier, tool schemas by the model
	// binding), so they are absent from `input` but DO consume the budget.
	SystemPromptChars int
	ToolSchemaChars   int
	// Calibrator supplies the empirical chars-per-token ratio from observed usage.
	// Nil falls back to defaultCharsPerToken.
	Calibrator *TokenCalibrator
	// ContextLogger, when set, receives the compression report and final context.
	ContextLogger *ContextLogger
	// OnContextSize reports the estimated full-context token count after each pass
	// (both within-limit and post-compression).
	OnContextSize func(int)
}

// messageChars estimates the total character size of a message including tool calls.
// This provides a more accurate estimate for token counting than Content alone.
func messageChars(msg *schema.Message) int {
	total := len(msg.Content)

	// Count tool calls (assistant messages)
	for _, tc := range msg.ToolCalls {
		total += len(tc.ID)
		total += len(tc.Type)
		total += len(tc.Function.Name)
		total += len(tc.Function.Arguments)
	}

	// Count tool result metadata (tool messages)
	total += len(msg.ToolCallID)
	total += len(msg.ToolName)
	total += len(msg.Name)

	return total
}

// estimateMessagesChars sums the measured size of a message slice.
func estimateMessagesChars(msgs []*schema.Message) int {
	total := 0
	for _, msg := range msgs {
		total += messageChars(msg)
	}
	return total
}

// tokensFromChars converts a char count to tokens using the supplied ratio.
func tokensFromChars(chars int, charsPerTokenRatio float64) int {
	if charsPerTokenRatio <= 0 {
		return 0
	}
	return int(float64(chars) / charsPerTokenRatio)
}

// NewContextRewriter creates a context rewriter with default config: the
// conservative cold-start ratio, no calibration, no logging. Used by snapshot
// compression and tests. maxContextTokens is the maximum context size in TOKENS.
func NewContextRewriter(maxContextTokens int) react.MessageModifier {
	return NewContextRewriterFromConfig(ContextRewriterConfig{MaxContextTokens: maxContextTokens})
}

// NewContextRewriterFromConfig builds the rewriter that decides — in real tokens —
// whether the running context exceeds the budget and, if so, compresses it while
// preserving chronological order and assistant/tool pairing. The budget covers the
// full request: conversation messages plus the system prompt and tool schemas that
// are injected after this rewriter runs.
func NewContextRewriterFromConfig(cfg ContextRewriterConfig) react.MessageModifier {
	fixedOverheadChars := cfg.SystemPromptChars + cfg.ToolSchemaChars

	return func(ctx context.Context, input []*schema.Message) []*schema.Message {
		if len(input) == 0 {
			return input
		}

		ratio := defaultCharsPerToken
		if cfg.Calibrator != nil {
			ratio = cfg.Calibrator.CharsPerToken()
		}
		// Budget in chars = (token ceiling minus headroom) scaled by the real ratio.
		maxChars := int(float64(cfg.MaxContextTokens) * (1 - contextHeadroomFraction) * ratio)

		report := func(keptMsgChars int) {
			fullChars := keptMsgChars + fixedOverheadChars
			if cfg.Calibrator != nil {
				cfg.Calibrator.RecordRequestChars(fullChars)
			}
			if cfg.OnContextSize != nil {
				cfg.OnContextSize(tokensFromChars(fullChars, ratio))
			}
		}

		msgChars := estimateMessagesChars(input)
		if msgChars+fixedOverheadChars <= maxChars {
			slog.DebugContext(ctx, "context within limit, no compression needed",
				"tokens", tokensFromChars(msgChars+fixedOverheadChars, ratio),
				"limit_tokens", cfg.MaxContextTokens, "chars_per_token", ratio)
			report(msgChars)
			return input
		}

		slog.DebugContext(ctx, "context exceeds limit, compressing",
			"tokens", tokensFromChars(msgChars+fixedOverheadChars, ratio),
			"limit_tokens", cfg.MaxContextTokens, "chars_per_token", ratio)

		// Budget available to conversation messages after the mandatory overhead.
		msgBudget := maxChars - fixedOverheadChars
		if msgBudget < 0 {
			msgBudget = 0
		}

		result, removed := compressMessages(ctx, input, msgBudget)

		report(estimateMessagesChars(result))

		slog.DebugContext(ctx, "context compressed",
			"before", len(input), "after", len(result),
			"removed", len(input)-len(result), "removed_tool_results", len(removed))

		if cfg.ContextLogger != nil {
			cfg.ContextLogger.LogCompressionReport(ctx, len(input), len(result), removed)
			cfg.ContextLogger.LogContext(ctx, result, -1)
		}

		return result
	}
}

// compressMessages fits the conversation into budgetChars (chars reserved for the
// conversation, system prompt + tool schemas already subtracted by the caller). It
// keeps the system message and the most recent user turns that fit (hard ceiling),
// then fits as many recent assistant/tool pairs as the remainder allows, preserving
// chronological order. Returns the kept messages and labels for what was dropped.
func compressMessages(ctx context.Context, input []*schema.Message, budgetChars int) ([]*schema.Message, []string) {
	var systemPrompt *schema.Message
	var conv []*schema.Message
	for _, msg := range input {
		if msg.Role == schema.System {
			systemPrompt = msg
		} else {
			conv = append(conv, msg)
		}
	}

	systemSize := 0
	if systemPrompt != nil {
		systemSize = messageChars(systemPrompt)
	}

	// Hard ceiling: keep the most recent user turns that fit; drop the oldest.
	conv, droppedUsers := truncateToUserBudget(ctx, conv, budgetChars-systemSize)

	// Reserve the kept user messages; fit recent assistant/tool pairs in the rest.
	userSize := 0
	for _, msg := range conv {
		if msg.Role == schema.User {
			userSize += messageChars(msg)
		}
	}
	remaining := budgetChars - systemSize - userSize
	if remaining < 0 {
		remaining = 0
	}

	keepNonUser, removedToolResults := fitNonUserMessages(conv, remaining)

	result := make([]*schema.Message, 0, len(conv)+1)
	if systemPrompt != nil {
		result = append(result, systemPrompt)
	}
	for i, msg := range conv {
		if msg.Role == schema.User || keepNonUser[i] {
			result = append(result, msg)
		}
	}

	removedToolResults = append(removedToolResults, droppedUsers...)
	return result, removedToolResults
}

// truncateToUserBudget drops the oldest user turns until the kept user messages fit
// within userBudgetChars, always keeping at least the most recent user message (the
// live turn). It returns the surviving suffix — everything from the earliest kept
// user message onward — and a label per dropped user turn.
func truncateToUserBudget(ctx context.Context, conv []*schema.Message, userBudgetChars int) ([]*schema.Message, []string) {
	lastUserIdx := -1
	for i := len(conv) - 1; i >= 0; i-- {
		if conv[i].Role == schema.User {
			lastUserIdx = i
			break
		}
	}
	if lastUserIdx == -1 {
		return conv, nil // no user messages; nothing to anchor truncation on
	}

	// Walk users newest→oldest, accumulating size; the earliest that still fits
	// becomes the cut point. The live turn is always kept, even if it alone exceeds.
	start := lastUserIdx
	used := messageChars(conv[lastUserIdx])
	for i := lastUserIdx - 1; i >= 0; i-- {
		if conv[i].Role != schema.User {
			continue
		}
		sz := messageChars(conv[i])
		if used+sz > userBudgetChars {
			break
		}
		used += sz
		start = i
	}

	if messageChars(conv[lastUserIdx]) > userBudgetChars {
		slog.WarnContext(ctx, "most recent user message alone exceeds context budget; sending anyway",
			"user_chars", messageChars(conv[lastUserIdx]), "user_budget_chars", userBudgetChars)
	}

	if start == 0 {
		return conv, nil
	}

	var dropped []string
	for _, msg := range conv[:start] {
		if msg.Role == schema.User {
			dropped = append(dropped, "user turn (evicted)")
		}
	}
	return conv[start:], dropped
}

// fitNonUserMessages decides which non-user messages to keep within remaining
// chars, walking from the most recent backwards and keeping assistant/tool pairs
// together. User messages are kept unconditionally by the caller, so they are
// skipped here. Returns a keep-set over conv indices and labels for dropped tool
// interactions.
func fitNonUserMessages(conv []*schema.Message, remaining int) (map[int]bool, []string) {
	f := newMessageFitter(conv, remaining)
	for i := len(conv) - 1; i >= 0; i-- {
		f.consider(i)
	}
	return f.keep, f.removed
}

// messageFitter holds the running state of the backwards fit pass so each step is
// a small, flat method rather than one deeply nested loop body.
//
// A tool-calling assistant and ALL of its tool results form one atomic group: they
// are kept together or dropped together. Keeping an assistant while evicting one of
// its tool results would leave a tool_call with no matching result — a malformed
// transcript that OpenAI-strict providers reject — so the group is the unit of fit.
type messageFitter struct {
	conv      []*schema.Message
	remaining int
	members   map[int][]int // assistant index -> [assistant idx, ...its tool result idxs]
	groupOf   map[int]int   // any tool-result idx -> owning assistant idx
	keep      map[int]bool
	doneGroup map[int]bool
	used      int
	removed   []string
}

func newMessageFitter(conv []*schema.Message, remaining int) *messageFitter {
	f := &messageFitter{
		conv:      conv,
		remaining: remaining,
		members:   make(map[int][]int),
		groupOf:   make(map[int]int),
		keep:      make(map[int]bool),
		doneGroup: make(map[int]bool),
	}

	idToAssistant := make(map[string]int)
	for i, msg := range conv {
		if msg.Role == schema.Assistant && len(msg.ToolCalls) > 0 {
			f.members[i] = []int{i}
			for _, tc := range msg.ToolCalls {
				idToAssistant[tc.ID] = i
			}
		}
	}
	for j, msg := range conv {
		if msg.Role != schema.Tool {
			continue
		}
		a := f.ownerAssistant(j, idToAssistant)
		if a < 0 {
			continue // orphaned tool result; handled as a droppable singleton
		}
		f.members[a] = append(f.members[a], j)
		f.groupOf[j] = a
	}
	return f
}

// ownerAssistant resolves the assistant that owns a tool result, by call ID or by
// the immediately preceding tool-calling assistant. Returns -1 when none matches.
func (f *messageFitter) ownerAssistant(j int, idToAssistant map[string]int) int {
	msg := f.conv[j]
	if msg.ToolCallID != "" {
		if idx, ok := idToAssistant[msg.ToolCallID]; ok {
			return idx
		}
	}
	if j > 0 {
		prev := f.conv[j-1]
		if prev.Role == schema.Assistant && len(prev.ToolCalls) > 0 {
			return j - 1
		}
	}
	return -1
}

// consider routes one message to the matching fit rule. Users are kept by the
// caller, so they are skipped here.
func (f *messageFitter) consider(i int) {
	msg := f.conv[i]
	switch {
	case msg.Role == schema.User:
		return
	case msg.Role == schema.Tool:
		if a, ok := f.groupOf[i]; ok {
			f.fitGroup(a)
			return
		}
		f.removed = append(f.removed, msg.Name+" (orphaned)")
	case msg.Role == schema.Assistant && len(msg.ToolCalls) > 0:
		f.fitGroup(i)
	default:
		f.fitRegular(i)
	}
}

// fitGroup keeps a tool-calling assistant and all of its tool results together, or
// drops the whole group when it does not fit. An assistant whose tool results are
// all absent is an orphaned call and is dropped.
func (f *messageFitter) fitGroup(assistantIdx int) {
	if f.doneGroup[assistantIdx] {
		return
	}
	f.doneGroup[assistantIdx] = true

	mem := f.members[assistantIdx]
	if len(mem) <= 1 {
		// Assistant with tool_calls but no surviving tool results — dropping it keeps
		// the transcript well-formed.
		if calls := f.conv[assistantIdx].ToolCalls; len(calls) > 0 {
			f.removed = append(f.removed, calls[0].Function.Name+" (orphaned call)")
		}
		return
	}

	size := 0
	for _, idx := range mem {
		size += messageChars(f.conv[idx])
	}
	if f.used+size > f.remaining {
		for _, idx := range mem {
			if f.conv[idx].Role == schema.Tool {
				f.removed = append(f.removed, f.conv[idx].Name)
			}
		}
		return
	}
	for _, idx := range mem {
		f.keep[idx] = true
	}
	f.used += size
}

// fitRegular keeps a plain assistant message when it fits the remaining budget.
func (f *messageFitter) fitRegular(i int) {
	sz := messageChars(f.conv[i])
	if f.used+sz > f.remaining {
		return
	}
	f.keep[i] = true
	f.used += sz
}
