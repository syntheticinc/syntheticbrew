package callbacks

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	einocallbacks "github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent"
	ucb "github.com/cloudwego/eino/utils/callbacks"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agents"
)

// stepWatchdogTick is how often the step watchdog checks for a hung step.
const stepWatchdogTick = 2 * time.Second

// BuilderConfig holds configuration for constructing the callback builder.
type BuilderConfig struct {
	EventCallback    func(event *domain.AgentEvent) error
	ChunkCallback    func(chunk string) error
	SessionID        string
	AgentID          string // "supervisor" or "code-agent-{uuid}"
	ToolCallRecorder ToolCallRecorder
	// AbortLoop cancels the context the loop runs under. On the eino path the
	// error-loop breaker calls it to halt a runaway loop; on the owned path the
	// step watchdog calls it to abort a hung step. May be nil in tests.
	AbortLoop context.CancelFunc
	// DisableLoopBreakers stands the callbacks-layer loop breakers down. The owned
	// graph owns loop policy (detection + correction + escalation) in its routing,
	// so the callbacks breakers must not also fire. The step watchdog stays active.
	DisableLoopBreakers bool
}

// AgentCallbackBuilder wires together all callback sub-components
// and exposes the public API consumed by the agent.
type AgentCallbackBuilder struct {
	counter      *StepCounter
	emitter      *EventEmitter
	modelHandler *ModelEventHandler
	toolHandler  *ToolEventHandler
	tokenAcc     *TokenAccumulator
	terminal     *TerminalState
	activity     *ActivityClock
}

// NewBuilder creates and wires all callback components.
func NewBuilder(cfg BuilderConfig) *AgentCallbackBuilder {
	agentID := cfg.AgentID
	if agentID == "" {
		agentID = "supervisor"
	}

	counter := NewStepCounter()
	emitter := NewEventEmitter(cfg.EventCallback, agentID)
	extractor := agents.NewReasoningExtractor()
	tokenAcc := NewTokenAccumulator()
	terminal := NewTerminalState(cfg.AbortLoop)
	activity := NewActivityClock()

	modelHandler := NewModelEventHandler(emitter, counter, extractor, cfg.ChunkCallback, tokenAcc, activity)
	toolHandler := NewToolEventHandler(emitter, counter, modelHandler, cfg.ToolCallRecorder, cfg.SessionID, terminal, activity, cfg.DisableLoopBreakers)

	return &AgentCallbackBuilder{
		counter:      counter,
		emitter:      emitter,
		modelHandler: modelHandler,
		toolHandler:  toolHandler,
		tokenAcc:     tokenAcc,
		terminal:     terminal,
		activity:     activity,
	}
}

// buildHandler assembles the model + tool callback handler shared by the eino
// agent path and the owned-graph path.
func (b *AgentCallbackBuilder) buildHandler() einocallbacks.Handler {
	modelHandler := &ucb.ModelCallbackHandler{
		OnEnd:                 b.modelHandler.OnModelEnd,
		OnEndWithStreamOutput: b.modelHandler.OnModelEndWithStreamOutput,
	}
	toolHandler := &ucb.ToolCallbackHandler{
		OnStart: b.toolHandler.OnToolStart,
		OnEnd:   b.toolHandler.OnToolEnd,
		OnError: b.toolHandler.OnToolError,
	}
	return ucb.NewHandlerHelper().
		ChatModel(modelHandler).
		Tool(toolHandler).
		Handler()
}

// BuildCallbackOption creates an Eino agent option with the callback handler.
func (b *AgentCallbackBuilder) BuildCallbackOption() agent.AgentOption {
	return agent.WithComposeOptions(compose.WithCallbacks(b.buildHandler()))
}

// BuildComposeCallbacksOption creates a raw compose option with the callback
// handler, for invoking a hand-built graph runnable directly (the owned loop)
// rather than the prebuilt eino agent.
func (b *AgentCallbackBuilder) BuildComposeCallbacksOption() compose.Option {
	return compose.WithCallbacks(b.buildHandler())
}

// GetStep returns the current step (thread-safe, public method).
func (b *AgentCallbackBuilder) GetStep() int {
	return b.counter.GetStep()
}

// WaitStreamDone blocks until all streaming goroutines complete.
func (b *AgentCallbackBuilder) WaitStreamDone() {
	b.modelHandler.streamWg.Wait()
}

// WaitStreamDoneBounded waits for the streaming goroutines but gives up after
// timeout, so a pathological stream that never closes cannot wedge the request
// handler forever. Returns true if all goroutines finished, false on timeout.
// The owned loop pairs this with the per-frame ctx check in the stream goroutine
// (which stops consuming on cancel); this is the last-resort backstop that lets
// the caller disarm a still-running goroutine when it overruns.
func (b *AgentCallbackBuilder) WaitStreamDoneBounded(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		b.modelHandler.streamWg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// FinalizeAccumulatedText emits EventTypeAnswer for any accumulated streamed text.
func (b *AgentCallbackBuilder) FinalizeAccumulatedText(ctx context.Context) {
	b.modelHandler.FinalizeAccumulatedText(ctx)
}

// EmitTokenUsage emits a token_usage event with accumulated totals from all model calls.
// contextTokens is the actual context window size (in tokens) at the last model call,
// reported by the ContextRewriter via an agent-scoped atomic counter.
// Called after agent execution completes, before ProcessingStopped.
func (b *AgentCallbackBuilder) EmitTokenUsage(ctx context.Context, contextTokens int) {
	total := b.tokenAcc.TotalTokens()
	if total == 0 {
		return
	}
	metadata := map[string]interface{}{
		"total_tokens":      b.tokenAcc.TotalTokens(),
		"prompt_tokens":     b.tokenAcc.PromptTokens(),
		"completion_tokens": b.tokenAcc.CompletionTokens(),
	}
	if contextTokens > 0 {
		metadata["context_tokens"] = contextTokens
	}
	b.emitter.Emit(ctx, &domain.AgentEvent{
		Type:     domain.EventTypeTokenUsage,
		Metadata: metadata,
	})
}

// GetTotalTokens returns accumulated total tokens across all model calls.
func (b *AgentCallbackBuilder) GetTotalTokens() int {
	return b.tokenAcc.TotalTokens()
}

// HITLSeen reports whether a HITL tool fired during this run.
func (b *AgentCallbackBuilder) HITLSeen() bool {
	return b.modelHandler.HITLSeen()
}

// TerminalTripped reports whether the turn was force-terminated this run, with
// the reason, offending tool (if any), and detail. The owned loop reads it to
// surface the step-watchdog reason (a hung step) for its graceful fallback.
func (b *AgentCallbackBuilder) TerminalTripped() (reason TerminalReason, tool, detail string, ok bool) {
	return b.terminal.Tripped()
}

// EmitGracefulFallback emits the hardcoded graceful answer for an explicit
// terminal reason. The owned loop calls this only when its finalize model
// produced no content, so there is nothing streamed to retract. Returns the
// message, or "" when the reason has no message.
func (b *AgentCallbackBuilder) EmitGracefulFallback(ctx context.Context, reason TerminalReason, tool, detail string) string {
	msg := formatTerminalMessage(reason, tool, detail)
	if msg == "" {
		return ""
	}
	b.emitter.Emit(ctx, &domain.AgentEvent{
		Type:       domain.EventTypeAnswer,
		Content:    msg,
		IsComplete: true,
	})
	return msg
}

// StartStepWatchdog launches a watchdog that trips TerminalStepTimeout when a
// single step produces no activity for longer than maxStepDuration (a hung model
// call or tool). Returns a stop function the caller must defer. A non-positive
// maxStepDuration disables the watchdog.
func (b *AgentCallbackBuilder) StartStepWatchdog(ctx context.Context, maxStepDuration time.Duration) func() {
	if maxStepDuration <= 0 {
		return func() {}
	}
	b.activity.Touch()

	tick := stepWatchdogTick
	if maxStepDuration < tick {
		tick = maxStepDuration
	}

	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(tick)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if b.activity.Idle() <= maxStepDuration {
					continue
				}
				if b.terminal.Trip(TerminalStepTimeout, "", fmt.Sprintf("%ds", int(maxStepDuration.Seconds()))) {
					slog.WarnContext(ctx, "[CALLBACK] step watchdog fired, force-stopping turn",
						"max_step_duration", maxStepDuration)
				}
				return
			}
		}
	}()

	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}
