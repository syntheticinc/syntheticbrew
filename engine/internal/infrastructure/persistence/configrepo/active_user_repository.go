package configrepo

import (
	"context"
	"fmt"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"gorm.io/gorm"
)

// GORMActiveUserRepository is the tenant-scoped active-users store. It records
// one last-seen row per (tenant, user_sub) and answers active-in-window
// counts over the last_active_at column.
type GORMActiveUserRepository struct {
	db *gorm.DB
}

// NewGORMActiveUserRepository creates a new GORMActiveUserRepository.
func NewGORMActiveUserRepository(db *gorm.DB) *GORMActiveUserRepository {
	return &GORMActiveUserRepository{db: db}
}

// touchSQL is the atomic upsert that records a user as active now: insert on
// first sight, bump last_active_at on every subsequent touch — one statement,
// so concurrent requests cannot race a check-then-write.
const touchSQL = `
INSERT INTO active_users (tenant_id, user_sub, last_active_at)
VALUES (?, ?, now())
ON CONFLICT (tenant_id, user_sub) DO UPDATE SET last_active_at = now()
`

// Touch marks userSub as active now in the current tenant, creating the row
// on first sight.
func (r *GORMActiveUserRepository) Touch(ctx context.Context, userSub string) error {
	err := r.db.WithContext(ctx).Exec(touchSQL, tenantIDFromCtx(ctx), userSub).Error
	if err != nil {
		return fmt.Errorf("touch active user: %w", err)
	}
	return nil
}

// CountActiveSince returns the number of users in the current tenant whose
// last activity is after since.
func (r *GORMActiveUserRepository) CountActiveSince(ctx context.Context, since time.Time) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&models.ActiveUserModel{}).
		Scopes(tenantScope(ctx)).
		Where("last_active_at > ?", since).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count active users: %w", err)
	}
	return count, nil
}

// IsActiveSince reports whether userSub has been active in the current tenant
// after since.
func (r *GORMActiveUserRepository) IsActiveSince(ctx context.Context, userSub string, since time.Time) (bool, error) {
	var exists bool
	err := r.db.WithContext(ctx).
		Raw(`SELECT EXISTS (
			SELECT 1 FROM active_users
			WHERE tenant_id = ? AND user_sub = ? AND last_active_at > ?
		)`, tenantIDFromCtx(ctx), userSub, since).
		Scan(&exists).Error
	if err != nil {
		return false, fmt.Errorf("check active user: %w", err)
	}
	return exists, nil
}
