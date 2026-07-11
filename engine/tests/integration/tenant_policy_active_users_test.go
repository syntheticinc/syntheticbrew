//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
)

// otherTenantID is a second tenant used for isolation assertions.
const otherTenantID = "00000000-0000-0000-0000-000000000002"

// cleanPolicyTables removes policy/activity rows so each subtest starts clean.
// These tables are not in the shared truncate list, so the test owns its reset.
func cleanPolicyTables(t *testing.T) {
	t.Helper()
	require.NoError(t, testDB.Exec("DELETE FROM tenant_policies").Error)
	require.NoError(t, testDB.Exec("DELETE FROM active_users").Error)
}

// TestTenantPolicy_Repository exercises the real GORM policy store against the
// migrated database (migration 016 applied by the suite).
func TestTenantPolicy_Repository(t *testing.T) {
	requireSuite(t)

	ctx := domain.WithTenantID(context.Background(), ceTenantID)
	policies := configrepo.NewGORMTenantPolicyRepository(testDB)

	t.Run("set get and upsert overwrite", func(t *testing.T) {
		cleanPolicyTables(t)

		require.NoError(t, policies.Set(ctx, domain.TenantPolicy{Key: domain.PolicyActiveUsersLimit, Value: "100"}))
		got, err := policies.Get(ctx, domain.PolicyActiveUsersLimit)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "100", got.Value)

		// Upsert overwrites — a subsequent Set must replace the previous value.
		require.NoError(t, policies.Set(ctx, domain.TenantPolicy{Key: domain.PolicyActiveUsersLimit, Value: "2000"}))
		got, err = policies.Get(ctx, domain.PolicyActiveUsersLimit)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "2000", got.Value)
	})

	t.Run("get absent returns nil nil", func(t *testing.T) {
		cleanPolicyTables(t)

		got, err := policies.Get(ctx, domain.PolicySystemPromptPrefix)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("getmany returns only present keys in one query", func(t *testing.T) {
		cleanPolicyTables(t)

		require.NoError(t, policies.Set(ctx, domain.TenantPolicy{Key: domain.PolicyActiveUsersLimit, Value: "100"}))
		require.NoError(t, policies.Set(ctx, domain.TenantPolicy{Key: domain.PolicyActiveUsersMode, Value: domain.ActiveUsersModeEnforce}))

		values, err := policies.GetMany(ctx, []string{
			domain.PolicyActiveUsersLimit,
			domain.PolicyActiveUsersMode,
			domain.PolicySystemPromptPrefix, // absent
		})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{
			domain.PolicyActiveUsersLimit: "100",
			domain.PolicyActiveUsersMode:  domain.ActiveUsersModeEnforce,
		}, values)
	})

	t.Run("delete removes and is idempotent", func(t *testing.T) {
		cleanPolicyTables(t)

		require.NoError(t, policies.Set(ctx, domain.TenantPolicy{Key: domain.PolicyWidgetAttribution, Value: "on"}))
		require.NoError(t, policies.Delete(ctx, domain.PolicyWidgetAttribution))
		got, err := policies.Get(ctx, domain.PolicyWidgetAttribution)
		require.NoError(t, err)
		assert.Nil(t, got)

		// Deleting an absent policy is not an error.
		require.NoError(t, policies.Delete(ctx, domain.PolicyWidgetAttribution))
	})

	t.Run("tenant isolation", func(t *testing.T) {
		cleanPolicyTables(t)

		otherCtx := domain.WithTenantID(context.Background(), otherTenantID)
		require.NoError(t, policies.Set(ctx, domain.TenantPolicy{Key: domain.PolicyActiveUsersLimit, Value: "100"}))
		require.NoError(t, policies.Set(otherCtx, domain.TenantPolicy{Key: domain.PolicyActiveUsersLimit, Value: "9"}))

		got, err := policies.Get(ctx, domain.PolicyActiveUsersLimit)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "100", got.Value, "tenant A must not see tenant B's value")

		got, err = policies.Get(otherCtx, domain.PolicyActiveUsersLimit)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "9", got.Value)
	})
}

// TestActiveUsers_Repository exercises the real GORM activity store against
// the migrated database (migration 016 applied by the suite).
func TestActiveUsers_Repository(t *testing.T) {
	requireSuite(t)

	ctx := domain.WithTenantID(context.Background(), ceTenantID)
	activity := configrepo.NewGORMActiveUserRepository(testDB)
	windowStart := func() time.Time {
		return time.Now().Add(-time.Duration(domain.ActiveUsersWindowSeconds) * time.Second)
	}

	t.Run("touch inserts then refreshes last_active_at", func(t *testing.T) {
		cleanPolicyTables(t)

		require.NoError(t, activity.Touch(ctx, "visitor-1"))
		active, err := activity.IsActiveSince(ctx, "visitor-1", windowStart())
		require.NoError(t, err)
		assert.True(t, active)

		// Backdate, then Touch must refresh the row (upsert on PK).
		require.NoError(t, testDB.Exec(
			"UPDATE active_users SET last_active_at = now() - interval '40 days' WHERE user_sub = ?",
			"visitor-1").Error)
		active, err = activity.IsActiveSince(ctx, "visitor-1", windowStart())
		require.NoError(t, err)
		assert.False(t, active, "backdated row must fall outside the window")

		require.NoError(t, activity.Touch(ctx, "visitor-1"))
		active, err = activity.IsActiveSince(ctx, "visitor-1", windowStart())
		require.NoError(t, err)
		assert.True(t, active, "touch must bring the user back into the window")
	})

	t.Run("count respects the window boundary", func(t *testing.T) {
		cleanPolicyTables(t)

		require.NoError(t, activity.Touch(ctx, "visitor-1"))
		require.NoError(t, activity.Touch(ctx, "visitor-2"))
		require.NoError(t, activity.Touch(ctx, "visitor-2")) // same user twice = one row

		count, err := activity.CountActiveSince(ctx, windowStart())
		require.NoError(t, err)
		assert.Equal(t, int64(2), count)

		// Push one user outside the window.
		require.NoError(t, testDB.Exec(
			"UPDATE active_users SET last_active_at = now() - interval '40 days' WHERE user_sub = ?",
			"visitor-2").Error)
		count, err = activity.CountActiveSince(ctx, windowStart())
		require.NoError(t, err)
		assert.Equal(t, int64(1), count)
	})

	t.Run("tenant isolation", func(t *testing.T) {
		cleanPolicyTables(t)

		otherCtx := domain.WithTenantID(context.Background(), otherTenantID)
		require.NoError(t, activity.Touch(ctx, "visitor-1"))
		require.NoError(t, activity.Touch(otherCtx, "visitor-1"))
		require.NoError(t, activity.Touch(otherCtx, "visitor-2"))

		count, err := activity.CountActiveSince(ctx, windowStart())
		require.NoError(t, err)
		assert.Equal(t, int64(1), count, "tenant A must count only its own users")

		count, err = activity.CountActiveSince(otherCtx, windowStart())
		require.NoError(t, err)
		assert.Equal(t, int64(2), count)
	})
}
