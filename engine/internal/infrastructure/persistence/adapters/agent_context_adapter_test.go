package adapters

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSerializeDeserializeSchemaMessages_Roundtrip(t *testing.T) {
	messages := []*schema.Message{
		{
			Role:    schema.User,
			Content: "Hello, how are you?",
		},
		{
			Role:    schema.Assistant,
			Content: "I'm doing well, thank you!",
		},
		{
			Role:    schema.User,
			Content: "Can you help me with a task?",
		},
	}

	// Serialize
	data, err := SerializeSchemaMessages(messages)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	// Deserialize
	restored, err := DeserializeSchemaMessages(data)
	require.NoError(t, err)
	require.Len(t, restored, 3)

	// Verify content
	assert.Equal(t, schema.User, restored[0].Role)
	assert.Equal(t, "Hello, how are you?", restored[0].Content)
	assert.Equal(t, schema.Assistant, restored[1].Role)
	assert.Equal(t, "I'm doing well, thank you!", restored[1].Content)
	assert.Equal(t, schema.User, restored[2].Role)
	assert.Equal(t, "Can you help me with a task?", restored[2].Content)
}

func TestSerializeDeserializeSchemaMessages_Empty(t *testing.T) {
	tests := []struct {
		name     string
		messages []*schema.Message
	}{
		{
			name:     "nil messages",
			messages: nil,
		},
		{
			name:     "empty slice",
			messages: []*schema.Message{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Serialize
			data, err := SerializeSchemaMessages(tt.messages)
			require.NoError(t, err)
			require.NotEmpty(t, data)

			// Deserialize
			restored, err := DeserializeSchemaMessages(data)
			require.NoError(t, err)
			require.NotNil(t, restored)
			assert.Empty(t, restored)
		})
	}
}

func TestSerializeDeserializeSchemaMessages_EmptyData(t *testing.T) {
	// Deserialize empty bytes
	restored, err := DeserializeSchemaMessages([]byte{})
	require.NoError(t, err)
	assert.Nil(t, restored)
}

func TestSerializeDeserializeSchemaMessages_ToolCalls(t *testing.T) {
	// Create message with tool calls
	toolCallArgs := map[string]interface{}{
		"path":  "/path/to/file.go",
		"query": "test",
	}
	argsJSON, _ := json.Marshal(toolCallArgs)

	messages := []*schema.Message{
		{
			Role:    schema.Assistant,
			Content: "",
			ToolCalls: []schema.ToolCall{
				{
					ID:   "call-123",
					Type: "function",
					Function: schema.FunctionCall{
						Name:      "read_file",
						Arguments: string(argsJSON),
					},
				},
			},
		},
		{
			Role:       schema.Tool,
			Content:    "file contents here",
			ToolCallID: "call-123",
		},
	}

	// Serialize
	data, err := SerializeSchemaMessages(messages)
	require.NoError(t, err)

	// Deserialize
	restored, err := DeserializeSchemaMessages(data)
	require.NoError(t, err)
	require.Len(t, restored, 2)

	// Verify tool call message
	assert.Equal(t, schema.Assistant, restored[0].Role)
	assert.Empty(t, restored[0].Content)
	require.Len(t, restored[0].ToolCalls, 1)
	assert.Equal(t, "call-123", restored[0].ToolCalls[0].ID)
	assert.Equal(t, "read_file", restored[0].ToolCalls[0].Function.Name)

	// Verify tool result message
	assert.Equal(t, schema.Tool, restored[1].Role)
	assert.Equal(t, "file contents here", restored[1].Content)
	assert.Equal(t, "call-123", restored[1].ToolCallID)
}

func TestSerializeDeserializeSchemaMessages_Unicode(t *testing.T) {
	messages := []*schema.Message{
		{
			Role:    schema.User,
			Content: "Привет! Как дела? 你好！ مرحبا! こんにちは！",
		},
		{
			Role:    schema.Assistant,
			Content: "Всё отлично, спасибо! 很好，谢谢！ شكرا جزيلا! ありがとう！",
		},
	}

	// Serialize
	data, err := SerializeSchemaMessages(messages)
	require.NoError(t, err)

	// Deserialize
	restored, err := DeserializeSchemaMessages(data)
	require.NoError(t, err)
	require.Len(t, restored, 2)

	// Verify unicode preserved across cyrillic, chinese, arabic, japanese.
	assert.Equal(t, "Привет! Как дела? 你好！ مرحبا! こんにちは！", restored[0].Content)
	assert.Equal(t, "Всё отлично, спасибо! 很好，谢谢！ شكرا جزيلا! ありがとう！", restored[1].Content)
}

func TestAgentContextSnapshotToModel_Roundtrip(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)

	domainSnapshot := &domain.AgentContextSnapshot{
		ID:            uuid.New().String(),
		SessionID:     uuid.New().String(),
		AgentID:       "supervisor",
		SchemaVersion: domain.CurrentSchemaVersion,
		ContextData:   []byte(`[{"role":"user","content":"test"}]`),
		StepNumber:    5,
		TokenCount:    1500,
		Status:        domain.AgentContextStatusActive,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	// Convert to model
	model := AgentContextSnapshotToModel(domainSnapshot)
	require.NotNil(t, model)

	// Convert back to domain
	restored := AgentContextSnapshotFromModel(model)
	require.NotNil(t, restored)

	// Verify identical
	assert.Equal(t, domainSnapshot.ID, restored.ID)
	assert.Equal(t, domainSnapshot.SessionID, restored.SessionID)
	assert.Equal(t, domainSnapshot.AgentID, restored.AgentID)
	assert.Equal(t, domainSnapshot.SchemaVersion, restored.SchemaVersion)
	assert.Equal(t, domainSnapshot.ContextData, restored.ContextData)
	assert.Equal(t, domainSnapshot.StepNumber, restored.StepNumber)
	assert.Equal(t, domainSnapshot.TokenCount, restored.TokenCount)
	assert.Equal(t, domainSnapshot.Status, restored.Status)
	assert.True(t, domainSnapshot.CreatedAt.Equal(restored.CreatedAt))
	assert.True(t, domainSnapshot.UpdatedAt.Equal(restored.UpdatedAt))
}

func TestAgentContextSnapshotToModel_Nil(t *testing.T) {
	// Convert nil to model
	model := AgentContextSnapshotToModel(nil)
	assert.Nil(t, model)

	// Convert nil model to domain
	domain := AgentContextSnapshotFromModel(nil)
	assert.Nil(t, domain)
}

func TestAgentContextSnapshotToModel_AllAgentTypes(t *testing.T) {
	agentTypes := []string{
		"supervisor",
		"coder",
		"reviewer",
		"researcher",
	}

	for _, agentType := range agentTypes {
		t.Run(agentType, func(t *testing.T) {
			domainSnapshot := &domain.AgentContextSnapshot{
				ID:            uuid.New().String(),
				SessionID:     uuid.New().String(),
				AgentID:       "agent-" + agentType,
				SchemaVersion: 1,
				ContextData:   []byte(`[]`),
				StepNumber:    0,
				TokenCount:    0,
				Status:        domain.AgentContextStatusActive,
				CreatedAt:     time.Now(),
				UpdatedAt:     time.Now(),
			}

			// Convert to model and back
			model := AgentContextSnapshotToModel(domainSnapshot)
			restored := AgentContextSnapshotFromModel(model)

			// Verify agent ID preserved
			assert.Equal(t, "agent-"+agentType, restored.AgentID)
		})
	}
}

func TestAgentContextSnapshotToModel_AllStatuses(t *testing.T) {
	statuses := []domain.AgentContextStatus{
		domain.AgentContextStatusActive,
		domain.AgentContextStatusCompacted,
		domain.AgentContextStatusExpired,
	}

	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			domainSnapshot := &domain.AgentContextSnapshot{
				ID:            uuid.New().String(),
				SessionID:     uuid.New().String(),
				AgentID:       "test-agent",
				SchemaVersion: 1,
				ContextData:   []byte(`[]`),
				StepNumber:    0,
				TokenCount:    0,
				Status:        status,
				CreatedAt:     time.Now(),
				UpdatedAt:     time.Now(),
			}

			// Convert to model and back
			model := AgentContextSnapshotToModel(domainSnapshot)
			restored := AgentContextSnapshotFromModel(model)

			// Verify status preserved
			assert.Equal(t, status, restored.Status)
		})
	}
}
