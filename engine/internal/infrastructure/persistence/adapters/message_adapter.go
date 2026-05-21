package adapters

import (
	"encoding/json"

	"github.com/google/uuid"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// EventToModel converts a domain Message to MessageModel.
func EventToModel(event *domain.Message) (*models.MessageModel, error) {
	if event == nil {
		return nil, nil
	}

	id := event.ID
	if id == "" {
		id = uuid.New().String()
	}

	payload := event.Payload
	if payload == nil {
		payload = json.RawMessage("{}")
	}

	var agentID *string
	if event.AgentID != "" {
		agentID = &event.AgentID
	}

	return &models.MessageModel{
		ID:        id,
		SessionID: event.SessionID,
		EventType: string(event.Type),
		AgentID:   agentID,
		CallID:    event.CallID,
		Payload:   payload,
		CreatedAt: event.CreatedAt,
	}, nil
}

// EventFromModel converts a MessageModel to a domain Message.
func EventFromModel(model *models.MessageModel) (*domain.Message, error) {
	if model == nil {
		return nil, nil
	}

	payload := model.Payload
	if payload == nil {
		payload = json.RawMessage("{}")
	}

	agentID := ""
	if model.AgentID != nil {
		agentID = *model.AgentID
	}

	return &domain.Message{
		ID:        model.ID,
		SessionID: model.SessionID,
		Type:      domain.MessageType(model.EventType),
		AgentID:   agentID,
		CallID:    model.CallID,
		Payload:   payload,
		CreatedAt: model.CreatedAt,
	}, nil
}

