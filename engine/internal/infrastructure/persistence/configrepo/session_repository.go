package configrepo

import (
	"context"
	"errors"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
	"gorm.io/gorm"
)

// GORMSessionRepository implements session CRUD using GORM.
type GORMSessionRepository struct {
	db *gorm.DB
}

// NewGORMSessionRepository creates a new GORMSessionRepository.
func NewGORMSessionRepository(db *gorm.DB) *GORMSessionRepository {
	return &GORMSessionRepository{db: db}
}

// List returns paginated sessions for the tenant sorted by updated_at desc with optional filters.
// The userSub filter matches sessions.user_sub (JWT sub of the end-user).
func (r *GORMSessionRepository) List(ctx context.Context, agentName, userSub, status, from, to string, page, perPage int) ([]models.SessionModel, int64, error) {
	q := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Model(&models.SessionModel{})

	// Q.5: agent_name column dropped from sessions. agentName filter is a no-op.
	_ = agentName
	if userSub != "" {
		q = q.Where("user_sub = ?", userSub)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if from != "" {
		q = q.Where("created_at >= ?", from)
	}
	if to != "" {
		q = q.Where("created_at <= ?", to)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count sessions: %w", err)
	}

	var sessions []models.SessionModel
	offset := (page - 1) * perPage
	if err := q.Order("updated_at DESC").Offset(offset).Limit(perPage).Find(&sessions).Error; err != nil {
		return nil, 0, fmt.Errorf("list sessions: %w", err)
	}

	return sessions, total, nil
}

// Get returns a session by ID, tenant-scoped.
func (r *GORMSessionRepository) Get(ctx context.Context, id string) (*models.SessionModel, error) {
	var session models.SessionModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("id = ?", id).
		First(&session).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("get session: %w", err)
	}
	return &session, nil
}

// Create inserts a new session record, stamping tenant from context.
func (r *GORMSessionRepository) Create(ctx context.Context, session *models.SessionModel) error {
	session.TenantID = tenantIDFromCtx(ctx)
	if err := r.db.WithContext(ctx).Create(session).Error; err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// Update updates session fields by ID, tenant-scoped.
func (r *GORMSessionRepository) Update(ctx context.Context, id string, updates map[string]interface{}) error {
	result := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Model(&models.SessionModel{}).
		Where("id = ?", id).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("update session: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("session not found: %s", id)
	}
	return nil
}

// Delete removes a session by ID (tenant-scoped).
func (r *GORMSessionRepository) Delete(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Delete(&models.SessionModel{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("delete session: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return pkgerrors.NotFound(fmt.Sprintf("session not found: %s", id))
	}
	return nil
}

// TouchUpdatedAt updates the updated_at timestamp for a session (tenant-scoped).
func (r *GORMSessionRepository) TouchUpdatedAt(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Model(&models.SessionModel{}).
		Where("id = ?", id).
		Update("updated_at", gorm.Expr("NOW()"))
	if result.Error != nil {
		return fmt.Errorf("touch session updated_at: %w", result.Error)
	}
	return nil
}

// LastForSchema returns the ID of the most-recently-updated session for the given
// schema and user within the current tenant. Returns ("", nil) when none exists.
func (r *GORMSessionRepository) LastForSchema(ctx context.Context, schemaID, userSub string) (string, error) {
	var session models.SessionModel
	err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("schema_id = ? AND user_sub = ?", schemaID, userSub).
		Order("updated_at DESC").
		First(&session).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("last session for schema: %w", err)
	}
	return session.ID, nil
}

// GetUserSubBySessionID returns the user_sub (JWT sub) for a session
// for ownership checks. Tenant-scoped. Returns ("", false, nil) if session not found.
func (r *GORMSessionRepository) GetUserSubBySessionID(ctx context.Context, sessionID string) (string, bool, error) {
	var m models.SessionModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Select("user_sub").
		First(&m, "id = ?", sessionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get session user: %w", err)
	}
	if m.UserSub == "" {
		return "", false, nil
	}
	return m.UserSub, true, nil
}
