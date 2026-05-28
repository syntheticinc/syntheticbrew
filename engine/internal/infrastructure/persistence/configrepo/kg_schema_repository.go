package configrepo

import (
	"context"
	"errors"
	"fmt"

	"github.com/lib/pq"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// GORMKGSchemaRepository implements entity-schema CRUD using GORM.
type GORMKGSchemaRepository struct {
	db *gorm.DB
}

func NewGORMKGSchemaRepository(db *gorm.DB) *GORMKGSchemaRepository {
	return &GORMKGSchemaRepository{db: db}
}

func (r *GORMKGSchemaRepository) dbHandle(ctx context.Context) *gorm.DB {
	if tx, ok := txFromContext(ctx); ok {
		return tx
	}
	return r.db.WithContext(ctx)
}

// UpsertSchema creates or updates a single schema row.
func (r *GORMKGSchemaRepository) UpsertSchema(ctx context.Context, s *domain.KGEntitySchema) error {
	model := &models.KGEntitySchemaModel{
		TenantID:        s.TenantID,
		BundleName:      s.BundleName,
		EntityType:      s.EntityType,
		SchemaJSON:      datatypes.JSON(s.SchemaJSON),
		SchemaHash:      s.SchemaHash,
		IDField:         s.IDField,
		ExposeTools:     pq.StringArray(s.ExposeTools),
		ToolDescription: s.ToolDescription,
	}
	return r.dbHandle(ctx).Save(model).Error
}

// UpsertSchemas bulk-upserts schemas inside a (tenant_id, bundle_name) scope.
// Replaces existing rows by primary key. Called by kgapply during atomic apply.
func (r *GORMKGSchemaRepository) UpsertSchemas(
	ctx context.Context,
	tenantID, bundleName string,
	schemas []*domain.KGEntitySchema,
) error {
	if len(schemas) == 0 {
		return nil
	}
	db := r.dbHandle(ctx)
	for _, s := range schemas {
		if s.TenantID != tenantID || s.BundleName != bundleName {
			return fmt.Errorf("schema tenant/bundle mismatch: got %s/%s, want %s/%s",
				s.TenantID, s.BundleName, tenantID, bundleName)
		}
		if err := r.upsertOne(db, s); err != nil {
			return fmt.Errorf("upsert schema %s: %w", s.EntityType, err)
		}
	}
	return nil
}

func (r *GORMKGSchemaRepository) upsertOne(db *gorm.DB, s *domain.KGEntitySchema) error {
	model := &models.KGEntitySchemaModel{
		TenantID:        s.TenantID,
		BundleName:      s.BundleName,
		EntityType:      s.EntityType,
		SchemaJSON:      datatypes.JSON(s.SchemaJSON),
		SchemaHash:      s.SchemaHash,
		IDField:         s.IDField,
		ExposeTools:     pq.StringArray(s.ExposeTools),
		ToolDescription: s.ToolDescription,
	}
	return db.Save(model).Error
}

// ListByBundle returns all schemas for a bundle.
func (r *GORMKGSchemaRepository) ListByBundle(ctx context.Context, tenantID, bundleName string) ([]*domain.KGEntitySchema, error) {
	var rows []models.KGEntitySchemaModel
	if err := r.dbHandle(ctx).
		Where("tenant_id = ? AND bundle_name = ?", tenantID, bundleName).
		Order("entity_type ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list schemas: %w", err)
	}
	out := make([]*domain.KGEntitySchema, 0, len(rows))
	for _, row := range rows {
		out = append(out, schemaFromModel(row))
	}
	return out, nil
}

// ListAllExceptBundle returns every schema in the tenant EXCEPT those belonging
// to the named bundle. Used by the cross-bundle collision detector so that
// re-applying a bundle does not flag its own existing schemas as collisions.
// If excludeBundle is empty, all schemas in the tenant are returned.
func (r *GORMKGSchemaRepository) ListAllExceptBundle(ctx context.Context, tenantID, excludeBundle string) ([]*domain.KGEntitySchema, error) {
	query := r.dbHandle(ctx).Where("tenant_id = ?", tenantID)
	if excludeBundle != "" {
		query = query.Where("bundle_name <> ?", excludeBundle)
	}
	var rows []models.KGEntitySchemaModel
	if err := query.Order("bundle_name, entity_type ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list schemas across tenant: %w", err)
	}
	out := make([]*domain.KGEntitySchema, 0, len(rows))
	for _, row := range rows {
		out = append(out, schemaFromModel(row))
	}
	return out, nil
}

// Get returns one schema by (tenant, bundle, entity_type). Returns (nil, nil)
// when not found.
func (r *GORMKGSchemaRepository) Get(ctx context.Context, tenantID, bundleName, entityType string) (*domain.KGEntitySchema, error) {
	return r.GetSchema(ctx, tenantID, bundleName, entityType)
}

// GetSchema alias used by the kgmutate consumer interface.
func (r *GORMKGSchemaRepository) GetSchema(ctx context.Context, tenantID, bundleName, entityType string) (*domain.KGEntitySchema, error) {
	var row models.KGEntitySchemaModel
	if err := r.dbHandle(ctx).
		Where("tenant_id = ? AND bundle_name = ? AND entity_type = ?", tenantID, bundleName, entityType).
		First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("get schema: %w", err)
	}
	return schemaFromModel(row), nil
}

func schemaFromModel(row models.KGEntitySchemaModel) *domain.KGEntitySchema {
	return &domain.KGEntitySchema{
		TenantID:        row.TenantID,
		BundleName:      row.BundleName,
		EntityType:      row.EntityType,
		SchemaJSON:      []byte(row.SchemaJSON),
		SchemaHash:      row.SchemaHash,
		IDField:         row.IDField,
		ExposeTools:     []string(row.ExposeTools),
		ToolDescription: row.ToolDescription,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
}
