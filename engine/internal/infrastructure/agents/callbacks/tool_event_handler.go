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

// defaultMaxConsecutiveToolErrors caps how many [ERROR] results a single tool
// may return in a row before the loop is force-stopped. Without it a model that
// ignores the advisory warning loops on the failing tool until MaxSteps.
const defaultMaxConsecutiveToolErrors = 4

// defaultMaxIdenticalToolCalls caps how many byte-identical tool calls (same
// name + arguments) may run in a row before the loop is force-stopped. Catches
// the degenerate loop where a model repeats the exact same call — including ones
// that return successful-but-empty results, which the error-loop breaker (which
// only counts [ERROR] results) never sees. Trips on the Nth identical call.
const defaultMaxIdenticalToolCalls = 3

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
	emitter           *EventEmitter
	counter           *StepCounter
	model             *ModelEventHandler
	recorder          ToolCallRecorder
	sessionID         string
	errThreshold      int
	sameArgsThreshold int
	terminal          *TerminalState
	activity          *ActivityClock

	mu            sync.Mutex
	consecErr     map[string]int // per-tool consecutive [ERROR] count, this turn
	lastArgsKey   string         // last (tool, args) call key, this turn
	sameArgsCount int            // consecutive byte-identical (tool, args) calls
}

// NewToolEventHandler creates a new ToolEventHandler. terminal records terminal
// conditions and cancels the react loop; activity is touched on every tool event
// so the step watchdog can detect a hung step. Both must be non-nil.
func NewToolEventHandler(
	emitter *EventEmitter,
	counter *StepCounter,
	model *ModelEventHandler,
	recorder ToolCallRecorder,
	sessionID string,
	terminal *TerminalState,
	activity *ActivityClock,
) *ToolEventHandler {
	return &ToolEventHandler{
		emitter:           emitter,
		counter:           counter,
		model:             model,
		recorder:          recorder,
		sessionID:         sessionID,
		errThreshold:      defaultMaxConsecutiveToolErrors,
		sameArgsThreshold: defaultMaxIdenticalToolCalls,
		terminal:          terminal,
		activity:          activity,
		consecErr:         make(map[string]int),
	}
}

// tripBreakerIfLooping force-stops the turn when toolName returned an [ERROR]
// result errThreshold times in a row; a success resets the streak. Counts are
// tracked locally because the recorder is wrapped by adapters before this layer.
// On trip it records the terminal condition and cancels the loop (a per-callback
// child cancel does not stop Eino). Reports whether the breaker tripped.
func (h *ToolEventHandler) tripBreakerIfLooping(ctx context.Context, toolName, result string) bool {
	h.mu.Lock()
	if !strings.HasPrefix(result, "[ERROR]") {
		h.consecErr[toolName] = 0
		h.mu.Unlock()
		return false
	}
	h.consecErr[toolName]++
	count := h.consecErr[toolName]
	h.mu.Unlock()

	if count < h.errThreshold {
		return false
	}
	if h.terminal.Trip(TerminalToolErrorLoop, toolName, result) {
		slog.WarnContext(ctx, "[CALLBACK] tool error loop detected, force-stopping turn",
			"tool_name", toolName, "threshold", h.errThreshold)
	}
	return true
}

// tripIdenticalArgsIfLooping force-stops the turn when the model issues the same
// tool call (byte-identical name + arguments) sameArgsThreshold times in a row,
// regardless of result content. This catches the degenerate loop that the
// error-loop breaker misses: a tool repeatedly returning successful-but-empty
// results. A call with different arguments (e.g. pagination) resets the streak,
// so legitimate iteration is never affected. Reports whether the breaker tripped.
func (h *ToolEventHandler) tripIdenticalArgsIfLooping(ctx context.Context, toolName, argsJSON string) bool {
	if argsJSON == "" {
		return false // no arguments to compare — cannot identify a repeat
	}
	key := toolName + "\x00" + argsJSON

	h.mu.Lock()
	if key == h.lastArgsKey {
		h.sameArgsCount++
	} else {
		h.lastArgsKey = key
		h.sameArgsCount = 1
	}
	count := h.sameArgsCount
	h.mu.Unlock()

	if count < h.sameArgsThreshold {
		return false
	}
	if h.terminal.Trip(TerminalIdenticalArgsLoop, toolName, "") {
		slog.WarnContext(ctx, "[CALLBACK] identical-args tool loop detected, force-stopping turn",
			"tool_name", toolName, "identical_calls", count, "threshold", h.sameArgsThreshold)
	}
	return true
}

// OnToolStart handles tool execution start.
func (h *ToolEventHandler) OnToolStart(ctx context.Context, info *callbacks.RunInfo, input *einotool.CallbackInput) context.Context {
	h.activity.Touch()
	currentStep := h.counter.GetStep()
	slog.InfoContext(ctx, "[CALLBACK] onToolStart called", "tool_name", info.Name, "step", currentStep)

	// Force-stop a model that repeats the exact same call without progress.
	argsJSON := ""
	if input != nil {
		argsJSON = input.ArgumentsInJSON
	}
	h.tripIdenticalArgsIfLooping(ctx, info.Name, argsJSON)

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
	h.activity.Touch()
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
	h.activity.Touch()
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
