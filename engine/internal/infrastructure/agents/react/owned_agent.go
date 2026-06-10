package react

import (
	"context"
	stdErrors "errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agents/callbacks"
	"github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// defaultTurnTimeout bounds a single turn when the agent sets no max_turn_duration.
// It is a backstop against a hung provider, distinct from the graph's between-step
// time-wall routing; max_turn_duration overrides it.
const defaultTurnTimeout = 120 * time.Second

// terminalHolder carries the graph's chosen terminal reason out of a run, so the
// orchestration can emit the hardcoded fallback if the finalize model yields
// nothing. Threaded through the run context (the graph is built once but runs
// per turn), first-wins, thread-safe.
type terminalHolder struct {
	mu     sync.Mutex
	reason callbacks.TerminalReason
	tool   string
	detail string
}

func (h *terminalHolder) set(reason callbacks.TerminalReason, tool, detail string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.reason == callbacks.TerminalNone {
		h.reason = reason
		h.tool = tool
		h.detail = detail
	}
}

func (h *terminalHolder) get() (callbacks.TerminalReason, string, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reason, h.tool, h.detail
}

type terminalHolderKey struct{}

func withTerminalHolder(ctx context.Context, h *terminalHolder) context.Context {
	return context.WithValue(ctx, terminalHolderKey{}, h)
}

func terminalHolderFrom(ctx context.Context) *terminalHolder {
	h, _ := ctx.Value(terminalHolderKey{}).(*terminalHolder)
	return h
}

// streamOwned executes one turn through our hand-built graph instead of eino's
// prebuilt react.Agent. Budget and loop terminations are routed inside the graph
// to a tool-less finalize node, so the model produces a real summary that streams
// out as a normal answer — there is no post-hoc error→graceful-answer mapping and
// no shadow-state reconstruction. The hardcoded message survives only as the
// fallback when the finalize model yields nothing.
func (a *Agent) streamOwned(ctx context.Context, input string, callback func(chunk string) error, eventCallback func(event *domain.AgentEvent) error) error {
	slog.InfoContext(ctx, "[OWNED] starting streamOwned", "input_length", len(input))

	messages := a.buildMessagesWithHistory(input)
	if a.contextLogger != nil {
		a.contextLogger.LogContext(ctx, messages, 0)
	}

	var finalContent string
	wrappedCallback := func(chunk string) error {
		finalContent += chunk
		if callback != nil {
			return callback(chunk)
		}
		return nil
	}

	// Turn-time backstop: the graph's time-wall routing catches a turn that runs
	// over budget between calls; this deadline catches a single call that hangs.
	streamTimeout := defaultTurnTimeout
	if a.maxTurnDuration > 0 {
		streamTimeout = time.Duration(a.maxTurnDuration) * time.Second
	}
	streamCtx, cancel := context.WithTimeout(ctx, streamTimeout)
	defer cancel()
	// The terminal holder carries the graph's chosen reason (budget/loop) out to
	// the fallback below; the graph writes it via the onTerminal hook.
	holder := &terminalHolder{}
	streamCtx = withTerminalHolder(streamCtx, holder)

	// The graph owns loop policy (DisableLoopBreakers), so the callbacks breakers
	// stand down. AbortLoop is the step watchdog's lever: a hung step cancels the
	// run context, which the graph cannot do itself.
	cb := callbacks.NewBuilder(callbacks.BuilderConfig{
		EventCallback:       eventCallback,
		ChunkCallback:       wrappedCallback,
		SessionID:           a.sessionID,
		AgentID:             a.agentID,
		ToolCallRecorder:    a.toolCallRecorder,
		AbortLoop:           cancel,
		DisableLoopBreakers: true,
	})
	if a.messageModifier != nil {
		a.messageModifier.StartTurn()
	}

	reader, err := a.ownedRun.Stream(streamCtx, messages, cb.BuildComposeCallbacksOption())
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if reason, ok := graphBackstopReason(err, ctx.Err()); ok {
			cb.EmitGracefulFallback(ctx, reason, "", "")
			cb.EmitTokenUsage(ctx, a.lastContextTokens())
			return nil
		}
		return errors.Wrap(err, errors.CodeInternal, "owned agent stream failed")
	}

	stopWatchdog := cb.StartStepWatchdog(streamCtx, a.stepWatchdogDuration())

	recvCount := 0
	for {
		if ctx.Err() != nil {
			slog.InfoContext(ctx, "[OWNED] outer context cancelled, stopping drain", "recv_count", recvCount)
			break
		}
		_, rerr := reader.Recv()
		recvCount++
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			if ctx.Err() != nil {
				break
			}
			// Hard wall in the unlimited-budget config: end gracefully via the
			// fallback below rather than surfacing a bare error.
			if reason, ok := graphBackstopReason(rerr, ctx.Err()); ok {
				holder.set(reason, "", "")
				slog.InfoContext(ctx, "[OWNED] graph hit hard backstop, ending gracefully", "reason", reason.String())
				break
			}
			stopWatchdog()
			reader.Close()
			return errors.Wrap(rerr, errors.CodeInternal, "owned stream read failed")
		}
	}
	stopWatchdog()
	reader.Close()

	cb.WaitStreamDone()
	cb.FinalizeAccumulatedText(ctx)

	// HITL turns: the widget is the message — drop any prose so history and the
	// final-answer event stay consistent with the suppressed live stream.
	if cb.HITLSeen() && finalContent != "" {
		slog.InfoContext(ctx, "[OWNED] suppressing assistant content on HITL turn", "dropped_length", len(finalContent))
		finalContent = ""
	}

	// Fallback: a terminal turn whose finalize model produced nothing still owes
	// the client a graceful answer. The reason comes from the graph (budget/loop)
	// via the holder, or from the step watchdog (a hung step — the carve-out that
	// keeps the hardcoded message). Emit only when nothing was streamed.
	if !cb.HITLSeen() && finalContent == "" {
		if reason, tool, detail := gracefulTerminalReason(holder, cb); reason != callbacks.TerminalNone {
			cb.EmitGracefulFallback(ctx, reason, tool, detail)
			slog.InfoContext(ctx, "[OWNED] finalize produced no content, emitted hardcoded fallback", "reason", reason.String())
		}
	}

	cb.EmitTokenUsage(ctx, a.lastContextTokens())

	if finalContent != "" {
		messages = append(messages, &schema.Message{Role: schema.Assistant, Content: finalContent})
	}
	if a.contextLogger != nil {
		a.contextLogger.LogContextSummary(ctx, messages)
	}

	slog.InfoContext(ctx, "[OWNED] streamOwned completed", "recv_count", recvCount, "answer_length", len(finalContent))
	return nil
}

// graphBackstopReason maps a hard-wall error out of the owned graph to a graceful
// terminal reason. It is the backstop for the unlimited-budget config, where our
// routing never caps the loop: eino's WithMaxRunSteps fires (ErrExceedMaxSteps),
// or our own turn-time deadline on the run context expires (DeadlineExceeded while
// the outer client context is still alive). A genuine client cancel (outer ctx
// done) is NOT a budget and must surface as cancellation, not a graceful answer.
func graphBackstopReason(err error, outerCtxErr error) (callbacks.TerminalReason, bool) {
	if stdErrors.Is(err, compose.ErrExceedMaxSteps) || errors.Is(err, errors.CodeAgentBudgetExhausted) {
		return callbacks.TerminalStepBudget, true
	}
	if outerCtxErr == nil && stdErrors.Is(err, context.DeadlineExceeded) {
		return callbacks.TerminalTimeBudget, true
	}
	return callbacks.TerminalNone, false
}

// gracefulTerminalReason returns the turn's terminal reason: the graph's choice
// (budget/loop via the holder) takes precedence, falling back to the step
// watchdog's reason recorded on the callbacks builder (a hung step).
func gracefulTerminalReason(holder *terminalHolder, cb *callbacks.AgentCallbackBuilder) (callbacks.TerminalReason, string, string) {
	if reason, tool, detail := holder.get(); reason != callbacks.TerminalNone {
		return reason, tool, detail
	}
	if reason, tool, detail, ok := cb.TerminalTripped(); ok {
		return reason, tool, detail
	}
	return callbacks.TerminalNone, "", ""
}

// runOwned executes one turn through the owned graph in non-streaming mode (the
// counterpart of streamOwned for RunWithCallbacks). Events still flow through the
// callbacks layer; the final assistant answer is emitted explicitly, as the
// non-streaming model callback only emits answer chunks.
func (a *Agent) runOwned(ctx context.Context, input string, eventCallback func(event *domain.AgentEvent) error) (string, error) {
	slog.InfoContext(ctx, "[OWNED] starting runOwned", "input_length", len(input))

	messages := a.buildMessagesWithHistory(input)
	if a.contextLogger != nil {
		a.contextLogger.LogContext(ctx, messages, 0)
	}

	streamTimeout := defaultTurnTimeout
	if a.maxTurnDuration > 0 {
		streamTimeout = time.Duration(a.maxTurnDuration) * time.Second
	}
	streamCtx, cancel := context.WithTimeout(ctx, streamTimeout)
	defer cancel()
	holder := &terminalHolder{}
	streamCtx = withTerminalHolder(streamCtx, holder)

	cb := callbacks.NewBuilder(callbacks.BuilderConfig{
		EventCallback:       eventCallback,
		SessionID:           a.sessionID,
		AgentID:             a.agentID,
		ToolCallRecorder:    a.toolCallRecorder,
		AbortLoop:           cancel,
		DisableLoopBreakers: true,
	})
	if a.messageModifier != nil {
		a.messageModifier.StartTurn()
	}

	stopWatchdog := cb.StartStepWatchdog(streamCtx, a.stepWatchdogDuration())
	result, err := a.ownedRun.Invoke(streamCtx, messages, cb.BuildComposeCallbacksOption())
	stopWatchdog()

	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		// A watchdog abort cancels streamCtx (not the outer ctx); surface the
		// graceful hardcoded fallback rather than a bare error.
		if reason, tool, detail := gracefulTerminalReason(holder, cb); reason != callbacks.TerminalNone {
			msg := cb.EmitGracefulFallback(ctx, reason, tool, detail)
			cb.EmitTokenUsage(ctx, a.lastContextTokens())
			return msg, nil
		}
		// Hard wall in the unlimited-budget config (eino max-steps backstop or our
		// turn-time deadline) — also graceful, never a bare error.
		if reason, ok := graphBackstopReason(err, ctx.Err()); ok {
			msg := cb.EmitGracefulFallback(ctx, reason, "", "")
			cb.EmitTokenUsage(ctx, a.lastContextTokens())
			return msg, nil
		}
		return "", errors.Wrap(err, errors.CodeInternal, "owned agent run failed")
	}

	content := result.Content
	if cb.HITLSeen() {
		content = ""
	}

	if content == "" && !cb.HITLSeen() {
		if reason, tool, detail := gracefulTerminalReason(holder, cb); reason != callbacks.TerminalNone {
			content = cb.EmitGracefulFallback(ctx, reason, tool, detail)
		}
	} else if content != "" && eventCallback != nil {
		if emitErr := eventCallback(&domain.AgentEvent{
			Type:       domain.EventTypeAnswer,
			Timestamp:  time.Now(),
			Step:       cb.GetStep(),
			Content:    content,
			IsComplete: true,
		}); emitErr != nil {
			slog.WarnContext(ctx, "[OWNED] failed to emit final answer event", "error", emitErr)
		}
	}

	cb.EmitTokenUsage(ctx, a.lastContextTokens())

	if content != "" {
		messages = append(messages, &schema.Message{Role: schema.Assistant, Content: content})
	}
	if a.contextLogger != nil {
		a.contextLogger.LogContextSummary(ctx, messages)
	}

	slog.InfoContext(ctx, "[OWNED] runOwned completed", "answer_length", len(content))
	return content, nil
}
