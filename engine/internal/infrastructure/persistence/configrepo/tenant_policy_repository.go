package configrepo

import (
	"context"
	"errors"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GORMTenantPolicyRepository is the tenant-scoped policy store implementation.
// It stores protected per-tenant key/value entries, one row per
// (tenant_id, key). No HTTP handler exposes this table — writes come only
// through the plugin seam.
type GORMTenantPolicyRepository struct {
	db *gorm.DB
}

// NewGORMTenantPolicyRepository creates a new GORMTenantPolicyRepository.
func NewGORMTenantPolicyRepository(db *gorm.DB) *GORMTenantPolicyRepository {
	return &GORMTenantPolicyRepository{db: db}
}

// Get returns the policy for a key in the current tenant, or (nil, nil) when
// none is configured.
func (r *GORMTenantPolicyRepository) Get(ctx context.Context, key string) (*domain.TenantPolicy, error) {
	var m models.TenantPolicyModel
	err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("key = ?", key).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get tenant policy %q: %w", key, err)
	}
	return &domain.TenantPolicy{Key: m.Key, Value: m.Value}, nil
}

// GetMany returns the values for the requested keys in the current tenant as
// a key→value map, in a single query. Keys with no configured policy are
// simply absent from the map.
func (r *GORMTenantPolicyRepository) GetMany(ctx context.Context, keys []string) (map[string]string, error) {
	if len(keys) == 0 {
		return map[string]string{}, nil
	}
	var rows []models.TenantPolicyModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("key IN ?", keys).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("get tenant policies: %w", err)
	}
	values := make(map[string]string, len(rows))
	for _, m := range rows {
		values[m.Key] = m.Value
	}
	return values, nil
}

// Set upserts the policy for its key, stamping the tenant from context.
//
// The row is built as an explicit column map (not the struct) so a zero-value
// Value="" is written verbatim: the model's empty-string default tag would make GORM
// drop an empty string from the struct INSERT and let the column default win.
// The map path has no struct-default interference, and the ON CONFLICT branch
// assigns from the declared value (not excluded.*) for the same reason.
func (r *GORMTenantPolicyRepository) Set(ctx context.Context, p domain.TenantPolicy) error {
	row := map[string]interface{}{
		"tenant_id": tenantIDFromCtx(ctx),
		"key":       p.Key,
		"value":     p.Value,
	}
	err := r.db.WithContext(ctx).
		Table(models.TenantPolicyModel{}.TableName()).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "tenant_id"}, {Name: "key"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"value":      p.Value,
				"updated_at": gorm.Expr("now()"),
			}),
		}).Create(row).Error
	if err != nil {
		return fmt.Errorf("set tenant policy %q: %w", p.Key, err)
	}
	return nil
}

// Delete removes the policy for a key in the current tenant. Removing an
// absent policy is not an error.
func (r *GORMTenantPolicyRepository) Delete(ctx context.Context, key string) error {
	err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("key = ?", key).
		Delete(&models.TenantPolicyModel{}).Error
	if err != nil {
		return fmt.Errorf("delete tenant policy %q: %w", key, err)
	}
	return nil
}

// List returns all configured policies for the current tenant.
func (r *GORMTenantPolicyRepository) List(ctx context.Context) ([]domain.TenantPolicy, error) {
	var rows []models.TenantPolicyModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Order("key").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list tenant policies: %w", err)
	}
	policies := make([]domain.TenantPolicy, 0, len(rows))
	for _, m := range rows {
		policies = append(policies, domain.TenantPolicy{Key: m.Key, Value: m.Value})
	}
	return policies, nil
}
