package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/cloudwego/eino/schema"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools"
)

// MessageCollector accumulates schema.Message from AgentEvents and saves them to history
type MessageCollector struct {
	sessionID   string
	agentID     string
	historyRepo HistoryRepository
	// writeCtx carries tenant_id (and other request-scoped values) into DB
	// writes triggered by the eino callback chain. handleEvent runs from a
	// callback signature without ctx (`func(*domain.AgentEvent) error`), so
	// without this stored ctx writes fall back to CETenantID and multi-tenant users
	// lose their assistant/tool/reasoning rows on reload.
	writeCtx    context.Context
	messages    []*schema.Message
	stepCount   int
	mu          sync.Mutex

	// Track pending tool call for pairing with result
	pendingToolCall *pendingToolCallInfo

	// accumulatedAnswer holds text streamed via EventTypeAnswerChunk between tool
	// calls / final answer. The streaming-finalize path sometimes emits
	// EventTypeAnswer with empty content (the callback layer already drained
	// accumulated text earlier); in that case handleAnswer falls back to this
	// buffer so the final assistant row is never lost from history.
	accumulatedAnswer string
}

type pendingToolCallInfo struct {
	toolCallID string
	toolName   string
}

// NewMessageCollector creates a new MessageCollector. ctx must carry the
// tenant_id that owns the session — DB writes from the eino callback chain
// inherit it via writeCtx. context.WithoutCancel is applied so the writes
// continue if the original request ctx is cancelled mid-turn.
func NewMessageCollector(ctx context.Context, sessionID, agentID string, historyRepo HistoryRepository) *MessageCollector {
	return &MessageCollector{
		writeCtx:    context.WithoutCancel(ctx),
		sessionID:   sessionID,
		agentID:     agentID,
		historyRepo: historyRepo,
		messages:    make([]*schema.Message, 0),
	}
}

// WrapEventCallback returns a wrapper that intercepts events to collect messages
func (mc *MessageCollector) WrapEventCallback(original func(*domain.AgentEvent) error) func(*domain.AgentEvent) error {
	return func(event *domain.AgentEvent) error {
		// Collect messages from events
		mc.handleEvent(event)

		// Pass through to original callback
		if original != nil {
			return original(event)
		}
		return nil
	}
}

// CollectUserMessage persists the user's input message to history.
// Called before agent execution so user messages appear in session history on reload.
func (mc *MessageCollector) CollectUserMessage(ctx context.Context, content string) {
	if content == "" {
		return
	}

	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.messages = append(mc.messages, &schema.Message{
		Role:    schema.User,
		Content: content,
	})

	histMsg, err := domain.NewUserMessageEvent(mc.sessionID, content)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create user event", "error", err)
		return
	}
	histMsg.AgentID = mc.agentID

	if mc.historyRepo != nil {
		if err := mc.historyRepo.Create(ctx, histMsg); err != nil {
			slog.ErrorContext(ctx, "failed to save user message", "error", err)
		}
	}

	slog.InfoContext(ctx, "collected user message", "length", len(content), "agent_id", mc.agentID)
}

// RecordResumeAnswer appends the user's HITL resume answer to the in-memory
// transcript so it lands in the snapshot the NEXT turn loads. Called on resume
// turns INSTEAD of CollectUserMessage: unlike a normal user turn it does NOT write
// a messages-table row — the answer is already mirrored there as an
// interrupt_resume row (chat_http_adapter) and the widget's answered state
// represents it for the UI. Without this the answer lives only in the live resume
// turn's input and vanishes from the agent's context next turn, so the model loses
// the submitted value (device name / DevEUI / AppKey in a multi-step widget flow)
// and reconstructs it from earlier examples.
func (mc *MessageCollector) RecordResumeAnswer(content string) {
	if content == "" {
		return
	}

	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.messages = append(mc.messages, &schema.Message{
		Role:    schema.User,
		Content: content,
	})
}

// handleEvent processes events to extract and save messages
func (mc *MessageCollector) handleEvent(event *domain.AgentEvent) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	ctx := mc.writeCtx
	if ctx == nil {
		ctx = context.Background()
	}

	switch event.Type {
	case domain.EventTypeToolCall:
		mc.handleToolCall(ctx, event)
	case domain.EventTypeToolResult:
		mc.handleToolResult(ctx, event)
	case domain.EventTypeAnswer:
		mc.handleAnswer(ctx, event)
	case domain.EventTypeReasoning:
		mc.handleReasoning(ctx, event)
	case domain.EventTypeInterruptRequest:
		mc.handleInterruptEvent(ctx, event, true)
	case domain.EventTypeInterruptResume:
		mc.handleInterruptEvent(ctx, event, false)
	case domain.EventTypeAnswerChunk:
		// Track streaming chunks so handleAnswer can recover the full text
		// when the callback layer emits EventTypeAnswer with empty content
		// (streaming-finalize path).
		if event.Content != "" {
			mc.accumulatedAnswer += event.Content
		}
	}
}

// handleToolCall creates assistant message with tool call
func (mc *MessageCollector) handleToolCall(ctx context.Context, event *domain.AgentEvent) {
	// Any text streamed before this tool call is already captured via the
	// tool_call event's assistant_content metadata. Drop our fallback buffer
	// so it doesn't leak into a later EventTypeAnswer for the final turn.
	mc.accumulatedAnswer = ""

	metadata := event.Metadata
	if metadata == nil {
		slog.WarnContext(ctx, "tool_call event without metadata")
		return
	}

	toolCallID, _ := metadata["id"].(string)
	toolName, _ := metadata["tool_name"].(string)
	argsJSON, _ := metadata["function_arguments"].(string)
	assistantContent, _ := metadata["assistant_content"].(string)

	if toolCallID == "" || toolName == "" {
		slog.WarnContext(ctx, "tool_call event missing required fields",
			"id", toolCallID, "name", toolName)
		return
	}

	// Parse arguments JSON to map for domain.ToolCallInfo
	var argsMap map[string]string
	if argsJSON != "" {
		var rawArgs map[string]interface{}
		if err := json.Unmarshal([]byte(argsJSON), &rawArgs); err == nil {
			argsMap = make(map[string]string)
			for k, v := range rawArgs {
				argsMap[k] = fmt.Sprintf("%v", v)
			}
		}
	}

	// Create assistant message with tool call
	msg := &schema.Message{
		Role:    schema.Assistant,
		Content: assistantContent,
		ToolCalls: []schema.ToolCall{{
			ID: toolCallID,
			Function: schema.FunctionCall{
				Name:      toolName,
				Arguments: argsJSON,
			},
		}},
	}

	mc.messages = append(mc.messages, msg)

	// Save assistant text if present (as separate event before tool call)
	if assistantContent != "" {
		if aMsg, err := domain.NewAssistantEvent(mc.sessionID, assistantContent); err == nil {
			aMsg.AgentID = mc.agentID
			if mc.historyRepo != nil {
				if err := mc.historyRepo.Create(ctx, aMsg); err != nil {
					slog.ErrorContext(ctx, "failed to save assistant event", "error", err)
				}
			}
		}
	}

	// Save tool call event
	histMsg, err := domain.NewToolCallEvent(mc.sessionID, toolCallID, toolName, argsMap)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create tool call event", "error", err)
		return
	}
	histMsg.AgentID = mc.agentID

	if mc.historyRepo != nil {
		if err := mc.historyRepo.Create(ctx, histMsg); err != nil {
			slog.ErrorContext(ctx, "failed to save tool call event", "error", err)
		}
	}

	// Track pending tool call for result pairing
	mc.pendingToolCall = &pendingToolCallInfo{
		toolCallID: toolCallID,
		toolName:   toolName,
	}

	slog.InfoContext(ctx, "collected tool call message",
		"tool_name", toolName, "id", toolCallID, "agent_id", mc.agentID)
}

// handleToolResult creates tool message
func (mc *MessageCollector) handleToolResult(ctx context.Context, event *domain.AgentEvent) {
	metadata := event.Metadata
	if metadata == nil {
		slog.WarnContext(ctx, "tool_result event without metadata")
		return
	}

	toolName, _ := metadata["tool_name"].(string)
	fullResult, _ := metadata["full_result"].(string)

	// Use full_result if available, otherwise use event.Content
	content := fullResult
	if content == "" {
		content = event.Content
	}

	// Get tool call ID from pending call
	toolCallID := ""
	if mc.pendingToolCall != nil && mc.pendingToolCall.toolName == toolName {
		toolCallID = mc.pendingToolCall.toolCallID
		mc.pendingToolCall = nil // Clear pending
	} else {
		// Fallback: generate ID if not found
		toolCallID = fmt.Sprintf("server-%s-%d", toolName, mc.stepCount)
		slog.WarnContext(ctx, "no pending tool call found, using fallback ID",
			"tool_name", toolName, "id", toolCallID)
	}

	// Wrap content with prompt-injection markers ONLY for the LLM-bound message.
	// Raw content flows to history / SSE / audit. This is the single chokepoint
	// for marker application — tool callbacks, event_stream, chat_http_adapter
	// all see raw content.
	riskLevel := tools.GetContentRiskLevel(toolName)
	llmContent := wrapContentForLLMContext(toolName, content, riskLevel)

	// Create tool message for next LLM iteration (wrapped to protect against
	// prompt injection in untrusted tool output)
	msg := &schema.Message{
		Role:       schema.Tool,
		Content:    llmContent,
		ToolCallID: toolCallID,
		Name:       toolName,
	}

	mc.messages = append(mc.messages, msg)

	// Persist raw content to history — clients reading the event log get the
	// untouched tool output, not the LLM-protection wrapper.
	histMsg, err := domain.NewToolResultEvent(mc.sessionID, toolCallID, toolName, content, event.Error != nil)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create tool result event", "error", err)
		return
	}
	histMsg.AgentID = mc.agentID

	if mc.historyRepo != nil {
		if err := mc.historyRepo.Create(ctx, histMsg); err != nil {
			slog.ErrorContext(ctx, "failed to save tool result event", "error", err)
		}
	}

	// Increment step after tool result
	mc.stepCount++

	slog.InfoContext(ctx, "collected tool result message",
		"tool_name", toolName, "step", mc.stepCount, "agent_id", mc.agentID)
}

// handleAnswer creates final assistant message.
// Falls back to accumulated streaming chunks when event.Content is empty —
// this is the streaming-finalize path where the agent callback layer drains
// the accumulated buffer before emitting EventTypeAnswer. Without the
// fallback the final assistant row would be lost and disappear on reload.
func (mc *MessageCollector) handleAnswer(ctx context.Context, event *domain.AgentEvent) {
	content := event.Content
	if content == "" {
		content = mc.accumulatedAnswer
	}
	// Reset accumulator: any subsequent turn starts fresh.
	mc.accumulatedAnswer = ""

	if content == "" {
		return
	}

	// Create assistant message
	msg := &schema.Message{
		Role:    schema.Assistant,
		Content: content,
	}

	mc.messages = append(mc.messages, msg)

	// Save assistant message event
	histMsg, err := domain.NewAssistantEvent(mc.sessionID, content)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create assistant event", "error", err)
		return
	}
	histMsg.AgentID = mc.agentID

	if mc.historyRepo != nil {
		if err := mc.historyRepo.Create(ctx, histMsg); err != nil {
			slog.ErrorContext(ctx, "failed to save assistant event", "error", err)
		}
	}

	slog.InfoContext(ctx, "collected answer event",
		"length", len(content), "agent_id", mc.agentID,
		"content_fallback", event.Content == "")
}

// handleInterruptEvent persists HITL interrupt_request / interrupt_resume into
// the messages table so reload re-renders the widget. Content already carries
// the wire-shape payload JSON.
func (mc *MessageCollector) handleInterruptEvent(ctx context.Context, event *domain.AgentEvent, isRequest bool) {
	interruptID, _ := event.Metadata["interrupt_id"].(string)
	if interruptID == "" && event.Metadata != nil {
		// Resume path doesn't always populate Metadata when injected from
		// chat_http_adapter; fall back to inline payload decode.
		var probe struct {
			InterruptID string `json:"interrupt_id"`
		}
		_ = json.Unmarshal([]byte(event.Content), &probe)
		interruptID = probe.InterruptID
	}

	var histMsg *domain.Message
	var err error
	if isRequest {
		histMsg, err = domain.NewInterruptRequestMessage(mc.sessionID, interruptID, event.Content)
	} else {
		histMsg, err = domain.NewInterruptResumeMessage(mc.sessionID, interruptID, event.Content)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to build interrupt history message", "error", err, "is_request", isRequest)
		return
	}
	histMsg.AgentID = mc.agentID

	if mc.historyRepo != nil {
		if err := mc.historyRepo.Create(ctx, histMsg); err != nil {
			slog.ErrorContext(ctx, "failed to save interrupt history message", "error", err, "is_request", isRequest)
		}
	}
}

// handleReasoning creates reasoning event (previously not persisted).
// Skips intermediate streaming chunks — the callback emits one reasoning
// event per accumulated chunk plus a final IsComplete=true event; we keep
// only the final aggregated row so the messages table doesn't balloon with
// dozens of partial snapshots per turn.
func (mc *MessageCollector) handleReasoning(ctx context.Context, event *domain.AgentEvent) {
	if event.Content == "" {
		return
	}
	if !event.IsComplete {
		return
	}

	histMsg, err := domain.NewReasoningEvent(mc.sessionID, event.Content)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create reasoning event", "error", err)
		return
	}
	histMsg.AgentID = mc.agentID

	if mc.historyRepo != nil {
		if err := mc.historyRepo.Create(ctx, histMsg); err != nil {
			slog.ErrorContext(ctx, "failed to save reasoning event", "error", err)
		}
	}

	slog.InfoContext(ctx, "collected reasoning event",
		"length", len(event.Content), "agent_id", mc.agentID)
}

// GetAccumulatedMessages returns all collected messages (thread-safe)
func (mc *MessageCollector) GetAccumulatedMessages() []*schema.Message {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	// Return copy to prevent concurrent modification
	result := make([]*schema.Message, len(mc.messages))
	copy(result, mc.messages)
	return result
}

// StepCount returns the current step count (thread-safe)
func (mc *MessageCollector) StepCount() int {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	return mc.stepCount
}

