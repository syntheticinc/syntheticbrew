package react

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agents/callbacks"
)

// owned_loop_breakers.go owns the loop-correction policy for the owned graph,
// replacing the callbacks-layer breaker. A non-productive loop is corrected
// (nudge the model, keep going) before it is ever terminated; only a loop that
// survives the correction budget escalates to the finalize node. This makes a
// false positive harmless — the first response to a suspected loop is guidance,
// not a turn-ending message — which matters because some "repeats" are
// legitimate (polling a deploy status on a timer).

// applyLoopPolicy runs as the tools-node post-handler: it inspects the tool
// results plus the assistant's tool calls, detects a loop, and either stages a
// correction nudge or escalates the turn to finalize once the correction budget
// is spent. It never aborts — termination is the graph's job via routing.
func (c ownedGraphConfig) applyLoopPolicy(ctx context.Context, state *ownedState, results []*schema.Message) {
	reason, tool, detail := c.detectLoop(state, results)
	if reason == callbacks.TerminalNone {
		return
	}

	// Graduated response: nudge first, escalate only past the correction budget.
	if state.correctionCount < c.correctionBudgetOr() {
		state.correctionCount++
		state.pendingCorrection = loopCorrectionMessage(reason, tool)
		return
	}

	if state.terminalReason == callbacks.TerminalNone {
		state.terminalReason = reason
		state.terminalTool = tool
		state.terminalDetail = detail
		if c.onTerminal != nil {
			c.onTerminal(ctx, reason, tool, detail)
		}
	}
}

// detectLoop updates the breaker counters from this round and reports a loop
// reason when a threshold is crossed. Identical-args detection is single-tool
// only and gated on tight timing (paced polling never accrues a streak);
// error-loop counts consecutive [ERROR] results per tool.
func (c ownedGraphConfig) detectLoop(state *ownedState, results []*schema.Message) (callbacks.TerminalReason, string, string) {
	if name, args, ok := lastSingleToolCall(state.Messages); ok {
		key := name + "\x00" + args
		now := time.Now()
		tight := !state.lastSameArgsAt.IsZero() && now.Sub(state.lastSameArgsAt) <= c.identicalTightWindowOr()
		if key == state.lastArgsKey && tight {
			state.sameArgsCount++
		} else {
			state.sameArgsCount = 1
		}
		state.lastArgsKey = key
		state.lastSameArgsAt = now
		if state.sameArgsCount >= c.sameArgsThresholdOr() {
			return callbacks.TerminalIdenticalArgsLoop, name, ""
		}
	} else {
		// A different call shape (no tool call, or parallel calls) breaks the
		// identical-args streak.
		state.lastArgsKey = ""
		state.sameArgsCount = 0
	}

	if state.consecErrByTool == nil {
		state.consecErrByTool = make(map[string]int)
	}
	names := toolCallNamesByID(state.Messages)
	for _, r := range results {
		if r.Role != schema.Tool {
			continue
		}
		toolName := names[r.ToolCallID]
		if strings.HasPrefix(r.Content, "[ERROR]") {
			state.consecErrByTool[toolName]++
			if state.consecErrByTool[toolName] >= c.errLoopThresholdOr() {
				return callbacks.TerminalToolErrorLoop, toolName, r.Content
			}
		} else {
			state.consecErrByTool[toolName] = 0
		}
	}

	return callbacks.TerminalNone, "", ""
}

// lastSingleToolCall returns the name + arguments of the most recent assistant
// message that carries exactly one tool call. Identical-args detection only
// applies to the single-call degenerate loop; a multi-call step is not a repeat.
func lastSingleToolCall(messages []*schema.Message) (name, args string, ok bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Role != schema.Assistant {
			continue
		}
		if len(m.ToolCalls) != 1 {
			return "", "", false
		}
		return m.ToolCalls[0].Function.Name, m.ToolCalls[0].Function.Arguments, true
	}
	return "", "", false
}

// toolCallNamesByID maps tool-call IDs to tool names from the most recent
// assistant message, so a tool-result message can be attributed to its tool.
func toolCallNamesByID(messages []*schema.Message) map[string]string {
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Role != schema.Assistant || len(m.ToolCalls) == 0 {
			continue
		}
		names := make(map[string]string, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			names[tc.ID] = tc.Function.Name
		}
		return names
	}
	return map[string]string{}
}

// safeToolNamePattern is the allowlist a tool name must match to be quoted
// verbatim into a model-facing message: the OpenAI function-name charset plus the
// dotted/colon MCP conventions, length 1..128.
var safeToolNamePattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// sanitizeToolNameForPrompt neutralises a tool name before it is interpolated
// into a System-role correction message. Tool names are tenant/MCP-controlled and
// are NOT charset-validated on non-OpenAI routes, so a hostile name could smuggle
// instructions (with or without newlines) into a high-authority System turn. An
// allowlist is used, not a denylist: a conforming name — including the dotted MCP
// convention (`device.list`) — passes verbatim; anything else is replaced with a
// neutral placeholder so no attacker-controlled text reaches the System turn.
func sanitizeToolNameForPrompt(name string) string {
	if safeToolNamePattern.MatchString(name) {
		return name
	}
	return "the tool"
}

// loopCorrectionMessage builds the nudge for a detected loop. Identical-args is a
// gentle, polling-aware steer that does NOT forbid the tool (legitimate polling
// must keep working); tool-error tells the model to stop calling a tool that
// keeps failing and work with what it has.
func loopCorrectionMessage(reason callbacks.TerminalReason, tool string) string {
	tool = sanitizeToolNameForPrompt(tool)
	switch reason {
	case callbacks.TerminalIdenticalArgsLoop:
		return fmt.Sprintf("You have called `%s` with identical arguments several times in quick succession without new information. If you are intentionally polling for a change, wait between checks instead of calling back-to-back. Otherwise stop repeating this exact call — try a materially different approach, or give your best final answer now from what you already have.", tool)
	case callbacks.TerminalToolErrorLoop:
		return fmt.Sprintf("The `%s` tool has failed repeatedly and is unlikely to succeed on another identical retry. Stop calling it. Use the information you have already gathered, or clearly tell the user that this operation failed and what you could not complete.", tool)
	default:
		return ""
	}
}
