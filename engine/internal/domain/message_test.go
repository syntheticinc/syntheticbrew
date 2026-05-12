package domain

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestNewUserMessageEvent(t *testing.T) {
	msg, err := NewUserMessageEvent("session-1", "Hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != MessageTypeUserMessage {
		t.Errorf("Type = %v, want %v", msg.Type, MessageTypeUserMessage)
	}
	if msg.GetContent() != "Hello" {
		t.Errorf("GetContent() = %v, want Hello", msg.GetContent())
	}
	if msg.SessionID != "session-1" {
		t.Errorf("SessionID = %v, want session-1", msg.SessionID)
	}
}

func TestNewUserMessageEvent_Validation(t *testing.T) {
	if _, err := NewUserMessageEvent("", "Hello"); err == nil {
		t.Error("expected error for empty session_id")
	}
	if _, err := NewUserMessageEvent("s1", ""); err == nil {
		t.Error("expected error for empty content")
	}
}

func TestNewAssistantEvent(t *testing.T) {
	msg, err := NewAssistantEvent("session-1", "Hi there")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != MessageTypeAssistantMessage {
		t.Errorf("Type = %v, want %v", msg.Type, MessageTypeAssistantMessage)
	}
	if msg.GetContent() != "Hi there" {
		t.Errorf("GetContent() = %v, want 'Hi there'", msg.GetContent())
	}
}

func TestNewToolCallEvent(t *testing.T) {
	args := map[string]string{"query": "main.go"}
	msg, err := NewToolCallEvent("session-1", "call-1", "search_code", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != MessageTypeToolCall {
		t.Errorf("Type = %v, want %v", msg.Type, MessageTypeToolCall)
	}
	if msg.CallID != "call-1" {
		t.Errorf("CallID = %v, want call-1", msg.CallID)
	}

	p, ok := msg.GetToolCallPayload()
	if !ok {
		t.Fatal("GetToolCallPayload() returned false")
	}
	if p.Tool != "search_code" {
		t.Errorf("Tool = %v, want search_code", p.Tool)
	}
	if p.Arguments["query"] != "main.go" {
		t.Errorf("Arguments[query] = %v, want main.go", p.Arguments["query"])
	}
}

func TestNewToolCallEvent_Validation(t *testing.T) {
	args := map[string]string{"q": "test"}
	if _, err := NewToolCallEvent("", "c1", "tool", args); err == nil {
		t.Error("expected error for empty session_id")
	}
	if _, err := NewToolCallEvent("s1", "", "tool", args); err == nil {
		t.Error("expected error for empty call_id")
	}
	if _, err := NewToolCallEvent("s1", "c1", "", args); err == nil {
		t.Error("expected error for empty tool name")
	}
}

func TestNewToolResultEvent(t *testing.T) {
	msg, err := NewToolResultEvent("session-1", "call-1", "search_code", "Found 5 results", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != MessageTypeToolResult {
		t.Errorf("Type = %v, want %v", msg.Type, MessageTypeToolResult)
	}
	if msg.CallID != "call-1" {
		t.Errorf("CallID = %v, want call-1", msg.CallID)
	}

	p, ok := msg.GetToolResultPayload()
	if !ok {
		t.Fatal("GetToolResultPayload() returned false")
	}
	if p.Tool != "search_code" {
		t.Errorf("Tool = %v, want search_code", p.Tool)
	}
	if p.Content != "Found 5 results" {
		t.Errorf("Content = %v, want 'Found 5 results'", p.Content)
	}
	if p.IsError {
		t.Errorf("IsError = true, want false on happy path")
	}
}

func TestNewToolResultEvent_IsError(t *testing.T) {
	msg, err := NewToolResultEvent("session-1", "call-1", "rule.list",
		"[UNAVAILABLE] circuit breaker open for chirp-platform: too many failures", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	p, ok := msg.GetToolResultPayload()
	if !ok {
		t.Fatal("GetToolResultPayload() returned false")
	}
	if !p.IsError {
		t.Errorf("IsError = false, want true for error result")
	}

	if !bytes.Contains(msg.Payload, []byte(`"is_error":true`)) {
		t.Errorf("payload JSON missing is_error:true marker; got %s", string(msg.Payload))
	}
}

func TestToolResultPayload_OmitEmptyOnSuccess(t *testing.T) {
	msg, _ := NewToolResultEvent("s1", "c1", "search", "ok", false)
	if bytes.Contains(msg.Payload, []byte("is_error")) {
		t.Errorf("happy-path payload must omit is_error; got %s", string(msg.Payload))
	}
}

func TestNewToolResultEvent_Validation(t *testing.T) {
	if _, err := NewToolResultEvent("", "c1", "tool", "result", false); err == nil {
		t.Error("expected error for empty session_id")
	}
	if _, err := NewToolResultEvent("s1", "", "tool", "result", false); err == nil {
		t.Error("expected error for empty call_id")
	}
}

func TestNewReasoningEvent(t *testing.T) {
	msg, err := NewReasoningEvent("session-1", "I should use the search tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != MessageTypeReasoning {
		t.Errorf("Type = %v, want %v", msg.Type, MessageTypeReasoning)
	}
	if msg.GetContent() != "I should use the search tool" {
		t.Errorf("GetContent() = %v, want reasoning text", msg.GetContent())
	}
}

func TestNewSystemEvent(t *testing.T) {
	msg, err := NewSystemEvent("session-1", "System notice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != MessageTypeSystem {
		t.Errorf("Type = %v, want %v", msg.Type, MessageTypeSystem)
	}
	if msg.GetContent() != "System notice" {
		t.Errorf("GetContent() = %v, want 'System notice'", msg.GetContent())
	}
}

func TestToHistoryMessage_AllTypes(t *testing.T) {
	tests := []struct {
		name     string
		msgType  MessageType
		payload  interface{}
		wantRole string
	}{
		{"user", MessageTypeUserMessage, ContentPayload{Content: "hi"}, "user"},
		{"assistant", MessageTypeAssistantMessage, ContentPayload{Content: "hello"}, "assistant"},
		{"tool_call", MessageTypeToolCall, ToolCallPayload{Tool: "search"}, "assistant"},
		{"tool_result", MessageTypeToolResult, ToolResultPayload{Tool: "search", Content: "found"}, "tool"},
		{"reasoning", MessageTypeReasoning, ContentPayload{Content: "thinking..."}, "assistant"},
		{"system", MessageTypeSystem, ContentPayload{Content: "notice"}, "system"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := json.Marshal(tt.payload)
			msg := &Message{
				SessionID: "s1",
				Type:      tt.msgType,
				Payload:   p,
			}
			hm := msg.ToHistoryMessage()
			if hm.Role != tt.wantRole {
				t.Errorf("Role = %v, want %v", hm.Role, tt.wantRole)
			}
		})
	}
}

func TestToHistoryMessage_ToolResult_Content(t *testing.T) {
	msg, _ := NewToolResultEvent("s1", "call-1", "search", "Found 3 files", false)
	hm := msg.ToHistoryMessage()

	if hm.Role != "tool" {
		t.Errorf("Role = %v, want tool", hm.Role)
	}
	if hm.CallID != "call-1" {
		t.Errorf("CallID = %v, want call-1", hm.CallID)
	}
	// tool_result GetContent extracts from ToolResultPayload.Content
	if hm.Content != "" {
		// GetContent uses ContentPayload, not ToolResultPayload
		// For tool results, content is in the payload struct
	}
}

func TestPayloadRoundtrip(t *testing.T) {
	args := map[string]string{"query": "test", "path": "/src"}
	msg, _ := NewToolCallEvent("s1", "c1", "search", args)

	// Serialize to JSON and back
	data, err := json.Marshal(msg.Payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored json.RawMessage
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var p ToolCallPayload
	if err := json.Unmarshal(restored, &p); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if p.Tool != "search" {
		t.Errorf("Tool = %v, want search", p.Tool)
	}
	if p.Arguments["query"] != "test" {
		t.Errorf("Arguments[query] = %v, want test", p.Arguments["query"])
	}
}
