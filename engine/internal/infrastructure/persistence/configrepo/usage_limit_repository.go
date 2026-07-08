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

// GORMUsageLimitRepository is the tenant-scoped ConfigStore implementation. It
// stores operator-declared usage limits, one row per (tenant_id, scope).
type GORMUsageLimitRepository struct {
	db *gorm.DB
}

// NewGORMUsageLimitRepository creates a new GORMUsageLimitRepository.
func NewGORMUsageLimitRepository(db *gorm.DB) *GORMUsageLimitRepository {
	return &GORMUsageLimitRepository{db: db}
}

// Get returns the limit for a scope in the current tenant, or (nil, nil) when
// none is configured.
func (r *GORMUsageLimitRepository) Get(ctx context.Context, scope string) (*domain.UsageLimit, error) {
	var m models.UsageLimitConfigModel
	err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("scope = ?", scope).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get usage limit %q: %w", scope, err)
	}
	limit := toUsageLimit(m)
	return &limit, nil
}

// Set upserts the limit for its scope, stamping the tenant from context.
//
// The row is built as an explicit column map (not the struct) so a zero-value
// Enabled=false is written verbatim: the model's `default:true` tag would make
// GORM drop a false bool from the struct INSERT and let the column default win.
// The map path has no struct-default interference, and the ON CONFLICT branch
// assigns from the declared values (not excluded.*) for the same reason.
func (r *GORMUsageLimitRepository) Set(ctx context.Context, limit domain.UsageLimit) error {
	row := map[string]interface{}{
		"tenant_id":        tenantIDFromCtx(ctx),
		"scope":            limit.Scope,
		"unit":             limit.Unit,
		"limit_value":      limit.LimitValue,
		"interval_seconds": limit.IntervalSeconds,
		"enabled":          limit.Enabled,
	}
	err := r.db.WithContext(ctx).
		Table(models.UsageLimitConfigModel{}.TableName()).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "tenant_id"}, {Name: "scope"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"unit":             limit.Unit,
				"limit_value":      limit.LimitValue,
				"interval_seconds": limit.IntervalSeconds,
				"enabled":          limit.Enabled,
				"updated_at":       gorm.Expr("now()"),
			}),
		}).Create(row).Error
	if err != nil {
		return fmt.Errorf("set usage limit %q: %w", limit.Scope, err)
	}
	return nil
}

// CreateIfAbsent inserts the limit for its scope only when no row exists for
// (tenant, scope), stamping the tenant from context. ON CONFLICT DO NOTHING
// makes "insert only if absent" atomic — no check-then-act window — so an
// existing (e.g. operator-raised) limit is never overwritten. Returns whether a
// row was actually inserted.
//
// Built as an explicit column map for the same zero-value-Enabled reason as Set.
func (r *GORMUsageLimitRepository) CreateIfAbsent(ctx context.Context, limit domain.UsageLimit) (bool, error) {
	row := map[string]interface{}{
		"tenant_id":        tenantIDFromCtx(ctx),
		"scope":            limit.Scope,
		"unit":             limit.Unit,
		"limit_value":      limit.LimitValue,
		"interval_seconds": limit.IntervalSeconds,
		"enabled":          limit.Enabled,
	}
	res := r.db.WithContext(ctx).
		Table(models.UsageLimitConfigModel{}.TableName()).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "scope"}},
			DoNothing: true,
		}).Create(row)
	if res.Error != nil {
		return false, fmt.Errorf("create usage limit %q if absent: %w", limit.Scope, res.Error)
	}
	return res.RowsAffected > 0, nil
}

// Delete removes the limit for a scope in the current tenant. Removing an
// absent limit is not an error.
func (r *GORMUsageLimitRepository) Delete(ctx context.Context, scope string) error {
	err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("scope = ?", scope).
		Delete(&models.UsageLimitConfigModel{}).Error
	if err != nil {
		return fmt.Errorf("delete usage limit %q: %w", scope, err)
	}
	return nil
}

// List returns all configured limits for the current tenant.
func (r *GORMUsageLimitRepository) List(ctx context.Context) ([]domain.UsageLimit, error) {
	var rows []models.UsageLimitConfigModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Order("scope").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list usage limits: %w", err)
	}
	limits := make([]domain.UsageLimit, 0, len(rows))
	for _, m := range rows {
		limits = append(limits, toUsageLimit(m))
	}
	return limits, nil
}

// toUsageLimit maps the persistence model to the pure domain entity.
func toUsageLimit(m models.UsageLimitConfigModel) domain.UsageLimit {
	return domain.UsageLimit{
		Scope:           m.Scope,
		Unit:            m.Unit,
		LimitValue:      m.LimitValue,
		IntervalSeconds: m.IntervalSeconds,
		Enabled:         m.Enabled,
	}
}
