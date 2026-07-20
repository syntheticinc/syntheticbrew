package configrepo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// ErrCodeJTIReplayed is returned by Store when an insert collides with the
// unique code_jti constraint — i.e. the same authorization code is being
// exchanged a second time. Callers treat it as a replay signal and revoke the
// implicated token family. It aliases the domain sentinel so the token usecase
// can match it (via errors.Is) without importing this persistence package.
var ErrCodeJTIReplayed = domain.ErrOAuthCodeReplayed

// codeJTIConstraint is the unique-constraint name enforcing one refresh-token
// family per authorization code. Matched by name because the production DB is
// opened without GORM's TranslateError, so the raw driver error is what surfaces.
const codeJTIConstraint = "uq_oauth_refresh_tokens_code_jti"

// GORMOAuthRefreshTokenRepository persists OAuth refresh tokens using GORM.
type GORMOAuthRefreshTokenRepository struct {
	db *gorm.DB
}

// NewGORMOAuthRefreshTokenRepository creates a GORMOAuthRefreshTokenRepository.
func NewGORMOAuthRefreshTokenRepository(db *gorm.DB) *GORMOAuthRefreshTokenRepository {
	return &GORMOAuthRefreshTokenRepository{db: db}
}

// Store inserts a refresh-token record. A collision on the unique code_jti
// constraint is surfaced as ErrCodeJTIReplayed so the caller can react to an
// authorization-code replay; any other error is wrapped as-is.
func (r *GORMOAuthRefreshTokenRepository) Store(ctx context.Context, t domain.OAuthRefreshToken) error {
	m := models.OAuthRefreshTokenModel{
		ID:        t.ID,
		TenantID:  t.TenantID,
		UserSub:   t.UserSub,
		CidHash:   t.CidHash,
		Scope:     t.Scope,
		Resource:  t.Resource,
		FamilyID:  t.FamilyID,
		TokenHash: t.TokenHash,
		ExpiresAt: t.ExpiresAt,
		RevokedAt: t.RevokedAt,
	}
	if t.CodeJTI != "" {
		jti := t.CodeJTI
		m.CodeJTI = &jti
	}
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		if isCodeJTIReplay(err) {
			return ErrCodeJTIReplayed
		}
		return fmt.Errorf("store oauth refresh token: %w", err)
	}
	return nil
}

// GetByHash looks up a refresh token by its SHA-256 hash. Token hashes are
// globally unique, so no tenant scoping is applied to the lookup.
func (r *GORMOAuthRefreshTokenRepository) GetByHash(ctx context.Context, tokenHash string) (domain.OAuthRefreshToken, error) {
	var m models.OAuthRefreshTokenModel
	if err := r.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&m).Error; err != nil {
		return domain.OAuthRefreshToken{}, fmt.Errorf("get oauth refresh token by hash: %w", err)
	}
	return toDomainRefreshToken(m), nil
}

// RotateRevoke atomically revokes a single refresh token by hash, but only if
// it is not already revoked. It returns the number of rows affected: a caller
// that sees 0 must treat the token as reuse of an already-rotated credential.
func (r *GORMOAuthRefreshTokenRepository) RotateRevoke(ctx context.Context, tokenHash string) (int64, error) {
	res := r.db.WithContext(ctx).
		Model(&models.OAuthRefreshTokenModel{}).
		Where("token_hash = ? AND revoked_at IS NULL", tokenHash).
		Update("revoked_at", time.Now())
	if res.Error != nil {
		return 0, fmt.Errorf("rotate-revoke oauth refresh token: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// RevokeFamily revokes every non-revoked token in a rotation family. Used when
// a reuse is detected to invalidate the entire chain at once.
func (r *GORMOAuthRefreshTokenRepository) RevokeFamily(ctx context.Context, familyID string) error {
	if err := r.db.WithContext(ctx).
		Model(&models.OAuthRefreshTokenModel{}).
		Where("family_id = ? AND revoked_at IS NULL", familyID).
		Update("revoked_at", time.Now()).Error; err != nil {
		return fmt.Errorf("revoke oauth refresh token family: %w", err)
	}
	return nil
}

// FindFamilyByCodeJTI returns the family ID of the token minted from the given
// authorization-code JTI, so a code-replay can be traced to the family it
// already spawned and revoked.
func (r *GORMOAuthRefreshTokenRepository) FindFamilyByCodeJTI(ctx context.Context, codeJTI string) (string, error) {
	var m models.OAuthRefreshTokenModel
	if err := r.db.WithContext(ctx).Where("code_jti = ?", codeJTI).First(&m).Error; err != nil {
		return "", fmt.Errorf("find oauth refresh token family by code_jti: %w", err)
	}
	return m.FamilyID, nil
}

func toDomainRefreshToken(m models.OAuthRefreshTokenModel) domain.OAuthRefreshToken {
	var codeJTI string
	if m.CodeJTI != nil {
		codeJTI = *m.CodeJTI
	}
	return domain.OAuthRefreshToken{
		ID:        m.ID,
		TenantID:  m.TenantID,
		UserSub:   m.UserSub,
		CidHash:   m.CidHash,
		Scope:     m.Scope,
		Resource:  m.Resource,
		FamilyID:  m.FamilyID,
		TokenHash: m.TokenHash,
		CodeJTI:   codeJTI,
		CreatedAt: m.CreatedAt,
		ExpiresAt: m.ExpiresAt,
		RevokedAt: m.RevokedAt,
	}
}

// isCodeJTIReplay reports whether err is a unique-constraint violation on the
// code_jti column. It matches both GORM's translated sentinel (used when the
// DB is opened with TranslateError, e.g. in tests) and the raw driver message
// carrying the constraint name (production Postgres, opened without it).
func isCodeJTIReplay(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	return strings.Contains(err.Error(), codeJTIConstraint)
}
