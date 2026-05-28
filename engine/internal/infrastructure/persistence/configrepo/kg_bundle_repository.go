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

// GORMKGBundleRepository implements bundle CRUD using GORM. One struct
// satisfies the consumer-side interfaces of kgapply, kgread, kgmutate, and
// any future package that needs to operate on bundles.
type GORMKGBundleRepository struct {
	db *gorm.DB
}

// NewGORMKGBundleRepository creates a new GORMKGBundleRepository.
func NewGORMKGBundleRepository(db *gorm.DB) *GORMKGBundleRepository {
	return &GORMKGBundleRepository{db: db}
}

// dbHandle returns the transaction-bound *gorm.DB if the context carries one,
// otherwise the repository's base handle. Used to participate in usecase-level
// transactions managed by GORMTransactionRunner.
func (r *GORMKGBundleRepository) dbHandle(ctx context.Context) *gorm.DB {
	if tx, ok := txFromContext(ctx); ok {
		return tx
	}
	return r.db.WithContext(ctx)
}

// UpsertBundle creates or updates a bundle row. Tenant id is sourced from
// the context (or CETenantID fallback for CE single-tenant).
func (r *GORMKGBundleRepository) UpsertBundle(ctx context.Context, b *domain.KGBundle) error {
	manifestJSON, err := json.Marshal(b.Manifest)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	model := &models.KGBundleModel{
		TenantID:   tenantIDFromCtx(ctx),
		BundleName: b.BundleName,
		Version:    b.Version,
		Manifest:   datatypes.JSON(manifestJSON),
	}

	return r.dbHandle(ctx).
		Save(model).Error
}

// DeleteBundle removes a bundle row (and via FK CASCADE all schemas + entities).
// Idempotent: returns nil even if the bundle does not exist.
func (r *GORMKGBundleRepository) DeleteBundle(ctx context.Context, tenantID, bundleName string) error {
	return r.dbHandle(ctx).
		Where("tenant_id = ? AND bundle_name = ?", tenantID, bundleName).
		Delete(&models.KGBundleModel{}).Error
}

// List returns all bundles for the calling tenant.
func (r *GORMKGBundleRepository) List(ctx context.Context, tenantID string) ([]*domain.KGBundle, error) {
	var rows []models.KGBundleModel
	if err := r.dbHandle(ctx).
		Where("tenant_id = ?", tenantID).
		Order("bundle_name ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list bundles: %w", err)
	}

	out := make([]*domain.KGBundle, 0, len(rows))
	for _, row := range rows {
		b, err := bundleFromModel(row)
		if err != nil {
			return nil, fmt.Errorf("decode bundle %s: %w", row.BundleName, err)
		}
		out = append(out, b)
	}
	return out, nil
}

// Get returns a single bundle by name. Returns (nil, nil) when not found.
func (r *GORMKGBundleRepository) Get(ctx context.Context, tenantID, bundleName string) (*domain.KGBundle, error) {
	return r.GetBundle(ctx, tenantID, bundleName)
}

// GetBundle returns a single bundle (alias used by the kgmutate consumer
// interface). Returns (nil, nil) when not found.
func (r *GORMKGBundleRepository) GetBundle(ctx context.Context, tenantID, bundleName string) (*domain.KGBundle, error) {
	var row models.KGBundleModel
	if err := r.dbHandle(ctx).
		Where("tenant_id = ? AND bundle_name = ?", tenantID, bundleName).
		First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("get bundle: %w", err)
	}
	return bundleFromModel(row)
}

// CountEntities returns the total entity count and total JSONB byte size for
// a bundle. Used by quota enforcement at bundle delete time.
func (r *GORMKGBundleRepository) CountEntities(ctx context.Context, tenantID, bundleName string) (int, int64, error) {
	var stats struct {
		Cnt   int64
		Bytes int64
	}
	if err := r.dbHandle(ctx).
		Table("kg_entity").
		Where("tenant_id = ? AND bundle_name = ?", tenantID, bundleName).
		Select("COUNT(*) AS cnt, COALESCE(SUM(octet_length(data::text)), 0) AS bytes").
		Scan(&stats).Error; err != nil {
		return 0, 0, fmt.Errorf("count entities: %w", err)
	}
	return int(stats.Cnt), stats.Bytes, nil
}

// bundleFromModel maps a GORM row to a domain.KGBundle.
func bundleFromModel(row models.KGBundleModel) (*domain.KGBundle, error) {
	manifest := make(map[string]any)
	if len(row.Manifest) > 0 {
		if err := json.Unmarshal(row.Manifest, &manifest); err != nil {
			return nil, fmt.Errorf("unmarshal manifest: %w", err)
		}
	}
	return &domain.KGBundle{
		TenantID:   row.TenantID,
		BundleName: row.BundleName,
		Version:    row.Version,
		Manifest:   manifest,
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
	}, nil
}
