package configrepo

import (
	"context"
	"fmt"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"gorm.io/gorm"
)

// GORMAPITokenRepository implements API token persistence using GORM.
type GORMAPITokenRepository struct {
	db *gorm.DB
}

// NewGORMAPITokenRepository creates a new GORMAPITokenRepository.
func NewGORMAPITokenRepository(db *gorm.DB) *GORMAPITokenRepository {
	return &GORMAPITokenRepository{db: db}
}

// Create inserts a new API token and returns its ID.
// Stamps tenant_id from context so tokens are tenant-scoped. userSub is the
// JWT `sub` of the admin/user creating the token (no FK — external identity).
func (r *GORMAPITokenRepository) Create(ctx context.Context, userSub, name, tokenHash string, scopesMask int) (string, error) {
	m := models.APITokenModel{
		UserSub:    userSub,
		Name:       name,
		TokenHash:  tokenHash,
		ScopesMask: scopesMask,
		TenantID:   tenantIDFromCtx(ctx),
	}
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		return "", fmt.Errorf("create api token: %w", err)
	}
	return m.ID, nil
}

// List returns all non-revoked API tokens for the current tenant.
func (r *GORMAPITokenRepository) List(ctx context.Context) ([]APITokenInfo, error) {
	var rows []models.APITokenModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("revoked_at IS NULL").
		Order("created_at DESC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list api tokens: %w", err)
	}

	result := make([]APITokenInfo, 0, len(rows))
	for _, m := range rows {
		result = append(result, APITokenInfo{
			ID:         m.ID,
			Name:       m.Name,
			ScopesMask: m.ScopesMask,
			CreatedAt:  m.CreatedAt,
			LastUsedAt: m.LastUsedAt,
		})
	}
	return result, nil
}

// Delete soft-revokes an API token by ID, scoped to the current tenant.
func (r *GORMAPITokenRepository) Delete(ctx context.Context, id string) error {
	now := time.Now()
	result := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Model(&models.APITokenModel{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Update("revoked_at", now)
	if result.Error != nil {
		return fmt.Errorf("revoke api token %s: %w", id, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("api token not found: %s", id)
	}
	return nil
}

// VerifiedToken is the repository-level result of a successful token lookup.
// Delivery adapters map this into their own DTOs (e.g. http.APITokenInfo).
type VerifiedToken struct {
	Name       string
	ScopesMask int
	TenantID   string
}

// VerifyToken looks up an API token by its SHA-256 hash.
// Only non-revoked tokens are considered valid. Updates last_used_at on success.
func (r *GORMAPITokenRepository) VerifyToken(ctx context.Context, tokenHash string) (VerifiedToken, error) {
	var m models.APITokenModel
	if err := r.db.WithContext(ctx).Where("token_hash = ? AND revoked_at IS NULL", tokenHash).First(&m).Error; err != nil {
		return VerifiedToken{}, fmt.Errorf("token not found")
	}

	// Update last_used_at asynchronously (best-effort)
	now := time.Now()
	r.db.WithContext(ctx).Model(&m).Update("last_used_at", now)

	return VerifiedToken{
		Name:       m.Name,
		ScopesMask: m.ScopesMask,
		TenantID:   m.TenantID,
	}, nil
}

// APITokenInfo is a token record returned by List (no raw token value).
type APITokenInfo struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	ScopesMask int        `json:"scopes_mask"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}
