package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

type mockMemoryRecaller struct {
	memories []*domain.Memory
	err      error
}

func (m *mockMemoryRecaller) ListBySchemaAndUser(ctx context.Context, schemaID, userID string) ([]*domain.Memory, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.memories, nil
}

func TestMemoryRecallTool_Info(t *testing.T) {
	tool := NewMemoryRecallTool("1", "user-1", &mockMemoryRecaller{})
	info, err := tool.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "memory_recall", info.Name)
}

func TestMemoryRecallTool_NoMemories(t *testing.T) {
	tool := NewMemoryRecallTool("1", "user-1", &mockMemoryRecaller{})
	result, err := tool.InvokableRun(context.Background(), "{}")
	require.NoError(t, err)
	assert.Contains(t, result, "No memories found")
}

func TestMemoryRecallTool_RecallAll(t *testing.T) {
	recaller := &mockMemoryRecaller{
		memories: []*domain.Memory{
			{ID: "1", Content: "user prefers dark mode", CreatedAt: time.Now()},
			{ID: "2", Content: "user's name is Alice", CreatedAt: time.Now()},
		},
	}
	tool := NewMemoryRecallTool("1", "user-1", recaller)

	result, err := tool.InvokableRun(context.Background(), "{}")
	require.NoError(t, err)
	assert.Contains(t, result, "Recalled 2 memories")
	assert.Contains(t, result, "dark mode")
	assert.Contains(t, result, "Alice")
}

func TestMemoryRecallTool_FilterByQuery(t *testing.T) {
	recaller := &mockMemoryRecaller{
		memories: []*domain.Memory{
			{ID: "1", Content: "user prefers dark mode"},
			{ID: "2", Content: "user's name is Alice"},
			{ID: "3", Content: "user works at Acme Corp"},
		},
	}
	tool := NewMemoryRecallTool("1", "user-1", recaller)

	args, _ := json.Marshal(memoryRecallArgs{Query: "dark"})
	result, err := tool.InvokableRun(context.Background(), string(args))
	require.NoError(t, err)
	assert.Contains(t, result, "Recalled 1 memories")
	assert.Contains(t, result, "dark mode")
	assert.NotContains(t, result, "Alice")
}

func TestMemoryRecallTool_Limit(t *testing.T) {
	memories := make([]*domain.Memory, 20)
	for i := range memories {
		memories[i] = &domain.Memory{ID: "id", Content: "memory"}
	}
	recaller := &mockMemoryRecaller{memories: memories}
	tool := NewMemoryRecallTool("1", "user-1", recaller)

	args, _ := json.Marshal(memoryRecallArgs{Limit: 5})
	result, err := tool.InvokableRun(context.Background(), string(args))
	require.NoError(t, err)
	assert.Contains(t, result, "Recalled 5 memories")
}

func TestMemoryRecallTool_EmptyArgs(t *testing.T) {
	recaller := &mockMemoryRecaller{
		memories: []*domain.Memory{
			{ID: "1", Content: "some memory"},
		},
	}
	tool := NewMemoryRecallTool("1", "user-1", recaller)

	// Empty string arguments — should use defaults
	result, err := tool.InvokableRun(context.Background(), "")
	require.NoError(t, err)
	assert.Contains(t, result, "Recalled 1 memories")
}

func TestMemoryRecallTool_WithMetadata(t *testing.T) {
	recaller := &mockMemoryRecaller{
		memories: []*domain.Memory{
			{
				ID:       "1",
				Content:  "user likes cats",
				Metadata: map[string]string{"source": "agent", "tool": "memory_store"},
			},
		},
	}
	tool := NewMemoryRecallTool("1", "user-1", recaller)

	result, err := tool.InvokableRun(context.Background(), "{}")
	require.NoError(t, err)
	assert.Contains(t, result, "Metadata:")
}
