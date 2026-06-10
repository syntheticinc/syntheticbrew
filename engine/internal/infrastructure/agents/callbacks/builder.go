package callbacks

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

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
	Store            *agents.StepContentStore
	SessionID        string
	AgentID          string // "supervisor" or "code-agent-{uuid}"
	ToolCallRecorder ToolCallRecorder
	// AbortLoop cancels the context the Eino react loop runs under. The tool
	// error-loop breaker calls it to actually halt the loop (cancelling a
	// per-callback child context does not stop Eino). May be nil in tests.
	AbortLoop context.CancelFunc
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

	modelHandler := NewModelEventHandler(emitter, counter, extractor, cfg.Store, cfg.ChunkCallback, tokenAcc, activity)
	toolHandler := NewToolEventHandler(emitter, counter, modelHandler, cfg.ToolCallRecorder, cfg.SessionID, terminal, activity)

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

// BuildCallbackOption creates an Eino agent option with the callback handler.
func (b *AgentCallbackBuilder) BuildCallbackOption() agent.AgentOption {
	modelHandler := &ucb.ModelCallbackHandler{
		OnEnd:                 b.modelHandler.OnModelEnd,
		OnEndWithStreamOutput: b.modelHandler.OnModelEndWithStreamOutput,
	}
	toolHandler := &ucb.ToolCallbackHandler{
		OnStart: b.toolHandler.OnToolStart,
		OnEnd:   b.toolHandler.OnToolEnd,
		OnError: b.toolHandler.OnToolError,
	}
	handler := ucb.NewHandlerHelper().
		ChatModel(modelHandler).
		Tool(toolHandler).
		Handler()
	return agent.WithComposeOptions(compose.WithCallbacks(handler))
}

// GetStep returns the current step (thread-safe, public method).
func (b *AgentCallbackBuilder) GetStep() int {
	return b.counter.GetStep()
}

// WaitStreamDone blocks until all streaming goroutines complete.
func (b *AgentCallbackBuilder) WaitStreamDone() {
	b.modelHandler.streamWg.Wait()
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

// TripTerminal records a terminal condition detected by the agent loop itself
// (a budget exhausted by Eino) and cancels the react loop. Loop-breakers and the
// step watchdog trip the same state from inside the callbacks.
func (b *AgentCallbackBuilder) TripTerminal(reason TerminalReason, detail string) {
	b.terminal.Trip(reason, "", detail)
}

// TerminalTripped reports whether the turn was force-terminated this run, with
// the reason, offending tool (if any), and detail.
func (b *AgentCallbackBuilder) TerminalTripped() (reason TerminalReason, tool, detail string, ok bool) {
	return b.terminal.Tripped()
}

// EmitTerminalAnswer emits the graceful final assistant answer for a tripped
// terminal condition and returns its text (for history), or "" when the turn was
// not force-terminated. The model produced no usable answer in these cases, so a
// hardcoded, self-contained message stands in.
func (b *AgentCallbackBuilder) EmitTerminalAnswer(ctx context.Context) string {
	reason, tool, detail, ok := b.terminal.Tripped()
	if !ok {
		return ""
	}
	msg := formatTerminalMessage(reason, tool, detail)
	if msg == "" {
		return ""
	}
	// If partial prose was streamed live before the turn was force-stopped,
	// scrub it first — the graceful answer replaces it (mirrors the HITL scrub),
	// keeping the live stream consistent with persisted history.
	if b.modelHandler.AnyChunkStreamed() {
		b.emitter.Emit(ctx, &domain.AgentEvent{
			Type:      domain.EventTypeRetractAssistant,
			Timestamp: time.Now(),
		})
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
