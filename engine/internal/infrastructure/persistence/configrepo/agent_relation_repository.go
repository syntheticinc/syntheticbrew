package configrepo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"gorm.io/gorm"
)

// AgentRelationRecord is an intermediate struct for DB <-> domain mapping.
//
// V2 has a single implicit DELEGATION relationship type (see
// docs/architecture/agent-first-runtime.md §3.1). Optional Config carries
// non-typing routing hints (priority, conditions).
//
// Q.5: source/target are now agent UUIDs (was agent names).
type AgentRelationRecord struct {
	ID            string
	SchemaID      string
	SourceAgentID string
	TargetAgentID string
	Config        map[string]interface{}
}

// GORMAgentRelationRepository implements agent-relation CRUD using GORM.
type GORMAgentRelationRepository struct {
	db *gorm.DB
}

// NewGORMAgentRelationRepository creates a new GORMAgentRelationRepository.
func NewGORMAgentRelationRepository(db *gorm.DB) *GORMAgentRelationRepository {
	return &GORMAgentRelationRepository{db: db}
}

// List returns all agent relations for a schema (tenant-scoped).
func (r *GORMAgentRelationRepository) List(ctx context.Context, schemaID string) ([]AgentRelationRecord, error) {
	var rels []models.AgentRelationModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("schema_id = ?", schemaID).
		Find(&rels).Error; err != nil {
		return nil, fmt.Errorf("list agent relations: %w", err)
	}

	records := make([]AgentRelationRecord, 0, len(rels))
	for _, rel := range rels {
		rec, err := toAgentRelationRecord(rel)
		if err != nil {
			return nil, fmt.Errorf("convert agent relation %s: %w", rel.ID, err)
		}
		records = append(records, rec)
	}
	return records, nil
}

// GetByID returns a single agent relation by ID (tenant-scoped).
func (r *GORMAgentRelationRepository) GetByID(ctx context.Context, id string) (*AgentRelationRecord, error) {
	var rel models.AgentRelationModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("id = ?", id).
		First(&rel).Error; err != nil {
		return nil, fmt.Errorf("get agent relation %s: %w", id, err)
	}
	rec, err := toAgentRelationRecord(rel)
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// Create inserts a new agent relation, stamping tenant from context.
func (r *GORMAgentRelationRepository) Create(ctx context.Context, record *AgentRelationRecord) error {
	configJSON, err := json.Marshal(record.Config)
	if err != nil {
		return fmt.Errorf("marshal agent relation config: %w", err)
	}

	model := models.AgentRelationModel{
		TenantID:      tenantIDFromCtx(ctx),
		SchemaID:      record.SchemaID,
		SourceAgentID: record.SourceAgentID,
		TargetAgentID: record.TargetAgentID,
		Config:        string(configJSON),
	}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("create agent relation: %w", err)
	}
	record.ID = model.ID
	return nil
}

// Update updates an existing agent relation by ID (tenant-scoped).
func (r *GORMAgentRelationRepository) Update(ctx context.Context, id string, record *AgentRelationRecord) error {
	configJSON, err := json.Marshal(record.Config)
	if err != nil {
		return fmt.Errorf("marshal agent relation config: %w", err)
	}

	result := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Model(&models.AgentRelationModel{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"source_agent_id": record.SourceAgentID,
			"target_agent_id": record.TargetAgentID,
			"config":          string(configJSON),
		})
	if result.Error != nil {
		return fmt.Errorf("update agent relation %s: %w", id, result.Error)
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// Delete removes an agent relation by ID (tenant-scoped).
func (r *GORMAgentRelationRepository) Delete(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Delete(&models.AgentRelationModel{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("delete agent relation %s: %w", id, result.Error)
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func toAgentRelationRecord(rel models.AgentRelationModel) (AgentRelationRecord, error) {
	var config map[string]interface{}
	if rel.Config != "" {
		if err := json.Unmarshal([]byte(rel.Config), &config); err != nil {
			return AgentRelationRecord{}, fmt.Errorf("unmarshal agent relation config: %w", err)
		}
	}
	return AgentRelationRecord{
		ID:            rel.ID,
		SchemaID:      rel.SchemaID,
		SourceAgentID: rel.SourceAgentID,
		TargetAgentID: rel.TargetAgentID,
		Config:        config,
	}, nil
}
