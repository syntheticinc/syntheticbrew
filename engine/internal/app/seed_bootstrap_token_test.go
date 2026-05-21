package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/authprim"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
)

// setupTokenTestDB creates an in-memory SQLite DB with only the api_tokens table.
func setupTokenTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	db.Callback().Create().Before("gorm:create").Register("token_test:uuid", func(tx *gorm.DB) {
		if tx.Statement.Schema == nil {
			return
		}
		for _, field := range tx.Statement.Schema.PrimaryFields {
			if field.DBName == "id" {
				val, isZero := field.ValueOf(tx.Statement.Context, tx.Statement.ReflectValue)
				if isZero || val == nil || val == "" {
					_ = field.Set(tx.Statement.Context, tx.Statement.ReflectValue, uuid.New().String())
				}
			}
		}
	})

	require.NoError(t, db.Exec(`CREATE TABLE api_tokens (
		id TEXT PRIMARY KEY,
		user_sub TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL UNIQUE,
		token_hash TEXT NOT NULL UNIQUE,
		scopes_mask INTEGER NOT NULL DEFAULT 0,
		tenant_id TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
		created_at DATETIME,
		last_used_at DATETIME,
		revoked_at DATETIME
	)`).Error)

	return db
}

func validToken() string {
	// bb_ + 64 hex chars = 67 chars total
	return "bb_" + hex.EncodeToString(make([]byte, 32))
}

func TestSeedBootstrapAdminToken_EmptyToken_NoOp(t *testing.T) {
	db := setupTokenTestDB(t)
	ctx := context.Background()

	require.NoError(t, seedBootstrapAdminToken(ctx, db, ""))

	repo := configrepo.NewGORMAPITokenRepository(db)
	tokens, err := repo.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, tokens, "empty token must not create any rows")
}

func TestSeedBootstrapAdminToken_InvalidFormat_ReturnsError(t *testing.T) {
	db := setupTokenTestDB(t)
	ctx := context.Background()

	for _, bad := range []string{"xxx", "bb_short", "aa_" + hex.EncodeToString(make([]byte, 32))} {
		err := seedBootstrapAdminToken(ctx, db, bad)
		require.Error(t, err, "invalid token %q must return error to fail-fast at boot", bad)
		assert.True(t, errors.Is(err, authprim.ErrInvalidTokenFormat),
			"error must wrap authprim.ErrInvalidTokenFormat for stable matching")
	}

	repo := configrepo.NewGORMAPITokenRepository(db)
	tokens, err := repo.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, tokens, "invalid tokens must not create any rows")
}

func TestSeedBootstrapAdminToken_ValidToken_CreatesRow(t *testing.T) {
	db := setupTokenTestDB(t)
	ctx := context.Background()

	raw := validToken()
	require.NoError(t, seedBootstrapAdminToken(ctx, db, raw))

	repo := configrepo.NewGORMAPITokenRepository(db)
	tokens, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	assert.Equal(t, bootstrapAdminTokenName, tokens[0].Name)
	assert.Equal(t, scopeAdmin, tokens[0].ScopesMask)

	// Verify hash stored matches sha256 of raw token.
	hash := sha256.Sum256([]byte(raw))
	expectedHash := hex.EncodeToString(hash[:])
	// List does not return hash — verify via direct DB query.
	var storedHash string
	require.NoError(t, db.Raw("SELECT token_hash FROM api_tokens WHERE name = ?", bootstrapAdminTokenName).Scan(&storedHash).Error)
	assert.Equal(t, expectedHash, storedHash)
}

func TestSeedBootstrapAdminToken_Idempotent(t *testing.T) {
	db := setupTokenTestDB(t)
	ctx := context.Background()

	raw := validToken()

	// Call twice — must not duplicate.
	require.NoError(t, seedBootstrapAdminToken(ctx, db, raw))
	require.NoError(t, seedBootstrapAdminToken(ctx, db, raw))

	repo := configrepo.NewGORMAPITokenRepository(db)
	tokens, err := repo.List(ctx)
	require.NoError(t, err)
	assert.Len(t, tokens, 1, "second seed call must not duplicate the token")
}
