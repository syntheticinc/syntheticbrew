package persistence

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"gorm.io/gorm"
)

// AgentRunStorage implements agent run persistence using GORM (PostgreSQL).
type AgentRunStorage struct {
	db *gorm.DB
}

// NewAgentRunStorage creates a new agent run storage.
func NewAgentRunStorage(db *gorm.DB) *AgentRunStorage {
	slog.InfoContext(context.Background(), "agent run storage initialized (PostgreSQL)")
	return &AgentRunStorage{db: db}
}

// Save persists a new agent run.
func (s *AgentRunStorage) Save(ctx context.Context, run *domain.AgentRun) error {
	m := agentRunToModel(run)
	if err := s.db.WithContext(ctx).Create(&m).Error; err != nil {
		return fmt.Errorf("insert agent run: %w", err)
	}
	slog.DebugContext(ctx, "agent run saved", "agent_id", run.ID, "session_id", run.SessionID, "status", run.Status)
	return nil
}

// Update updates an existing agent run.
func (s *AgentRunStorage) Update(ctx context.Context, run *domain.AgentRun) error {
	result := s.db.WithContext(ctx).Model(&models.AgentRunModel{}).
		Where("id = ?", run.ID).
		Updates(map[string]interface{}{
			"status":       string(run.Status),
			"result":       run.Result,
			"error":        run.Error,
			"completed_at": run.CompletedAt,
		})
	if result.Error != nil {
		return fmt.Errorf("update agent run: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("agent run not found: %s", run.ID)
	}
	slog.DebugContext(ctx, "agent run updated", "agent_id", run.ID, "status", run.Status)
	return nil
}

// GetByID retrieves an agent run by ID.
func (s *AgentRunStorage) GetByID(ctx context.Context, id string) (*domain.AgentRun, error) {
	var m models.AgentRunModel
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&m).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get agent run: %w", err)
	}
	return modelToAgentRun(&m), nil
}

// GetBySessionID retrieves all agent runs for a session.
func (s *AgentRunStorage) GetBySessionID(ctx context.Context, sessionID string) ([]*domain.AgentRun, error) {
	var ms []models.AgentRunModel
	err := s.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Order("started_at DESC").
		Find(&ms).Error
	if err != nil {
		return nil, fmt.Errorf("query agent runs by session: %w", err)
	}
	return modelsToAgentRuns(ms), nil
}

// GetRunningBySession retrieves all running agent runs for a session.
func (s *AgentRunStorage) GetRunningBySession(ctx context.Context, sessionID string) ([]*domain.AgentRun, error) {
	var ms []models.AgentRunModel
	err := s.db.WithContext(ctx).
		Where("session_id = ? AND status = ?", sessionID, "running").
		Order("started_at ASC").
		Find(&ms).Error
	if err != nil {
		return nil, fmt.Errorf("query running agent runs: %w", err)
	}
	return modelsToAgentRuns(ms), nil
}

// CountRunningBySession counts running agent runs for a session.
func (s *AgentRunStorage) CountRunningBySession(ctx context.Context, sessionID string) (int, error) {
	var count int64
	err := s.db.WithContext(ctx).
		Model(&models.AgentRunModel{}).
		Where("session_id = ? AND status = ?", sessionID, "running").
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count running agent runs: %w", err)
	}
	return int(count), nil
}

// CleanupOrphanedRuns marks all 'running' agent_runs as 'stopped'.
// Called at server startup — after crash, these agents are dead.
func (s *AgentRunStorage) CleanupOrphanedRuns(ctx context.Context) (int64, error) {
	now := time.Now()
	result := s.db.WithContext(ctx).
		Model(&models.AgentRunModel{}).
		Where("status = ?", "running").
		Updates(map[string]interface{}{
			"status":       "stopped",
			"completed_at": now,
		})
	if result.Error != nil {
		return 0, fmt.Errorf("cleanup orphaned runs: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// Close is a no-op because the shared DB is owned by the caller.
func (s *AgentRunStorage) Close() error {
	return nil
}

func agentRunToModel(run *domain.AgentRun) models.AgentRunModel {
	return models.AgentRunModel{
		ID:          run.ID,
		AgentID:     run.AgentID,
		TaskID:      run.TaskID,
		SessionID:   run.SessionID,
		Status:      string(run.Status),
		Result:      run.Result,
		Error:       run.Error,
		StartedAt:   run.StartedAt,
		CompletedAt: run.CompletedAt,
	}
}

func modelToAgentRun(m *models.AgentRunModel) *domain.AgentRun {
	return &domain.AgentRun{
		ID:          m.ID,
		AgentID:     m.AgentID,
		TaskID:      m.TaskID,
		SessionID:   m.SessionID,
		Status:      domain.AgentRunStatus(m.Status),
		Result:      m.Result,
		Error:       m.Error,
		StartedAt:   m.StartedAt,
		CompletedAt: m.CompletedAt,
	}
}

func modelsToAgentRuns(ms []models.AgentRunModel) []*domain.AgentRun {
	runs := make([]*domain.AgentRun, 0, len(ms))
	for i := range ms {
		runs = append(runs, modelToAgentRun(&ms[i]))
	}
	return runs
}
