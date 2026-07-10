package mcp

import (
	"context"

	infrastructure_mcp "github.com/syntheticinc/syntheticbrew/internal/infrastructure/mcp"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// ConnectAll iterates the persisted MCP server records and dials each one,
// registering successful clients with the supplied registry. Failures are
// logged and skipped so a single broken endpoint does not abort the boot
// sequence. policy is consulted before opening stdio transports; multi-tenant
// deployments pass a RestrictedTransportPolicy that blocks stdio (host code
// execution is forbidden in multi-tenant builds).
//
// Kept as a public entry point for legacy callers (tests, seeds). Internally
// delegates to (*ClientRegistry).Load — single source of truth for dial logic.
func ConnectAll(
	ctx context.Context,
	servers []models.MCPServerModel,
	registry *infrastructure_mcp.ClientRegistry,
	policy TransportPolicy,
) {
	_ = registry.Load(ctx, servers, policy)
}
