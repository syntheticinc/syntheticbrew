package domain

import (
	"encoding/json"
	"fmt"
	"time"
)

// MessageType represents the type of runtime event in the session timeline.
// Named MessageType (not EventType) to avoid collision with AgentEventType in agent_event.go.
type MessageType string

const (
	MessageTypeUserMessage      MessageType = "user_message"
	MessageTypeAssistantMessage MessageType = "assistant_message"
	MessageTypeToolCall         MessageType = "tool_call"
	MessageTypeToolResult       MessageType = "tool_result"
	MessageTypeReasoning        MessageType = "reasoning"
	MessageTypeSystem           MessageType = "system"
	// HITL Interrupt Primitive — engine 1.2.0.
	MessageTypeInterruptRequest MessageType = "interrupt_request"
	MessageTypeInterruptResume  MessageType = "interrupt_resume"
)

// ToolCallInfo represents a tool call made by the assistant (used in payloads).
type ToolCallInfo struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments"`
}

// Message represents a runtime event in a conversation session.
// All event types (user messages, assistant messages, tool calls, tool results,
// reasoning) are stored as peers in chronological order.
type Message struct {
	ID        string
	SessionID string
	Type      MessageType
	AgentID   string
	CallID    string          // Links tool_call ↔ tool_result events
	Payload   json.RawMessage // Type-specific data (content, tool args, etc.)
	CreatedAt time.Time
}

// Payload structures for each event type.

// ContentPayload is used by user_message, assistant_message, reasoning, system.
type ContentPayload struct {
	Content string `json:"content"`
}

// ToolCallPayload is used by tool_call events.
type ToolCallPayload struct {
	Tool      string            `json:"tool"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

// ToolResultPayload is used by tool_result events.
// IsError mirrors AgentEvent.Error so reloaded history distinguishes failed
// tool calls from successful ones.
type ToolResultPayload struct {
	Tool    string `json:"tool"`
	Content string `json:"content"`
	IsError bool   `json:"is_error,omitempty"`
}

// --- Constructors ---

// NewUserMessageEvent creates a user message event.
func NewUserMessageEvent(sessionID, content string) (*Message, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if content == "" {
		return nil, fmt.Errorf("content is required for user message")
	}
	payload, _ := json.Marshal(ContentPayload{Content: content})
	return &Message{
		SessionID: sessionID,
		Type:      MessageTypeUserMessage,
		Payload:   payload,
		CreatedAt: time.Now(),
	}, nil
}

// NewAssistantEvent creates an assistant message event.
func NewAssistantEvent(sessionID, content string) (*Message, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	payload, _ := json.Marshal(ContentPayload{Content: content})
	return &Message{
		SessionID: sessionID,
		Type:      MessageTypeAssistantMessage,
		Payload:   payload,
		CreatedAt: time.Now(),
	}, nil
}

// NewToolCallEvent creates a tool call event.
func NewToolCallEvent(sessionID, callID, toolName string, arguments map[string]string) (*Message, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if callID == "" {
		return nil, fmt.Errorf("call_id is required for tool call")
	}
	if toolName == "" {
		return nil, fmt.Errorf("tool name is required for tool call")
	}
	payload, _ := json.Marshal(ToolCallPayload{Tool: toolName, Arguments: arguments})
	return &Message{
		SessionID: sessionID,
		Type:      MessageTypeToolCall,
		CallID:    callID,
		Payload:   payload,
		CreatedAt: time.Now(),
	}, nil
}

// NewToolResultEvent creates a tool result event.
func NewToolResultEvent(sessionID, callID, toolName, content string, isError bool) (*Message, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if callID == "" {
		return nil, fmt.Errorf("call_id is required for tool result")
	}
	payload, _ := json.Marshal(ToolResultPayload{Tool: toolName, Content: content, IsError: isError})
	return &Message{
		SessionID: sessionID,
		Type:      MessageTypeToolResult,
		CallID:    callID,
		Payload:   payload,
		CreatedAt: time.Now(),
	}, nil
}

// NewReasoningEvent creates a reasoning event.
func NewReasoningEvent(sessionID, content string) (*Message, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	payload, _ := json.Marshal(ContentPayload{Content: content})
	return &Message{
		SessionID: sessionID,
		Type:      MessageTypeReasoning,
		Payload:   payload,
		CreatedAt: time.Now(),
	}, nil
}

// NewSystemEvent creates a system message event.
func NewSystemEvent(sessionID, content string) (*Message, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	payload, _ := json.Marshal(ContentPayload{Content: content})
	return &Message{
		SessionID: sessionID,
		Type:      MessageTypeSystem,
		Payload:   payload,
		CreatedAt: time.Now(),
	}, nil
}

// NewInterruptRequestMessage persists a HITL interrupt_request to chat
// history so reload can re-render the widget. Content is the raw
// InterruptRequestPayload JSON.
func NewInterruptRequestMessage(sessionID, interruptID, content string) (*Message, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	return &Message{
		SessionID: sessionID,
		Type:      MessageTypeInterruptRequest,
		CallID:    interruptID,
		Payload:   json.RawMessage(`{"content":` + jsonString(content) + `}`),
		CreatedAt: time.Now(),
	}, nil
}

// NewInterruptResumeMessage persists a HITL interrupt_resume. On reload the
// client matches it to its interrupt_request peer by call_id and marks the
// widget answered.
func NewInterruptResumeMessage(sessionID, interruptID, content string) (*Message, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	return &Message{
		SessionID: sessionID,
		Type:      MessageTypeInterruptResume,
		CallID:    interruptID,
		Payload:   json.RawMessage(`{"content":` + jsonString(content) + `}`),
		CreatedAt: time.Now(),
	}, nil
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// --- Payload accessors ---

// GetContent extracts content from payload (for user_message, assistant_message, reasoning, system).
func (m *Message) GetContent() string {
	var p ContentPayload
	if json.Unmarshal(m.Payload, &p) == nil {
		return p.Content
	}
	return ""
}

// GetToolCallPayload extracts tool call data from payload.
func (m *Message) GetToolCallPayload() (ToolCallPayload, bool) {
	var p ToolCallPayload
	if err := json.Unmarshal(m.Payload, &p); err != nil {
		return p, false
	}
	return p, p.Tool != ""
}

// GetToolResultPayload extracts tool result data from payload.
func (m *Message) GetToolResultPayload() (ToolResultPayload, bool) {
	var p ToolResultPayload
	if err := json.Unmarshal(m.Payload, &p); err != nil {
		return p, false
	}
	return p, true
}

// --- History conversion (for Eino schema.Message compatibility) ---

// HistoryMessage represents a simplified event for conversation history.
type HistoryMessage struct {
	Role    string          // "user", "assistant", "tool", "system"
	Content string          // Text content (for messages, reasoning, tool results)
	CallID  string          // Links tool_call ↔ tool_result
	Payload json.RawMessage // Raw payload for type-specific data
	AgentID string
}

// ToHistoryMessage converts a Message to HistoryMessage.
func (m *Message) ToHistoryMessage() HistoryMessage {
	hm := HistoryMessage{
		CallID:  m.CallID,
		Payload: m.Payload,
		AgentID: m.AgentID,
	}

	switch m.Type {
	case MessageTypeUserMessage:
		hm.Role = "user"
		hm.Content = m.GetContent()
	case MessageTypeAssistantMessage:
		hm.Role = "assistant"
		hm.Content = m.GetContent()
	case MessageTypeToolCall:
		hm.Role = "assistant"
		// Content stays empty; payload has tool details
	case MessageTypeToolResult:
		hm.Role = "tool"
		hm.Content = m.GetContent()
	case MessageTypeReasoning:
		hm.Role = "assistant"
		hm.Content = m.GetContent()
	case MessageTypeSystem:
		hm.Role = "system"
		hm.Content = m.GetContent()
	default:
		hm.Role = "user"
	}

	return hm
}
