package configrepo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// setupOAuthRefreshTestDB creates an in-memory SQLite DB with the
// oauth_refresh_tokens table. TranslateError is enabled so unique-constraint
// violations surface as gorm.ErrDuplicatedKey, exercising the replay path the
// same way GORM would when configured that way.
func setupOAuthRefreshTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
		TranslateError:                           true,
	})
	require.NoError(t, err)

	const ddl = `
CREATE TABLE oauth_refresh_tokens (
	id TEXT PRIMARY KEY,
	tenant_id TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
	user_sub TEXT NOT NULL,
	cid_hash TEXT NOT NULL,
	scope TEXT NOT NULL,
	resource TEXT NOT NULL,
	family_id TEXT NOT NULL,
	token_hash TEXT NOT NULL,
	code_jti TEXT,
	created_at DATETIME,
	expires_at DATETIME NOT NULL,
	revoked_at DATETIME,
	CONSTRAINT uq_oauth_refresh_tokens_token_hash UNIQUE (token_hash),
	CONSTRAINT uq_oauth_refresh_tokens_code_jti UNIQUE (code_jti)
)`
	require.NoError(t, db.Exec(ddl).Error)
	return db
}

func newRefreshToken(familyID, tokenHash, codeJTI string) domain.OAuthRefreshToken {
	return domain.OAuthRefreshToken{
		ID:        uuid.NewString(),
		TenantID:  domain.CETenantID,
		UserSub:   "user-1",
		CidHash:   "cid-hash",
		Scope:     "provision",
		Resource:  "https://engine.example/mcp",
		FamilyID:  familyID,
		TokenHash: tokenHash,
		CodeJTI:   codeJTI,
		ExpiresAt: time.Now().Add(time.Hour),
	}
}

func TestOAuthRefreshTokenRepository_StoreAndGetByHash(t *testing.T) {
	db := setupOAuthRefreshTestDB(t)
	repo := NewGORMOAuthRefreshTokenRepository(db)
	ctx := context.Background()

	family := uuid.NewString()
	tok := newRefreshToken(family, "hash-1", "jti-1")
	require.NoError(t, repo.Store(ctx, tok))

	got, err := repo.GetByHash(ctx, "hash-1")
	require.NoError(t, err)
	assert.Equal(t, family, got.FamilyID)
	assert.Equal(t, "jti-1", got.CodeJTI)
	assert.Equal(t, "user-1", got.UserSub)
	assert.Nil(t, got.RevokedAt)
}

func TestOAuthRefreshTokenRepository_StoreCodeJTIReplay(t *testing.T) {
	db := setupOAuthRefreshTestDB(t)
	repo := NewGORMOAuthRefreshTokenRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.Store(ctx, newRefreshToken(uuid.NewString(), "hash-a", "jti-dup")))

	// Second store of the SAME code_jti (different token) must be flagged.
	err := repo.Store(ctx, newRefreshToken(uuid.NewString(), "hash-b", "jti-dup"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCodeJTIReplayed))
}

func TestOAuthRefreshTokenRepository_StoreNullCodeJTIAllowsMany(t *testing.T) {
	db := setupOAuthRefreshTestDB(t)
	repo := NewGORMOAuthRefreshTokenRepository(db)
	ctx := context.Background()

	// Rotated (non-first) tokens carry no code_jti; several NULLs must coexist.
	require.NoError(t, repo.Store(ctx, newRefreshToken(uuid.NewString(), "hash-1", "")))
	require.NoError(t, repo.Store(ctx, newRefreshToken(uuid.NewString(), "hash-2", "")))
}

func TestOAuthRefreshTokenRepository_RotateRevokeAtomic(t *testing.T) {
	db := setupOAuthRefreshTestDB(t)
	repo := NewGORMOAuthRefreshTokenRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.Store(ctx, newRefreshToken(uuid.NewString(), "hash-x", "jti-x")))

	// First revoke wins.
	n, err := repo.RotateRevoke(ctx, "hash-x")
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	// Second revoke of the now-revoked token affects 0 rows → caller sees reuse.
	n, err = repo.RotateRevoke(ctx, "hash-x")
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
}

func TestOAuthRefreshTokenRepository_RevokeFamily(t *testing.T) {
	db := setupOAuthRefreshTestDB(t)
	repo := NewGORMOAuthRefreshTokenRepository(db)
	ctx := context.Background()

	family := uuid.NewString()
	require.NoError(t, repo.Store(ctx, newRefreshToken(family, "hash-1", "jti-1")))
	require.NoError(t, repo.Store(ctx, newRefreshToken(family, "hash-2", "")))
	require.NoError(t, repo.Store(ctx, newRefreshToken(uuid.NewString(), "hash-other", "jti-2")))

	require.NoError(t, repo.RevokeFamily(ctx, family))

	// Both family members revoked.
	for _, h := range []string{"hash-1", "hash-2"} {
		got, err := repo.GetByHash(ctx, h)
		require.NoError(t, err)
		assert.NotNil(t, got.RevokedAt, "token %s should be revoked", h)
	}
	// Unrelated family untouched.
	other, err := repo.GetByHash(ctx, "hash-other")
	require.NoError(t, err)
	assert.Nil(t, other.RevokedAt)
}

func TestOAuthRefreshTokenRepository_FindFamilyByCodeJTI(t *testing.T) {
	db := setupOAuthRefreshTestDB(t)
	repo := NewGORMOAuthRefreshTokenRepository(db)
	ctx := context.Background()

	family := uuid.NewString()
	require.NoError(t, repo.Store(ctx, newRefreshToken(family, "hash-1", "jti-find")))

	got, err := repo.FindFamilyByCodeJTI(ctx, "jti-find")
	require.NoError(t, err)
	assert.Equal(t, family, got)
}
