package configrepo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// GORMKGEntityRepository implements entity CRUD + filtered list using GORM.
type GORMKGEntityRepository struct {
	db *gorm.DB
}

func NewGORMKGEntityRepository(db *gorm.DB) *GORMKGEntityRepository {
	return &GORMKGEntityRepository{db: db}
}

func (r *GORMKGEntityRepository) dbHandle(ctx context.Context) *gorm.DB {
	if tx, ok := txFromContext(ctx); ok {
		return tx
	}
	return r.db.WithContext(ctx)
}

// UpsertEntity persists a single entity row (create or replace).
func (r *GORMKGEntityRepository) UpsertEntity(ctx context.Context, e *domain.KGEntity) error {
	model := entityToModel(e)
	return r.dbHandle(ctx).Save(model).Error
}

// ReplaceEntities does delete-then-insert of all entities under
// (tenant_id, bundle_name). Used by kgapply for atomic bundle replace.
func (r *GORMKGEntityRepository) ReplaceEntities(
	ctx context.Context,
	tenantID, bundleName string,
	entities []*domain.KGEntity,
) error {
	db := r.dbHandle(ctx)
	if err := db.
		Where("tenant_id = ? AND bundle_name = ?", tenantID, bundleName).
		Delete(&models.KGEntityModel{}).Error; err != nil {
		return fmt.Errorf("clear entities for bundle %s: %w", bundleName, err)
	}
	if len(entities) == 0 {
		return nil
	}
	rows := make([]models.KGEntityModel, len(entities))
	for i, e := range entities {
		rows[i] = *entityToModel(e)
	}
	// CreateInBatches handles potentially large inserts efficiently.
	if err := db.CreateInBatches(rows, 500).Error; err != nil {
		return fmt.Errorf("insert entities for bundle %s: %w", bundleName, err)
	}
	return nil
}

// DeleteEntity removes a single entity row. Idempotent.
func (r *GORMKGEntityRepository) DeleteEntity(ctx context.Context, tenantID, bundleName, entityType, entityID string) error {
	return r.dbHandle(ctx).
		Where("tenant_id = ? AND bundle_name = ? AND entity_type = ? AND entity_id = ?",
			tenantID, bundleName, entityType, entityID).
		Delete(&models.KGEntityModel{}).Error
}

// GetEntity returns one entity by composite key. Returns (nil, nil) when
// not found.
func (r *GORMKGEntityRepository) GetEntity(ctx context.Context, tenantID, bundleName, entityType, entityID string) (*domain.KGEntity, error) {
	var row models.KGEntityModel
	if err := r.dbHandle(ctx).
		Where("tenant_id = ? AND bundle_name = ? AND entity_type = ? AND entity_id = ?",
			tenantID, bundleName, entityType, entityID).
		First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("get entity: %w", err)
	}
	return entityFromModel(row), nil
}

// ListEntitiesQuery captures all filter inputs for the paginated list.
type ListEntitiesQuery struct {
	TenantID   string
	BundleName string
	EntityType string
	Filters    map[string]any // JSONB @> filter
	Limit      int
	Offset     int
}

// ListEntities returns a paginated list of entities matching the JSONB
// filter. Filters are applied via the @> containment operator backed by the
// generic GIN index, so any set of `x-index` fields is covered.
func (r *GORMKGEntityRepository) ListEntities(ctx context.Context, q ListEntitiesQuery) ([]*domain.KGEntity, int, error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Limit > 500 {
		q.Limit = 500
	}
	if q.Offset < 0 {
		q.Offset = 0
	}

	base := r.dbHandle(ctx).
		Model(&models.KGEntityModel{}).
		Where("tenant_id = ? AND bundle_name = ? AND entity_type = ?",
			q.TenantID, q.BundleName, q.EntityType)

	if len(q.Filters) > 0 {
		filtersJSON, err := json.Marshal(q.Filters)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal filters: %w", err)
		}
		base = base.Where("data @> ?::jsonb", string(filtersJSON))
	}

	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count entities: %w", err)
	}

	var rows []models.KGEntityModel
	if err := base.
		Order("entity_id ASC").
		Limit(q.Limit).
		Offset(q.Offset).
		Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list entities: %w", err)
	}

	out := make([]*domain.KGEntity, 0, len(rows))
	for _, row := range rows {
		out = append(out, entityFromModel(row))
	}
	return out, int(total), nil
}

func entityToModel(e *domain.KGEntity) *models.KGEntityModel {
	return &models.KGEntityModel{
		TenantID:   e.TenantID,
		BundleName: e.BundleName,
		EntityType: e.EntityType,
		EntityID:   e.EntityID,
		Data:       datatypes.JSON(e.Data),
		SchemaHash: e.SchemaHash,
	}
}

func entityFromModel(row models.KGEntityModel) *domain.KGEntity {
	return &domain.KGEntity{
		TenantID:   row.TenantID,
		BundleName: row.BundleName,
		EntityType: row.EntityType,
		EntityID:   row.EntityID,
		Data:       []byte(row.Data),
		SchemaHash: row.SchemaHash,
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
	}
}
