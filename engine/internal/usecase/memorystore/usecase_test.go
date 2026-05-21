package memorystore

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

type mockRepo struct {
	stored []*domain.Memory
	err    error
}

func (m *mockRepo) Store(ctx context.Context, mem *domain.Memory, maxEntries int) error {
	if m.err != nil {
		return m.err
	}
	mem.ID = "generated-id"
	m.stored = append(m.stored, mem)
	return nil
}

func TestUsecase_Execute(t *testing.T) {
	repo := &mockRepo{}
	uc := New(repo)

	mem, err := uc.Execute(context.Background(), Input{
		SchemaID:   "1",
		UserSub:     "user-1",
		Content:    "user prefers dark mode",
		Metadata:   map[string]string{"source": "agent"},
		MaxEntries: 100,
	})
	require.NoError(t, err)
	assert.Equal(t, "generated-id", mem.ID)
	assert.Equal(t, "1", mem.SchemaID)
	assert.Equal(t, "user-1", mem.UserSub)
	assert.Equal(t, "user prefers dark mode", mem.Content)

	val, ok := mem.GetMetadata("source")
	assert.True(t, ok)
	assert.Equal(t, "agent", val)
}

func TestUsecase_Execute_EmptyContent(t *testing.T) {
	uc := New(&mockRepo{})

	_, err := uc.Execute(context.Background(), Input{
		SchemaID: "1",
		UserSub:   "user-1",
		Content:  "",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "content is required")
}

func TestUsecase_Execute_RepoError(t *testing.T) {
	repo := &mockRepo{err: fmt.Errorf("db error")}
	uc := New(repo)

	_, err := uc.Execute(context.Background(), Input{
		SchemaID: "1",
		UserSub:   "user-1",
		Content:  "something",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store memory")
}
