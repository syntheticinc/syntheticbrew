package adapters

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

func TestEventToModel_UserMessage(t *testing.T) {
	msg, _ := domain.NewUserMessageEvent("session-1", "Hello")
	msg.ID = uuid.New().String()
	msg.AgentID = "supervisor"

	model, err := EventToModel(msg)
	if err != nil {
		t.Fatalf("EventToModel() error = %v", err)
	}
	if model.EventType != "user_message" {
		t.Errorf("EventType = %v, want user_message", model.EventType)
	}
	if model.SessionID != "session-1" {
		t.Errorf("SessionID = %v, want session-1", model.SessionID)
	}
	if model.AgentID == nil || *model.AgentID != "supervisor" {
		t.Errorf("AgentID = %v, want supervisor", model.AgentID)
	}
	if model.Payload == nil {
		t.Fatal("Payload is nil")
	}

	var p domain.ContentPayload
	if err := json.Unmarshal(model.Payload, &p); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if p.Content != "Hello" {
		t.Errorf("payload content = %v, want Hello", p.Content)
	}
}

func TestEventToModel_ToolCall(t *testing.T) {
	args := map[string]string{"query": "main.go"}
	msg, _ := domain.NewToolCallEvent("session-1", "call-1", "search", args)

	model, err := EventToModel(msg)
	if err != nil {
		t.Fatalf("EventToModel() error = %v", err)
	}
	if model.EventType != "tool_call" {
		t.Errorf("EventType = %v, want tool_call", model.EventType)
	}
	if model.CallID != "call-1" {
		t.Errorf("CallID = %v, want call-1", model.CallID)
	}

	var p domain.ToolCallPayload
	if err := json.Unmarshal(model.Payload, &p); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if p.Tool != "search" {
		t.Errorf("Tool = %v, want search", p.Tool)
	}
	if p.Arguments["query"] != "main.go" {
		t.Errorf("Arguments[query] = %v, want main.go", p.Arguments["query"])
	}
}

func TestEventToModel_Nil(t *testing.T) {
	model, err := EventToModel(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if model != nil {
		t.Error("expected nil model for nil event")
	}
}

func TestEventToModel_GeneratesUUID(t *testing.T) {
	msg, _ := domain.NewUserMessageEvent("session-1", "Hello")
	// ID is empty — should generate UUID
	model, _ := EventToModel(msg)
	if model.ID == "" {
		t.Error("expected generated UUID, got empty")
	}
}

func TestEventFromModel_UserMessage(t *testing.T) {
	payload, _ := json.Marshal(domain.ContentPayload{Content: "Hello"})
	supervisorID := "supervisor"
	model := &models.MessageModel{
		ID:        uuid.New().String(),
		SessionID: "session-1",
		EventType: "user_message",
		AgentID:   &supervisorID,
		Payload:   payload,
		CreatedAt: time.Now(),
	}

	event, err := EventFromModel(model)
	if err != nil {
		t.Fatalf("EventFromModel() error = %v", err)
	}
	if event.Type != domain.MessageTypeUserMessage {
		t.Errorf("Type = %v, want user_message", event.Type)
	}
	if event.GetContent() != "Hello" {
		t.Errorf("GetContent() = %v, want Hello", event.GetContent())
	}
	if event.AgentID != "supervisor" {
		t.Errorf("AgentID = %v, want supervisor", event.AgentID)
	}
}

func TestEventFromModel_ToolCall(t *testing.T) {
	payload, _ := json.Marshal(domain.ToolCallPayload{
		Tool:      "search",
		Arguments: map[string]string{"q": "test"},
	})
	model := &models.MessageModel{
		ID:        uuid.New().String(),
		SessionID: "session-1",
		EventType: "tool_call",
		CallID:    "call-1",
		Payload:   payload,
		CreatedAt: time.Now(),
	}

	event, err := EventFromModel(model)
	if err != nil {
		t.Fatalf("EventFromModel() error = %v", err)
	}
	if event.Type != domain.MessageTypeToolCall {
		t.Errorf("Type = %v, want tool_call", event.Type)
	}
	if event.CallID != "call-1" {
		t.Errorf("CallID = %v, want call-1", event.CallID)
	}
	p, ok := event.GetToolCallPayload()
	if !ok {
		t.Fatal("GetToolCallPayload() returned false")
	}
	if p.Tool != "search" {
		t.Errorf("Tool = %v, want search", p.Tool)
	}
}

func TestEventFromModel_Nil(t *testing.T) {
	event, err := EventFromModel(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event != nil {
		t.Error("expected nil event for nil model")
	}
}

func TestEventRoundtrip(t *testing.T) {
	original, _ := domain.NewToolResultEvent("session-1", "call-1", "search", "Found 5 files", false)
	original.AgentID = "supervisor"

	model, err := EventToModel(original)
	if err != nil {
		t.Fatalf("EventToModel: %v", err)
	}

	restored, err := EventFromModel(model)
	if err != nil {
		t.Fatalf("EventFromModel: %v", err)
	}

	if restored.Type != original.Type {
		t.Errorf("Type = %v, want %v", restored.Type, original.Type)
	}
	if restored.CallID != original.CallID {
		t.Errorf("CallID = %v, want %v", restored.CallID, original.CallID)
	}
	if restored.SessionID != original.SessionID {
		t.Errorf("SessionID = %v, want %v", restored.SessionID, original.SessionID)
	}
	if restored.AgentID != original.AgentID {
		t.Errorf("AgentID = %v, want %v", restored.AgentID, original.AgentID)
	}

	rp, _ := restored.GetToolResultPayload()
	if rp.Tool != "search" {
		t.Errorf("Tool = %v, want search", rp.Tool)
	}
	if rp.Content != "Found 5 files" {
		t.Errorf("Content = %v, want 'Found 5 files'", rp.Content)
	}
}
