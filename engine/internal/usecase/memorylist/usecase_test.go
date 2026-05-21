package memorylist

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

type mockRepo struct {
	memories []*domain.Memory
	err      error
}

func (m *mockRepo) ListBySchema(ctx context.Context, schemaID string) ([]*domain.Memory, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.memories, nil
}

func TestUsecase_Execute(t *testing.T) {
	repo := &mockRepo{
		memories: []*domain.Memory{
			{ID: "1", SchemaID: "10", Content: "memory 1"},
			{ID: "2", SchemaID: "10", Content: "memory 2"},
		},
	}
	uc := New(repo)

	memories, err := uc.Execute(context.Background(), "10")
	require.NoError(t, err)
	assert.Len(t, memories, 2)
}

func TestUsecase_Execute_EmptySchemaID(t *testing.T) {
	uc := New(&mockRepo{})
	_, err := uc.Execute(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "schema_id is required")
}

func TestUsecase_Execute_RepoError(t *testing.T) {
	repo := &mockRepo{err: fmt.Errorf("db error")}
	uc := New(repo)
	_, err := uc.Execute(context.Background(), "10")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list memories")
}
