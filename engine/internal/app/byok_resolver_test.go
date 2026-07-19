package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
)

const (
	tenantA = "11111111-1111-1111-1111-111111111111"
	tenantB = "22222222-2222-2222-2222-222222222222"
)

// enableBYOKFor writes byok.enabled=true for the given tenant.
func enableBYOKFor(t *testing.T, ctx context.Context, db *configrepo.GORMSettingRepository, tenantID string) {
	t.Helper()
	require.NoError(t, db.SetJSON(domain.WithTenantID(ctx, tenantID), settingKeyBYOKEnabled, []byte("true")))
}

// TestBYOKResolver_CrossTenantIsolation is the BUG-08 regression: tenant A
// enabling BYOK must NOT enable it for tenant B (the old process-global atomic
// leaked one tenant's toggle to all). SCC-02.
func TestBYOKResolver_CrossTenantIsolation(t *testing.T) {
	db := setupSettingsTestDB(t)
	repo := configrepo.NewGORMSettingRepository(db)
	ctx := context.Background()
	enableBYOKFor(t, ctx, repo, tenantA)

	r := newBYOKTenantResolver(db, config.BYOKConfig{}, false)

	assert.True(t, r.Resolve(domain.WithTenantID(ctx, tenantA)).Enabled, "tenant A enabled its own BYOK")
	assert.False(t, r.Resolve(domain.WithTenantID(ctx, tenantB)).Enabled, "tenant B must not inherit A's toggle")
}

// TestBYOKResolver_FailsClosedOnEmptyTenantExternal is F7: in external
// (multi-tenant) auth-mode an unattributed request (empty tenant_id) must fail
// closed — never fall through to the sentinel tenant's row, even if the
// sentinel has BYOK enabled.
func TestBYOKResolver_FailsClosedOnEmptyTenantExternal(t *testing.T) {
	db := setupSettingsTestDB(t)
	repo := configrepo.NewGORMSettingRepository(db)
	ctx := context.Background()
	// Sentinel row is enabled — the resolver must still refuse an empty tenant.
	enableBYOKFor(t, ctx, repo, domain.CETenantID)

	r := newBYOKTenantResolver(db, config.BYOKConfig{}, false) // external mode

	assert.False(t, r.Resolve(ctx).Enabled, "empty tenant in external mode must fail closed")
}

// TestBYOKResolver_LocalModeEmptyTenantUsesSentinel is the CE side of B1: in
// local auth-mode the CE token carries no tenant, so an empty tenant maps to
// the sentinel row (the whole engine reads under the sentinel in CE).
func TestBYOKResolver_LocalModeEmptyTenantUsesSentinel(t *testing.T) {
	db := setupSettingsTestDB(t)
	repo := configrepo.NewGORMSettingRepository(db)
	ctx := context.Background()
	enableBYOKFor(t, ctx, repo, domain.CETenantID)

	r := newBYOKTenantResolver(db, config.BYOKConfig{}, true) // local mode

	assert.True(t, r.Resolve(ctx).Enabled, "empty tenant in local mode reads the sentinel row")
}

// TestBYOKResolver_InvalidateKeyMatchesCacheKey is the B1 key-normalisation
// guard: in local mode a CE write carries an empty tenant, but the cache is
// keyed by the sentinel. InvalidateTenant must use the SAME normalisation so
// the entry is actually dropped — otherwise a CE admin's disable would not take
// effect until restart.
func TestBYOKResolver_InvalidateKeyMatchesCacheKey(t *testing.T) {
	db := setupSettingsTestDB(t)
	repo := configrepo.NewGORMSettingRepository(db)
	ctx := context.Background()
	enableBYOKFor(t, ctx, repo, domain.CETenantID)

	r := newBYOKTenantResolver(db, config.BYOKConfig{}, true) // local mode

	// Prime the cache (empty tenant → sentinel key).
	require.True(t, r.Resolve(ctx).Enabled)

	// Admin disables BYOK, then the settings write invalidates with an empty
	// tenant ctx — the same shape a CE PUT carries.
	require.NoError(t, repo.SetJSON(domain.WithTenantID(ctx, domain.CETenantID), settingKeyBYOKEnabled, []byte("false")))
	r.InvalidateTenant(ctx)

	assert.False(t, r.Resolve(ctx).Enabled, "disable must take effect (invalidation key must match cache key)")
}

// TestBYOKResolver_InvalidateTenantIsolation verifies InvalidateTenant drops
// only the writer's tenant, mirroring agentregistry.Manager — invalidating B
// must not evict A's cached entry.
func TestBYOKResolver_InvalidateTenantIsolation(t *testing.T) {
	db := setupSettingsTestDB(t)
	repo := configrepo.NewGORMSettingRepository(db)
	ctx := context.Background()
	enableBYOKFor(t, ctx, repo, tenantA)
	enableBYOKFor(t, ctx, repo, tenantB)

	r := newBYOKTenantResolver(db, config.BYOKConfig{}, false)
	ctxA := domain.WithTenantID(ctx, tenantA)
	ctxB := domain.WithTenantID(ctx, tenantB)

	// Prime both caches.
	require.True(t, r.Resolve(ctxA).Enabled)
	require.True(t, r.Resolve(ctxB).Enabled)

	// Disable A's row directly, then invalidate ONLY B. A stays cached (enabled),
	// B is unaffected by A's row change.
	require.NoError(t, repo.SetJSON(ctxA, settingKeyBYOKEnabled, []byte("false")))
	r.InvalidateTenant(ctxB)

	assert.True(t, r.Resolve(ctxA).Enabled, "A must remain cached — only B was invalidated")

	// Now invalidate A — it re-reads the disabled row.
	r.InvalidateTenant(ctxA)
	assert.False(t, r.Resolve(ctxA).Enabled, "A re-reads its now-disabled row after its own invalidation")
}
