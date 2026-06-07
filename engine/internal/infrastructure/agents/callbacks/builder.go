package callbacks

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent"
	ucb "github.com/cloudwego/eino/utils/callbacks"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agents"
)

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

	modelHandler := NewModelEventHandler(emitter, counter, extractor, cfg.Store, cfg.ChunkCallback, tokenAcc)
	toolHandler := NewToolEventHandler(emitter, counter, modelHandler, cfg.ToolCallRecorder, cfg.SessionID, cfg.AbortLoop)

	return &AgentCallbackBuilder{
		counter:      counter,
		emitter:      emitter,
		modelHandler: modelHandler,
		toolHandler:  toolHandler,
		tokenAcc:     tokenAcc,
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

// ToolLoopAborted reports whether the tool error-loop breaker tripped this run,
// returning the offending tool and its last error.
func (b *AgentCallbackBuilder) ToolLoopAborted() (tool, lastErr string, ok bool) {
	return b.toolHandler.Aborted()
}

// EmitToolLoopAbortAnswer emits a final assistant answer when the error-loop
// breaker force-stopped the turn and returns its text (for history), or "" when
// the breaker did not trip.
func (b *AgentCallbackBuilder) EmitToolLoopAbortAnswer(ctx context.Context) string {
	tool, lastErr, ok := b.toolHandler.Aborted()
	if !ok {
		return ""
	}
	msg := formatToolLoopAbortMessage(tool, lastErr)
	b.emitter.Emit(ctx, &domain.AgentEvent{
		Type:       domain.EventTypeAnswer,
		Content:    msg,
		IsComplete: true,
	})
	return msg
}

// formatToolLoopAbortMessage builds a user-facing message explaining that the
// turn was stopped because a tool kept failing. reason is the last tool error
// with the internal "[ERROR] " marker stripped and length-capped.
func formatToolLoopAbortMessage(tool, lastErr string) string {
	reason := strings.TrimSpace(strings.TrimPrefix(lastErr, "[ERROR]"))
	if len(reason) > 300 {
		reason = reason[:300] + "…"
	}
	if reason == "" {
		return fmt.Sprintf("I couldn't complete this request: the %q operation kept failing, so I stopped retrying to avoid looping. Please check the request details or try again shortly.", tool)
	}
	return fmt.Sprintf("I couldn't complete this request: the %q operation kept failing (%s). I stopped retrying to avoid looping. Please check the request details or try again shortly.", tool, reason)
}
