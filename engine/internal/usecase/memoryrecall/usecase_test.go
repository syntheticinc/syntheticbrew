package memoryrecall

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

func (m *mockRepo) ListBySchemaAndUser(ctx context.Context, schemaID, userID string) ([]*domain.Memory, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.memories, nil
}

func TestUsecase_Execute(t *testing.T) {
	repo := &mockRepo{
		memories: []*domain.Memory{
			{ID: "1", Content: "user prefers dark mode"},
			{ID: "2", Content: "user name is Alice"},
		},
	}
	uc := New(repo)

	memories, err := uc.Execute(context.Background(), Input{
		SchemaID: "10",
		UserID:   "user-1",
	})
	require.NoError(t, err)
	assert.Len(t, memories, 2)
}

func TestUsecase_Execute_EmptySchemaID(t *testing.T) {
	uc := New(&mockRepo{})
	_, err := uc.Execute(context.Background(), Input{UserID: "user-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "schema_id is required")
}

func TestUsecase_Execute_EmptyUserID(t *testing.T) {
	uc := New(&mockRepo{})
	_, err := uc.Execute(context.Background(), Input{SchemaID: "10"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user_id is required")
}

func TestUsecase_Execute_RepoError(t *testing.T) {
	repo := &mockRepo{err: fmt.Errorf("db error")}
	uc := New(repo)
	_, err := uc.Execute(context.Background(), Input{SchemaID: "10", UserID: "user-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recall memories")
}

func TestUsecase_Execute_NoMemories(t *testing.T) {
	repo := &mockRepo{memories: []*domain.Memory{}}
	uc := New(repo)

	memories, err := uc.Execute(context.Background(), Input{SchemaID: "10", UserID: "user-1"})
	require.NoError(t, err)
	assert.Len(t, memories, 0)
}
