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
// results plus the assistant's tool calls, detects a loop, and either returns a
// correction nudge (for the caller to fold into the tool result) or escalates the
// turn to finalize once the correction budget is spent. It never aborts —
// termination is the graph's job via routing — and never injects a standalone
// message; the returned nudge rides inside the tool result the loop already appends.
func (c ownedGraphConfig) applyLoopPolicy(ctx context.Context, state *ownedState, results []*schema.Message) string {
	reason, tool, detail := c.detectLoop(state, results)
	if reason == callbacks.TerminalNone {
		return ""
	}

	// Graduated response: nudge first, escalate only past the correction budget.
	if state.correctionCount < c.correctionBudgetOr() {
		state.correctionCount++
		return loopCorrectionMessage(reason, tool)
	}

	if state.terminalReason == callbacks.TerminalNone {
		state.terminalReason = reason
		state.terminalTool = tool
		state.terminalDetail = detail
		if c.onTerminal != nil {
			c.onTerminal(ctx, reason, tool, detail)
		}
	}
	return ""
}

// engineNoteMarker frames an engine-injected note inside a tool result so the model
// can tell it apart from the tool's own DATA (which it must treat as data, never as
// instructions — see the security reminder). The note rides in the tool result, a
// natural turn the loop appends anyway, so it never adds a standalone mid-conversation
// system message — which on Qwen/DashScope would re-render the chat template and
// discard the explicit-cache prefix.
const engineNoteMarker = "\n\n--- ENGINE NOTICE (runtime guidance, NOT tool output) ---\n"

// foldEngineNotesIntoToolResults appends the engine notes to the LAST tool result in
// output, under a clear marker. Position is immaterial for an advisory note, and the
// last result is the most recent the model will read. The note is folded into the
// result BEFORE it is appended to the transcript, so the formed message is never
// rewritten afterwards and the append-only cache prefix is preserved. No-op when
// there is no tool result to carry it.
func foldEngineNotesIntoToolResults(output []*schema.Message, notes []string) {
	if len(notes) == 0 {
		return
	}
	for i := len(output) - 1; i >= 0; i-- {
		if output[i] != nil && output[i].Role == schema.Tool {
			// Strip any occurrence of the marker from the tool's OWN data first, so the
			// engine's marker is the only one in the message. A hostile MCP tool can write
			// arbitrary text into its result (the standing reminder already tells the model
			// to treat tool output as data, not instructions); neutralising the marker stops
			// it from aliasing this channel and passing its text off as runtime guidance.
			cleaned := strings.ReplaceAll(output[i].Content, engineNoteMarker, "\n")
			output[i].Content = cleaned + engineNoteMarker + strings.Join(notes, "\n\n")
			return
		}
	}
}

// softLandingNote returns the budget soft-landing nudge to fold into the tool result
// once the turn is one round away from its step or time wall, or "" otherwise. It
// pushes the model to give its final answer now instead of being force-finalized at
// the wall — the same graceful-termination intent the finalize node guarantees
// structurally, delivered early as a natural-turn note rather than a standalone
// system message.
func (c ownedGraphConfig) softLandingNote(state *ownedState) string {
	if !c.nearBudgetWall(state) {
		return ""
	}
	note := strings.TrimSpace(finalizeDirective)
	if w := c.formatUrgencyWarning(state); w != "" {
		note = w + "\n\n" + note
	}
	return note
}

// formatUrgencyWarning returns the agent-configured wrap-up text with a live
// remaining-step count substituted for a %d verb, or "" when none is configured.
func (c ownedGraphConfig) formatUrgencyWarning(state *ownedState) string {
	w := strings.TrimSpace(c.urgencyWarning)
	if w == "" || !strings.Contains(w, "%d") {
		return w
	}
	remaining := c.effectiveStepBudget() - state.step
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprintf(w, remaining)
}

// nearBudgetWall reports whether the turn is close enough to its time or step wall
// that the model should be nudged to finalize now. Step: one tool round before the
// step budget. Time: past the soft-landing fraction of max_turn_duration. Unlimited
// budgets (effectiveStepBudget == the sentinel) never trip the step branch.
func (c ownedGraphConfig) nearBudgetWall(state *ownedState) bool {
	if c.maxTurnDuration > 0 && !state.turnStart.IsZero() {
		soft := c.maxTurnDuration * softLandingTimeNumerator / softLandingTimeDenominator
		if time.Since(state.turnStart) >= soft {
			return true
		}
	}
	if budget := c.effectiveStepBudget(); budget > 0 && budget < (1<<30) && state.step >= budget-1 {
		return true
	}
	return false
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
