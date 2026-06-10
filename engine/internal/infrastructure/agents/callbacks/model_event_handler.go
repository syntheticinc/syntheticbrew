package callbacks

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agents"
)

// ModelEventHandler handles model callbacks (OnEnd, OnEndWithStreamOutput).
type ModelEventHandler struct {
	emitter   *EventEmitter
	counter   *StepCounter
	extractor *agents.ReasoningExtractor
	chunkCb   func(chunk string) error
	tokenAcc  *TokenAccumulator
	activity  *ActivityClock

	// Text accumulation state for streaming
	accumulatedReasoning string
	reasoningMu          sync.Mutex
	answerStarted        bool
	accumulatedChunks    string
	accumulatedMu        sync.Mutex
	chunksStreamed       bool // true if chunks were sent via chunkCb (avoid duplicate EventTypeAnswer)

	// hitlSeen — true once a HITL tool_call fires; suppresses accumulated text.
	hitlSeen bool

	// streamWg tracks active streaming goroutines. Agent.Stream() waits on this
	// before returning, preventing ProcessingStopped from firing before all chunks
	// are delivered. Used instead of a channel because OnModelEndWithStreamOutput
	// may be called multiple times during multi-step agent execution.
	streamWg sync.WaitGroup
}

// MarkHITLSeen flags this turn as HITL and drops any already-accumulated text.
func (h *ModelEventHandler) MarkHITLSeen() {
	h.accumulatedMu.Lock()
	h.hitlSeen = true
	h.accumulatedChunks = ""
	h.accumulatedMu.Unlock()
}

func (h *ModelEventHandler) HITLSeen() bool {
	h.accumulatedMu.Lock()
	defer h.accumulatedMu.Unlock()
	return h.hitlSeen
}

// NewModelEventHandler creates a new ModelEventHandler.
func NewModelEventHandler(
	emitter *EventEmitter,
	counter *StepCounter,
	extractor *agents.ReasoningExtractor,
	chunkCb func(chunk string) error,
	tokenAcc *TokenAccumulator,
	activity *ActivityClock,
) *ModelEventHandler {
	return &ModelEventHandler{
		emitter:   emitter,
		counter:   counter,
		extractor: extractor,
		chunkCb:   chunkCb,
		tokenAcc:  tokenAcc,
		activity:  activity,
	}
}

// OnModelEnd handles non-streaming model output.
// In non-streaming mode, reasoning and content arrive as complete values (not chunks).
func (h *ModelEventHandler) OnModelEnd(ctx context.Context, info *callbacks.RunInfo, output *model.CallbackOutput) context.Context {
	h.activity.Touch()
	if output == nil || output.Message == nil {
		return ctx
	}

	// Accumulate token usage from this model call
	if output.TokenUsage != nil && h.tokenAcc != nil {
		h.tokenAcc.Add(output.TokenUsage)
	}

	msg := output.Message

	// Emit reasoning if present (non-streaming: comes as complete content)
	if reasoning, found := h.extractor.ExtractReasoning(msg); found {
		event := &domain.AgentEvent{
			Type:       domain.EventTypeReasoning,
			Timestamp:  time.Now(),
			Step:       h.counter.GetStep(),
			Content:    reasoning,
			Metadata:   make(map[string]interface{}),
			IsComplete: true,
		}
		h.emitter.Emit(ctx, event)
	}

	// Increment model call count
	h.counter.IncrementModelCallCount()

	// Store assistant content for the first upcoming tool call (onToolStart will attach it).
	// Do NOT emit EventTypeToolCall here — onToolStart already emits it.
	// Emitting here creates duplicates that corrupt toolMessageCollector's FIFO queue,
	// causing wrong tool results to be paired with wrong tool calls in DB history.
	// (Same approach as onModelEndWithStreamOutput which also skips tool call emission)
	if msg.Content != "" && len(msg.ToolCalls) > 0 {
		h.counter.SetPendingAssistantContent(msg.Content)
	}

	// Emit text content as AnswerChunk so it's visible to client (non-streaming mode).
	// In streaming mode, chunks are sent via chunkCb. In non-streaming mode,
	// content arrives complete — emit it as a single AnswerChunk event.
	// Only emit if there are NO tool calls (pure text answer), to avoid emitting
	// "thinking before acting" text that will be recovery-injected later.
	if msg.Content != "" && len(msg.ToolCalls) == 0 {
		h.emitter.Emit(ctx, &domain.AgentEvent{
			Type:      domain.EventTypeAnswerChunk,
			Timestamp: time.Now(),
			Step:      h.counter.GetStep(),
			Content:   msg.Content,
		})
	}

	return ctx
}

// OnModelEndWithStreamOutput handles streaming model output.
func (h *ModelEventHandler) OnModelEndWithStreamOutput(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[*model.CallbackOutput]) context.Context {
	// Capture modelCallIdx before goroutine to match message_modifier's assistantStepIdx
	modelCallIdx := h.counter.GetModelCallCount()
	h.counter.IncrementModelCallCount()
	slog.InfoContext(ctx, "[CALLBACK] onModelEndWithStreamOutput called, starting goroutine", "step", h.counter.GetStep(), "model_call_idx", modelCallIdx)
	h.streamWg.Add(1)
	go func() {
		slog.InfoContext(ctx, "[CALLBACK] goroutine started, will process stream frames")
		defer func() {
			slog.InfoContext(ctx, "[CALLBACK] goroutine defer: closing output stream + signaling done")
			output.Close()
			h.streamWg.Done()
		}()

		var streamErr error
		frameCount := 0
		hasToolCalls := false // Track if tool calls were detected

		for {
			// Stop promptly once the run context is cancelled (budget/watchdog/
			// client). eino's stream is not safe to Close from another goroutine,
			// so we cannot interrupt a blocked Recv directly — a hung Recv is
			// bounded instead by the turn deadline (transport honours ctx) and by
			// the caller's bounded WaitStreamDone, which keeps the request handler
			// from wedging even if this goroutine is parked.
			if ctx.Err() != nil {
				slog.InfoContext(ctx, "[CALLBACK] context cancelled, stopping stream goroutine", "frame_count", frameCount)
				break
			}
			slog.DebugContext(ctx, "[CALLBACK] waiting for frame.Recv...", "frame_count", frameCount)
			frame, err := output.Recv()
			frameCount++
			if err != nil {
				if err == io.EOF {
					slog.InfoContext(ctx, "[CALLBACK] stream EOF reached", "frame_count", frameCount)
					break
				}
				streamErr = err
				slog.ErrorContext(ctx, "[CALLBACK] error reading stream in callback", "error", err, "error_type", fmt.Sprintf("%T", err), "frame_count", frameCount)
				break
			}

			// A received frame is live activity — keep the step watchdog at bay
			// so a legitimately long stream is never mistaken for a hung step.
			h.activity.Touch()

			if frame == nil || frame.Message == nil {
				slog.DebugContext(ctx, "[CALLBACK] received nil frame or message, skipping", "frame_count", frameCount)
				continue
			}

			// Accumulate token usage from streaming frame (typically only the last frame has usage)
			if frame.TokenUsage != nil && h.tokenAcc != nil {
				h.tokenAcc.Add(frame.TokenUsage)
			}

			msg := frame.Message
			slog.DebugContext(ctx, "[CALLBACK] processing frame", "frame_count", frameCount, "has_content", msg.Content != "", "tool_calls", len(msg.ToolCalls))

			// Extract and emit reasoning with accumulated content (streaming mode)
			// Client expects full accumulated content, not just the current chunk
			if reasoning, found := h.extractor.ExtractReasoning(msg); found {
				h.reasoningMu.Lock()
				h.accumulatedReasoning += reasoning
				accumulated := h.accumulatedReasoning
				h.reasoningMu.Unlock()

				event := &domain.AgentEvent{
					Type:       domain.EventTypeReasoning,
					Timestamp:  time.Now(),
					Step:       h.counter.GetStep(),
					Content:    accumulated, // Send accumulated content, not just chunk
					Metadata:   make(map[string]interface{}),
					IsComplete: false, // Still streaming
				}
				h.emitter.Emit(ctx, event)
			}

			// Send answer content chunks via callback (streaming mode)
			// NOTE: In streaming mode, we only send chunks via chunkCallback, not EventTypeAnswer events
			// EventTypeAnswer is used only in non-streaming mode for complete responses
			if msg.Content != "" {
				if h.chunkCb != nil {
					// If this is the first answer chunk and we have accumulated reasoning,
					// finalize reasoning first so client knows reasoning is complete
					if !h.answerStarted {
						h.answerStarted = true
						h.reasoningMu.Lock()
						if h.accumulatedReasoning != "" {
							event := &domain.AgentEvent{
								Type:       domain.EventTypeReasoning,
								Timestamp:  time.Now(),
								Step:       h.counter.GetStep(),
								Content:    h.accumulatedReasoning,
								Metadata:   make(map[string]interface{}),
								IsComplete: true, // Finalize reasoning before answer starts
							}
							h.emitter.Emit(ctx, event)
							h.accumulatedReasoning = "" // Clear so we don't send again at end
						}
						h.reasoningMu.Unlock()
					}

					// Track accumulated text for finalizing before tool calls
					h.accumulatedMu.Lock()
					h.accumulatedChunks += msg.Content
					h.chunksStreamed = true
					h.accumulatedMu.Unlock()

					if err := h.chunkCb(msg.Content); err != nil {
						slog.ErrorContext(ctx, "failed to send answer chunk", "error", err)
					}
				}
			}

			// Handle tool calls
			// NOTE: We don't emit EventTypeToolCall here because:
			// - For proxied tools (read_file, search_code): proxy sends TOOL_CALL directly
			// - For server tools (manage_plan): onToolStart will emit EventTypeToolCall
			// Emitting here would create duplicates with wrong step numbers
			if len(msg.ToolCalls) > 0 {
				hasToolCalls = true // Mark that we detected tool calls
				slog.DebugContext(ctx, "[CALLBACK] tool calls detected in stream, will be handled by onToolStart",
					"tool_count", len(msg.ToolCalls),
					"step", h.counter.GetStep())
			}
		}

		slog.InfoContext(ctx, "[CALLBACK] stream loop ended, finalizing", "frame_count", frameCount, "has_error", streamErr != nil)

		// Emit final reasoning event with IsComplete=true if we accumulated any reasoning
		// (only if not already sent when answer started)
		h.reasoningMu.Lock()
		finalReasoning := h.accumulatedReasoning
		h.accumulatedReasoning = "" // Reset for next streaming session
		h.reasoningMu.Unlock()

		if finalReasoning != "" {
			slog.InfoContext(ctx, "[CALLBACK] emitting final reasoning event", "length", len(finalReasoning))
			event := &domain.AgentEvent{
				Type:       domain.EventTypeReasoning,
				Timestamp:  time.Now(),
				Step:       h.counter.GetStep(),
				Content:    finalReasoning,
				Metadata:   make(map[string]interface{}),
				IsComplete: true, // Streaming complete
			}
			h.emitter.Emit(ctx, event)
		}

		// Reset answerStarted for next streaming session
		h.answerStarted = false

		// Increment step only if no error occurred AND no tool calls detected
		// Tool execution will handle step increment in onToolEnd
		if streamErr != nil {
			slog.WarnContext(ctx, "[CALLBACK] NOT incrementing step due to error", "error", streamErr)
		} else if hasToolCalls {
			slog.InfoContext(ctx, "[CALLBACK] NOT incrementing step - tool calls detected, onToolEnd will handle it")
		} else {
			slog.InfoContext(ctx, "[CALLBACK] incrementing step (no error, no tool calls)")
			if err := h.counter.IncrementStep(ctx); err != nil {
				slog.WarnContext(ctx, "[CALLBACK] model end: step quota exceeded", "error", err)
			}
		}

		slog.InfoContext(ctx, "[CALLBACK] goroutine completed", "final_step", h.counter.GetStep())
	}()

	slog.InfoContext(ctx, "[CALLBACK] onModelEndWithStreamOutput returning (goroutine running in background)")
	return ctx
}

// FinalizeAccumulatedText emits EventTypeAnswer to complete any streamed text.
// Called before tool calls (from onToolStart) so the text appears in chat history before prompts.
// Also called after streaming completes (from agent) to finalize pure text answers.
// IsComplete is always false here -- the flow_handler sends the final IsFinal=true signal
// when the agent exits. This prevents premature client disconnect during forced continuations.
func (h *ModelEventHandler) FinalizeAccumulatedText(ctx context.Context) {
	h.accumulatedMu.Lock()
	accumulated := h.accumulatedChunks
	alreadyStreamed := h.chunksStreamed
	hitl := h.hitlSeen
	h.accumulatedChunks = ""
	h.chunksStreamed = false
	h.accumulatedMu.Unlock()

	if accumulated == "" {
		return
	}

	// Defense-in-depth for HITL turns — drop fabricated prose.
	if hitl {
		slog.InfoContext(ctx, "[CALLBACK] suppressing accumulated text on HITL turn",
			"dropped_length", len(accumulated))
		return
	}

	slog.InfoContext(ctx, "[CALLBACK] finalizing accumulated text", "length", len(accumulated), "already_streamed", alreadyStreamed)

	metadata := make(map[string]interface{})
	// If chunks were already streamed via chunkCb (message_delta SSE events),
	// mark the event so SSE/WS delivery skips it (client already has the text).
	// The event is still emitted for MessageCollector (history persistence).
	if alreadyStreamed {
		metadata["already_streamed"] = true
	}

	event := &domain.AgentEvent{
		Type:       domain.EventTypeAnswer,
		Timestamp:  time.Now(),
		Step:       h.counter.GetStep(),
		Content:    accumulated,
		IsComplete: false,
		Metadata:   metadata,
	}
	h.emitter.Emit(ctx, event)
}
