package agentstatecreate

import (
	"context"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// AgentStateRepository defines repository interface for AgentState
type AgentStateRepository interface {
	Create(ctx context.Context, state *domain.AgentState) error
	GetByID(ctx context.Context, id string) (*domain.AgentState, error)
	GetBySessionID(ctx context.Context, sessionID string) (*domain.AgentState, error)
	Update(ctx context.Context, state *domain.AgentState) error
}

// Input represents input for create agent state use case
type Input struct {
	SessionID string
	TaskID    string
}

// Output represents output from create agent state use case
type Output struct {
	AgentState *domain.AgentState
}

// Usecase handles agent state creation
type Usecase struct {
	agentStateRepo AgentStateRepository
}

// New creates a new create agent state use case
func New(agentStateRepo AgentStateRepository) *Usecase {
	return &Usecase{
		agentStateRepo: agentStateRepo,
	}
}

// Execute creates a new agent state
func (u *Usecase) Execute(ctx context.Context, input Input) (*Output, error) {
	agentState, err := domain.NewAgentState(input.SessionID, input.TaskID)
	if err != nil {
		return nil, err
	}

	if err := u.agentStateRepo.Create(ctx, agentState); err != nil {
		return nil, errors.Wrap(err, errors.CodeInternal, "failed to create agent state")
	}

	return &Output{AgentState: agentState}, nil
}
