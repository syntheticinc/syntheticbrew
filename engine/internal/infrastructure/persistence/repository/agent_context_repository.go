package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/adapters"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
	"gorm.io/gorm"
)

// AgentContextRepository provides CRUD for agent context snapshots
type AgentContextRepository struct {
	db *gorm.DB
}

// NewAgentContextRepository creates a new AgentContextRepository
func NewAgentContextRepository(db *gorm.DB) *AgentContextRepository {
	return &AgentContextRepository{db: db}
}

// Save upserts the active snapshot for (session_id, agent_id).
// Uses explicit find-then-update because ON CONFLICT requires a full unique
// index but the DB keeps only a partial unique index WHERE status='active',
// allowing compacted/expired rows to accumulate as history.
func (r *AgentContextRepository) Save(ctx context.Context, snapshot *domain.AgentContextSnapshot) error {
	model := adapters.AgentContextSnapshotToModel(snapshot)
	model.UpdatedAt = time.Now()

	var existing models.AgentContextSnapshotModel
	err := r.db.WithContext(ctx).
		Where("session_id = ? AND agent_id = ? AND status = 'active'", model.SessionID, model.AgentID).
		First(&existing).Error

	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return pkgerrors.Wrap(err, pkgerrors.CodeInternal, "find active snapshot")
	}

	if err == nil {
		// Active snapshot found — update it in place.
		model.ID = existing.ID
		result := r.db.WithContext(ctx).Model(&existing).Updates(map[string]interface{}{
			"context_data":   model.ContextData,
			"step_number":    model.StepNumber,
			"token_count":    model.TokenCount,
			"status":         model.Status,
			"updated_at":     model.UpdatedAt,
			"schema_version": model.SchemaVersion,
		})
		if result.Error != nil {
			return pkgerrors.Wrap(result.Error, pkgerrors.CodeInternal, "update agent context snapshot")
		}
		snapshot.ID = existing.ID
		return nil
	}

	// No active snapshot — insert new.
	if model.ID == "" {
		model.ID = uuid.New().String()
	}
	result := r.db.WithContext(ctx).Create(model)
	if result.Error != nil {
		return pkgerrors.Wrap(result.Error, pkgerrors.CodeInternal, "save agent context snapshot")
	}
	snapshot.ID = model.ID
	return nil
}

// Load loads the most-recently-updated snapshot for (session_id, agent_id).
// Multiple rows accumulate over a multi-turn session because Save leaves
// completed turns in status=expired/compacted as history; without an explicit
// order the resume path picked an arbitrary (often the first) row and the
// agent lost context between turns.
func (r *AgentContextRepository) Load(ctx context.Context, sessionID, agentID string) (*domain.AgentContextSnapshot, error) {
	var model models.AgentContextSnapshotModel
	result := r.db.WithContext(ctx).
		Where("session_id = ? AND agent_id = ?", sessionID, agentID).
		Order("updated_at DESC").
		First(&model)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil // Not found = fresh start
		}
		return nil, pkgerrors.Wrap(result.Error, pkgerrors.CodeInternal, "load agent context snapshot")
	}

	return adapters.AgentContextSnapshotFromModel(&model), nil
}

// Delete removes snapshot by session+agent ID
func (r *AgentContextRepository) Delete(ctx context.Context, sessionID, agentID string) error {
	result := r.db.WithContext(ctx).Where("session_id = ? AND agent_id = ?", sessionID, agentID).Delete(&models.AgentContextSnapshotModel{})
	if result.Error != nil {
		return pkgerrors.Wrap(result.Error, pkgerrors.CodeInternal, "delete agent context snapshot")
	}
	return nil
}

// FindActive returns all snapshots with status "active"
func (r *AgentContextRepository) FindActive(ctx context.Context) ([]*domain.AgentContextSnapshot, error) {
	var dbModels []models.AgentContextSnapshotModel
	result := r.db.WithContext(ctx).Where("status = ?", string(domain.AgentContextStatusActive)).Find(&dbModels)
	if result.Error != nil {
		return nil, pkgerrors.Wrap(result.Error, pkgerrors.CodeInternal, "find active snapshots")
	}

	snapshots := make([]*domain.AgentContextSnapshot, 0, len(dbModels))
	for i := range dbModels {
		snapshots = append(snapshots, adapters.AgentContextSnapshotFromModel(&dbModels[i]))
	}
	return snapshots, nil
}
