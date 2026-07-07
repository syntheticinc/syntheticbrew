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
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// cleanUsageTables removes usage rows so each subtest starts clean. These
// tables are not in the shared truncate list, so the test owns its own reset.
func cleanUsageTables(t *testing.T) {
	t.Helper()
	require.NoError(t, testDB.Exec("DELETE FROM usage_counters").Error)
	require.NoError(t, testDB.Exec("DELETE FROM usage_limit_configs").Error)
}

// TestUsageLimit_Repositories exercises the real GORM CounterStore/ConfigStore
// against the migrated database (migration 015 applied by the suite).
func TestUsageLimit_Repositories(t *testing.T) {
	requireSuite(t)

	ctx := domain.WithTenantID(context.Background(), ceTenantID)
	counters := configrepo.NewGORMUsageCounterRepository(testDB)

	t.Run("atomic increment inserts then increments both counts", func(t *testing.T) {
		cleanUsageTables(t)

		// First increment inserts (1 turn / 3 steps).
		require.NoError(t, counters.Increment(ctx, "", 3600, 1, 3))
		row, err := counters.Read(ctx, "")
		require.NoError(t, err)
		require.NotNil(t, row)
		assert.Equal(t, int64(1), row.TurnsCount)
		assert.Equal(t, int64(3), row.StepsCount)

		// Second increment bumps both (2 turns / 8 steps).
		require.NoError(t, counters.Increment(ctx, "", 3600, 1, 5))
		row, err = counters.Read(ctx, "")
		require.NoError(t, err)
		require.NotNil(t, row)
		assert.Equal(t, int64(2), row.TurnsCount)
		assert.Equal(t, int64(8), row.StepsCount)
	})

	t.Run("rolling reset on elapsed interval", func(t *testing.T) {
		cleanUsageTables(t)

		require.NoError(t, counters.Increment(ctx, "", 3600, 1, 10))
		require.NoError(t, counters.Increment(ctx, "", 3600, 1, 10))

		// Backdate period_start so the next increment is past the interval.
		require.NoError(t, testDB.WithContext(ctx).
			Model(&models.UsageCounterModel{}).
			Where("tenant_id = ? AND user_sub = ? AND window_kind = ?", ceTenantID, "", domain.WindowRolling).
			Update("period_start", time.Now().Add(-2*time.Hour)).Error)

		// interval 3600s, elapsed ~7200s → reset to (1 turn / 4 steps).
		require.NoError(t, counters.Increment(ctx, "", 3600, 1, 4))
		row, err := counters.Read(ctx, "")
		require.NoError(t, err)
		require.NotNil(t, row)
		assert.Equal(t, int64(1), row.TurnsCount, "elapsed window must reset turns to 1")
		assert.Equal(t, int64(4), row.StepsCount, "elapsed window must reset steps to the new turn's steps")
	})

	t.Run("unique constraint keeps one row per (tenant,user,window)", func(t *testing.T) {
		cleanUsageTables(t)

		for i := 0; i < 5; i++ {
			require.NoError(t, counters.Increment(ctx, "user-x", 3600, 1, 1))
		}

		var count int64
		require.NoError(t, testDB.WithContext(ctx).
			Model(&models.UsageCounterModel{}).
			Where("tenant_id = ? AND user_sub = ? AND window_kind = ?", ceTenantID, "user-x", domain.WindowRolling).
			Count(&count).Error)
		assert.Equal(t, int64(1), count, "repeated increments must upsert one row, not many")

		row, err := counters.Read(ctx, "user-x")
		require.NoError(t, err)
		require.NotNil(t, row)
		assert.Equal(t, int64(5), row.TurnsCount)
	})

	t.Run("tenant isolation", func(t *testing.T) {
		cleanUsageTables(t)

		otherTenant := "00000000-0000-0000-0000-000000000002"
		ctxA := domain.WithTenantID(context.Background(), ceTenantID)
		ctxB := domain.WithTenantID(context.Background(), otherTenant)

		require.NoError(t, counters.Increment(ctxA, "", 3600, 1, 1))
		require.NoError(t, counters.Increment(ctxA, "", 3600, 1, 1))
		require.NoError(t, counters.Increment(ctxB, "", 3600, 1, 1))

		rowA, err := counters.Read(ctxA, "")
		require.NoError(t, err)
		require.NotNil(t, rowA)
		assert.Equal(t, int64(2), rowA.TurnsCount, "tenant A counts only its own turns")

		rowB, err := counters.Read(ctxB, "")
		require.NoError(t, err)
		require.NotNil(t, rowB)
		assert.Equal(t, int64(1), rowB.TurnsCount, "tenant B is independent of tenant A")

		// Cleanup the extra tenant row we created outside the sentinel tenant.
		require.NoError(t, testDB.Exec("DELETE FROM usage_counters WHERE tenant_id = ?", otherTenant).Error)
	})
}

// TestUsageLimit_ConfigStore exercises the real GORM ConfigStore upsert and
// tenant-scoped reads against the migrated database.
func TestUsageLimit_ConfigStore(t *testing.T) {
	requireSuite(t)

	ctx := domain.WithTenantID(context.Background(), ceTenantID)
	configs := configrepo.NewGORMUsageLimitRepository(testDB)

	t.Run("set upserts one row per scope and get/list read it back", func(t *testing.T) {
		cleanUsageTables(t)

		require.NoError(t, configs.Set(ctx, domain.UsageLimit{
			Scope: domain.ScopeTenant, Unit: domain.UnitTurns, LimitValue: 10, IntervalSeconds: 3600, Enabled: true,
		}))
		// Re-set the same scope with new values → upsert, not a second row.
		require.NoError(t, configs.Set(ctx, domain.UsageLimit{
			Scope: domain.ScopeTenant, Unit: domain.UnitSteps, LimitValue: 500, IntervalSeconds: 60, Enabled: false,
		}))

		got, err := configs.Get(ctx, domain.ScopeTenant)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, domain.UnitSteps, got.Unit)
		assert.Equal(t, int64(500), got.LimitValue)
		assert.Equal(t, int64(60), got.IntervalSeconds)
		assert.False(t, got.Enabled)

		list, err := configs.List(ctx)
		require.NoError(t, err)
		assert.Len(t, list, 1, "re-setting a scope must upsert, not duplicate")
	})

	t.Run("first insert of enabled=false persists false", func(t *testing.T) {
		cleanUsageTables(t)

		// A brand-new row with Enabled false must not fall back to the column
		// default (true).
		require.NoError(t, configs.Set(ctx, domain.UsageLimit{
			Scope: domain.ScopeUser, Unit: domain.UnitTurns, LimitValue: 3, IntervalSeconds: 60, Enabled: false,
		}))
		got, err := configs.Get(ctx, domain.ScopeUser)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.False(t, got.Enabled, "operator-declared enabled=false must persist on first insert")
	})

	t.Run("get absent returns nil and delete is idempotent", func(t *testing.T) {
		cleanUsageTables(t)

		got, err := configs.Get(ctx, domain.ScopeUser)
		require.NoError(t, err)
		assert.Nil(t, got)

		// Delete of an absent row must not error.
		require.NoError(t, configs.Delete(ctx, domain.ScopeUser))
	})
}
