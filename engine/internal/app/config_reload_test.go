package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// TestConfigReloader_MultiTenant_NilRegistry_NoPanic is the F4 regression guard.
//
// In multi-tenant mode the eager AgentRegistry singleton is nil (it is assigned
// only when !RequireTenant). The old Reload dereferenced a.registry.Reload(ctx)
// unconditionally → nil-pointer panic → the Recoverer turned POST /config/reload
// into a 500. The fix nil-guards registry and routes multi-tenant reloads through
// the tenant-aware registry manager, keeping the CE 500-on-DB-error path intact.
//
// RED (pre-fix): Reload panics on the nil registry.
// GREEN (post-fix): Reload returns nil without panicking.
func TestConfigReloader_MultiTenant_NilRegistry_NoPanic(t *testing.T) {
	adapter := &configReloaderHTTPAdapter{
		registry:    nil, // multi-tenant: no eager singleton
		registryMgr: nil, // no manager wired in this minimal case → invalidate is skipped
		mcpManager:  nil, // reconnectMCPServers returns early
		db:          nil,
	}
	ctx := domain.WithTenantID(context.Background(), "tenant-abc")

	require.NotPanics(t, func() {
		require.NoError(t, adapter.Reload(ctx),
			"multi-tenant /config/reload must not error on a nil eager registry")
	}, "multi-tenant /config/reload must not panic on a nil eager registry")
}
