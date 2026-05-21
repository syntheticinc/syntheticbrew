package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

type mockMemoryStorer struct {
	stored []*domain.Memory
	err    error
}

func (m *mockMemoryStorer) Store(ctx context.Context, mem *domain.Memory, maxEntries int) error {
	if m.err != nil {
		return m.err
	}
	mem.ID = "mock-id"
	m.stored = append(m.stored, mem)
	return nil
}

func TestMemoryStoreTool_Info(t *testing.T) {
	tool := NewMemoryStoreTool("1", "user-1", &mockMemoryStorer{}, 0)
	info, err := tool.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "memory_store", info.Name)
}

func TestMemoryStoreTool_Store(t *testing.T) {
	storer := &mockMemoryStorer{}
	tool := NewMemoryStoreTool("1", "user-1", storer, 0)

	args, _ := json.Marshal(memoryStoreArgs{
		Content:  "user prefers dark mode",
		Metadata: map[string]string{"source": "chat"},
	})

	result, err := tool.InvokableRun(context.Background(), string(args))
	require.NoError(t, err)
	assert.Contains(t, result, "stored successfully")
	require.Len(t, storer.stored, 1)
	assert.Equal(t, "user prefers dark mode", storer.stored[0].Content)
	assert.Equal(t, "1", storer.stored[0].SchemaID)
	assert.Equal(t, "user-1", storer.stored[0].UserSub)

	val, ok := storer.stored[0].GetMetadata("source")
	assert.True(t, ok)
	assert.Equal(t, "chat", val)
}

func TestMemoryStoreTool_EmptyContent(t *testing.T) {
	storer := &mockMemoryStorer{}
	tool := NewMemoryStoreTool("1", "user-1", storer, 0)

	args, _ := json.Marshal(memoryStoreArgs{Content: ""})
	result, err := tool.InvokableRun(context.Background(), string(args))
	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Len(t, storer.stored, 0)
}

func TestMemoryStoreTool_InvalidJSON(t *testing.T) {
	tool := NewMemoryStoreTool("1", "user-1", &mockMemoryStorer{}, 0)
	result, err := tool.InvokableRun(context.Background(), "not json")
	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
}
