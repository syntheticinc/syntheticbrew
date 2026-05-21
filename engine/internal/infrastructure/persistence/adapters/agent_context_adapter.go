package adapters

import (
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// SerializeSchemaMessages serializes []*schema.Message to JSON bytes
func SerializeSchemaMessages(messages []*schema.Message) ([]byte, error) {
	if messages == nil {
		messages = []*schema.Message{}
	}
	data, err := json.Marshal(messages)
	if err != nil {
		return nil, fmt.Errorf("serialize schema messages: %w", err)
	}
	return data, nil
}

// DeserializeSchemaMessages deserializes JSON bytes to []*schema.Message
func DeserializeSchemaMessages(data []byte) ([]*schema.Message, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var messages []*schema.Message
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, fmt.Errorf("deserialize schema messages: %w", err)
	}
	return messages, nil
}

// AgentContextSnapshotToModel converts domain snapshot to AgentContextSnapshotModel
func AgentContextSnapshotToModel(snapshot *domain.AgentContextSnapshot) *models.AgentContextSnapshotModel {
	if snapshot == nil {
		return nil
	}

	id := snapshot.ID
	if id == "" {
		id = uuid.New().String()
	}

	return &models.AgentContextSnapshotModel{
		ID:            id,
		SessionID:     snapshot.SessionID,
		AgentID:       snapshot.AgentID,
		SchemaVersion: snapshot.SchemaVersion,
		ContextData:   snapshot.ContextData,
		StepNumber:    snapshot.StepNumber,
		TokenCount:    snapshot.TokenCount,
		Status:        string(snapshot.Status),
		CreatedAt:     snapshot.CreatedAt,
		UpdatedAt:     snapshot.UpdatedAt,
	}
}

// AgentContextSnapshotFromModel converts AgentContextSnapshotModel to domain snapshot
func AgentContextSnapshotFromModel(model *models.AgentContextSnapshotModel) *domain.AgentContextSnapshot {
	if model == nil {
		return nil
	}

	return &domain.AgentContextSnapshot{
		ID:            model.ID,
		SessionID:     model.SessionID,
		AgentID:       model.AgentID,
		SchemaVersion: model.SchemaVersion,
		ContextData:   model.ContextData,
		StepNumber:    model.StepNumber,
		TokenCount:    model.TokenCount,
		Status:        domain.AgentContextStatus(model.Status),
		CreatedAt:     model.CreatedAt,
		UpdatedAt:     model.UpdatedAt,
	}
}
