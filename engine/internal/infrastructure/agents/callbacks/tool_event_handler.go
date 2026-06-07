package callbacks

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/callbacks"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools"
)

// defaultMaxConsecutiveToolErrors is how many times a single tool may return an
// [ERROR] result in a row before the agent loop is force-stopped. Without this
// hard cap a model that ignores the advisory loop-warning can call the same
// failing tool until MaxSteps (thousands), hanging the turn.
const defaultMaxConsecutiveToolErrors = 4

// HITLAware lets the tool handler flag a HITL turn on the model handler.
type HITLAware interface {
	MarkHITLSeen()
}

// ToolCallRecorder defines interface for recording tool calls and results.
// Consumer-side interface: defined here where it's used.
type ToolCallRecorder interface {
	RecordToolCall(sessionID, toolName string)
	RecordToolResult(sessionID, toolName, result string)
}

// ToolEventHandler handles tool start/end callbacks.
type ToolEventHandler struct {
	emitter      *EventEmitter
	counter      *StepCounter
	model        *ModelEventHandler
	recorder     ToolCallRecorder
	sessionID    string
	errThreshold int
	abortLoop    context.CancelFunc

	mu           sync.Mutex
	consecErr    map[string]int // per-tool consecutive [ERROR] count, this turn
	aborted      bool
	abortTool    string
	abortLastErr string
}

// NewToolEventHandler creates a new ToolEventHandler. abortLoop cancels the
// context the react loop runs under; it is called by the error-loop breaker to
// halt the loop and may be nil (breaker still records the abort, but cannot
// force-stop Eino — used in unit tests).
func NewToolEventHandler(
	emitter *EventEmitter,
	counter *StepCounter,
	model *ModelEventHandler,
	recorder ToolCallRecorder,
	sessionID string,
	abortLoop context.CancelFunc,
) *ToolEventHandler {
	return &ToolEventHandler{
		emitter:      emitter,
		counter:      counter,
		model:        model,
		recorder:     recorder,
		sessionID:    sessionID,
		errThreshold: defaultMaxConsecutiveToolErrors,
		abortLoop:    abortLoop,
		consecErr:    make(map[string]int),
	}
}

// tripBreakerIfLooping force-stops the turn when toolName has returned an
// [ERROR] result errThreshold times in a row. The consecutive-error count is
// tracked locally (the handler is per-turn) rather than via the recorder, whose
// concrete type is wrapped by adapters before it reaches this layer. A success
// resets the tool's streak. On trip it cancels the loop context via abortLoop so
// Eino actually stops (a per-callback child cancel does not). Returns whether
// the breaker tripped.
func (h *ToolEventHandler) tripBreakerIfLooping(ctx context.Context, toolName, result string) bool {
	h.mu.Lock()
	if !strings.HasPrefix(result, "[ERROR]") {
		h.consecErr[toolName] = 0
		h.mu.Unlock()
		return false
	}
	h.consecErr[toolName]++
	if h.consecErr[toolName] < h.errThreshold {
		h.mu.Unlock()
		return false
	}
	firstTrip := !h.aborted
	if firstTrip {
		h.aborted = true
		h.abortTool = toolName
		h.abortLastErr = result
	}
	h.mu.Unlock()

	if !firstTrip {
		return true
	}
	slog.WarnContext(ctx, "[CALLBACK] tool error loop detected, force-stopping turn",
		"tool_name", toolName, "threshold", h.errThreshold)
	if h.abortLoop != nil {
		h.abortLoop()
	}
	return true
}

// Aborted reports whether the error-loop breaker tripped, with the tool name and
// last error so the agent loop can emit a clear final message to the user.
func (h *ToolEventHandler) Aborted() (tool, lastErr string, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.abortTool, h.abortLastErr, h.aborted
}

// OnToolStart handles tool execution start.
func (h *ToolEventHandler) OnToolStart(ctx context.Context, info *callbacks.RunInfo, input *einotool.CallbackInput) context.Context {
	currentStep := h.counter.GetStep()
	slog.InfoContext(ctx, "[CALLBACK] onToolStart called", "tool_name", info.Name, "step", currentStep)

	// Record tool call for efficiency reminders
	if h.recorder != nil && h.sessionID != "" && info.Name != "" {
		h.recorder.RecordToolCall(h.sessionID, info.Name)
	}

	// Mark HITL BEFORE finalizing so the model handler drops accumulated text.
	isHITL := domain.IsHITLTool(info.Name)
	if isHITL {
		if aware, ok := any(h.model).(HITLAware); ok {
			aware.MarkHITLSeen()
		}
	}

	// Finalize any accumulated text BEFORE tool call so it appears in chat history first
	h.model.FinalizeAccumulatedText(ctx)

	// For HITL tools, emit a retract so SSE clients can scrub already-delivered prose.
	if isHITL {
		h.emitter.Emit(ctx, &domain.AgentEvent{
			Type:      domain.EventTypeRetractAssistant,
			Timestamp: time.Now(),
			Step:      currentStep,
		})
	}

	// Generate call ID for server-side tools
	callID := fmt.Sprintf("server-%s-%d", info.Name, currentStep)

	// Emit ToolCall event for the tool that's about to be executed
	metadata := map[string]interface{}{
		"id":        callID,
		"tool_name": info.Name,
	}

	// Add tool input/arguments if available
	if input != nil && input.ArgumentsInJSON != "" {
		metadata["function_arguments"] = input.ArgumentsInJSON
		slog.InfoContext(ctx, "[CALLBACK] onToolStart: got arguments",
			"tool_name", info.Name,
			"arguments_json", input.ArgumentsInJSON)
	} else {
		slog.WarnContext(ctx, "[CALLBACK] onToolStart: NO arguments",
			"tool_name", info.Name,
			"input_nil", input == nil,
			"args_empty", input != nil && input.ArgumentsInJSON == "")
	}

	// Attach pending assistant content from onModelEnd (only for the first tool call)
	if assistantContent := h.counter.ConsumePendingAssistantContent(); assistantContent != "" {
		metadata["assistant_content"] = assistantContent
		slog.InfoContext(ctx, "[CALLBACK] onToolStart: attached assistant_content",
			"tool_name", info.Name,
			"content_length", len(assistantContent))
	}

	event := &domain.AgentEvent{
		Type:      domain.EventTypeToolCall,
		Timestamp: time.Now(),
		Step:      currentStep,
		Content:   info.Name,
		Metadata:  metadata,
	}

	slog.InfoContext(ctx, "[CALLBACK] emitting ToolCall event",
		"tool_name", info.Name,
		"step", currentStep,
		"call_id", callID)

	h.emitter.Emit(ctx, event)

	slog.InfoContext(ctx, "[CALLBACK] onToolStart completed", "tool_name", info.Name)
	return ctx
}

// OnToolEnd handles tool execution result.
func (h *ToolEventHandler) OnToolEnd(ctx context.Context, info *callbacks.RunInfo, output *einotool.CallbackOutput) context.Context {
	currentStep := h.counter.GetStep()
	slog.InfoContext(ctx, "[CALLBACK] onToolEnd called", "tool_name", info.Name, "step", currentStep)

	if output == nil {
		slog.WarnContext(ctx, "[CALLBACK] onToolEnd: output is nil")
		return ctx
	}

	preview := output.Response
	// Don't truncate smart_search results - client needs full citations
	if len(output.Response) > 500 && info.Name != "smart_search" {
		preview = output.Response[:500] + "..."
	}

	slog.InfoContext(ctx, "[CALLBACK] onToolEnd: emitting ToolResult event",
		"tool_name", info.Name,
		"full_result_length", len(output.Response),
		"preview_length", len(preview))

	// Store full result in metadata for server-side tools
	// agent_event_stream.go will use this for TOOL_RESULT
	metadata := map[string]interface{}{
		"tool_name":     info.Name,
		"result_length": len(output.Response),
		"full_result":   output.Response, // Full result for client display
	}

	// Compute display summary for client
	summary := tools.SummarizeToolResult(info.Name, output.Response)
	if summary != "" {
		metadata["summary"] = summary
	}

	event := &domain.AgentEvent{
		Type:      domain.EventTypeToolResult,
		Timestamp: time.Now(),
		Step:      currentStep,
		Content:   preview,
		Metadata:  metadata,
	}
	if strings.HasPrefix(output.Response, "[ERROR]") {
		event.Error = &domain.AgentError{Code: "tool_error", Message: output.Response}
	}

	slog.InfoContext(ctx, "[CALLBACK] emitting ToolResult event",
		"tool_name", info.Name,
		"step", currentStep,
		"expected_call_id", fmt.Sprintf("server-%s-%d", info.Name, currentStep))

	h.emitter.Emit(ctx, event)

	// Record tool result for error loop detection
	if h.recorder != nil && h.sessionID != "" && info.Name != "" {
		h.recorder.RecordToolResult(h.sessionID, info.Name, output.Response)
	}

	if h.tripBreakerIfLooping(ctx, info.Name, output.Response) {
		return ctx
	}

	// Increment step after tool execution completes
	// This ensures onToolStart and onToolEnd use the same step number for callId
	if err := h.counter.IncrementStep(ctx); err != nil {
		slog.WarnContext(ctx, "[CALLBACK] onToolEnd: step quota exceeded, cancelling context", "tool_name", info.Name, "error", err)
		ctx, cancel := context.WithCancelCause(ctx)
		cancel(err)
		return ctx
	}
	slog.InfoContext(ctx, "[CALLBACK] onToolEnd completed, step incremented", "tool_name", info.Name, "new_step", h.counter.GetStep())
	return ctx
}

// OnToolError handles tool execution errors. Called by Eino when InvokableRun returns a Go error.
//
// As of the [ERROR]-convention migration, MCP application-level errors
// (isError: true) no longer reach here — they are returned as normal
// tool-result content with an "[ERROR] " prefix and surface through
// OnToolEnd instead. This handler now only sees transport-level Go
// errors (network down, MCP server crashed) and Go-error failures from
// any other native tool whose InvokableRun signals a real platform
// problem rather than an application outcome.
func (h *ToolEventHandler) OnToolError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	currentStep := h.counter.GetStep()
	slog.WarnContext(ctx, "[CALLBACK] onToolError called", "tool_name", info.Name, "step", currentStep, "error", err)

	content := err.Error()

	callID := fmt.Sprintf("server-%s-%d", info.Name, currentStep)

	metadata := map[string]interface{}{
		"tool_name":     info.Name,
		"result_length": len(content),
		"full_result":   content,
	}

	summary := tools.SummarizeToolResult(info.Name, content)
	if summary != "" {
		metadata["summary"] = summary
	}

	event := &domain.AgentEvent{
		Type:      domain.EventTypeToolResult,
		Timestamp: time.Now(),
		Step:      currentStep,
		Content:   content,
		Metadata:  metadata,
		Error:     &domain.AgentError{Code: "tool_error", Message: content},
	}

	slog.InfoContext(ctx, "[CALLBACK] emitting ToolResult event (error)",
		"tool_name", info.Name,
		"step", currentStep,
		"call_id", callID)

	h.emitter.Emit(ctx, event)

	if h.recorder != nil && h.sessionID != "" && info.Name != "" {
		h.recorder.RecordToolResult(h.sessionID, info.Name, content)
	}

	if err := h.counter.IncrementStep(ctx); err != nil {
		slog.WarnContext(ctx, "[CALLBACK] onToolError: step quota exceeded, cancelling context", "tool_name", info.Name, "error", err)
		ctx, cancel := context.WithCancelCause(ctx)
		cancel(err)
		return ctx
	}
	slog.InfoContext(ctx, "[CALLBACK] onToolError completed, step incremented", "tool_name", info.Name, "new_step", h.counter.GetStep())
	return ctx
}
