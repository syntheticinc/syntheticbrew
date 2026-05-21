package agentstatecreate

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

type mockAgentStateRepo struct {
	created *domain.AgentState
	err     error
}

func (m *mockAgentStateRepo) Create(_ context.Context, state *domain.AgentState) error {
	if m.err != nil {
		return m.err
	}
	m.created = state
	return nil
}

func (m *mockAgentStateRepo) GetByID(_ context.Context, _ string) (*domain.AgentState, error) {
	return nil, nil
}

func (m *mockAgentStateRepo) GetBySessionID(_ context.Context, _ string) (*domain.AgentState, error) {
	return nil, nil
}

func (m *mockAgentStateRepo) Update(_ context.Context, _ *domain.AgentState) error {
	return nil
}

func TestNew(t *testing.T) {
	repo := &mockAgentStateRepo{}
	uc := New(repo)

	require.NotNil(t, uc)
	assert.Equal(t, repo, uc.agentStateRepo)
}

func TestUsecase_Execute(t *testing.T) {
	tests := []struct {
		name      string
		input     Input
		repoErr   error
		wantErr   bool
		errAssert func(t *testing.T, err error)
	}{
		{
			name: "valid input creates agent state",
			input: Input{
				SessionID: "session-123",
				TaskID:    "task-456",
			},
		},
		{
			name: "empty session id returns domain error",
			input: Input{
				SessionID: "",
				TaskID:    "task-456",
			},
			wantErr: true,
			errAssert: func(t *testing.T, err error) {
				assert.Contains(t, err.Error(), "session_id is required")
			},
		},
		{
			name: "empty task id returns domain error",
			input: Input{
				SessionID: "session-123",
				TaskID:    "",
			},
			wantErr: true,
			errAssert: func(t *testing.T, err error) {
				assert.Contains(t, err.Error(), "task_id is required")
			},
		},
		{
			name: "repo create failure returns wrapped error",
			input: Input{
				SessionID: "session-123",
				TaskID:    "task-456",
			},
			repoErr: fmt.Errorf("db connection lost"),
			wantErr: true,
			errAssert: func(t *testing.T, err error) {
				assert.True(t, pkgerrors.Is(err, pkgerrors.CodeInternal))
				assert.Contains(t, err.Error(), "failed to create agent state")
				assert.Contains(t, err.Error(), "db connection lost")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &mockAgentStateRepo{err: tt.repoErr}
			uc := New(repo)

			output, err := uc.Execute(context.Background(), tt.input)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, output)
				if tt.errAssert != nil {
					tt.errAssert(t, err)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, output)
			require.NotNil(t, output.AgentState)

			assert.Equal(t, tt.input.SessionID, output.AgentState.SessionID)
			assert.Equal(t, tt.input.TaskID, output.AgentState.TaskID)
			assert.False(t, output.AgentState.IsComplete)
			assert.Empty(t, output.AgentState.ToolCalls)
			assert.False(t, output.AgentState.CreatedAt.IsZero())
			assert.False(t, output.AgentState.UpdatedAt.IsZero())

			require.NotNil(t, repo.created)
			assert.Equal(t, output.AgentState, repo.created)
		})
	}
}
