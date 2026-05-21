package repository

import (
	"context"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// MessageRepository defines the interface for message persistence
type MessageRepository interface {
	Create(ctx context.Context, message *domain.Message) error
	GetBySessionID(ctx context.Context, sessionID string, limit, offset int) ([]*domain.Message, error)
	GetBySessionAndAgent(ctx context.Context, sessionID, agentID string, limit, offset int) ([]*domain.Message, error)
}
