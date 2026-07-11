package authprim

import (
	"errors"
	"fmt"
	"strings"
)

// Scope bitmask constants matching ERD api_tokens.scopes_mask.
//
// Canonical home: this leaf package, so both the delivery layer (route
// guards, token handler) and the JWT verifier in internal/infrastructure/auth
// can share one scope-name registry without a delivery→infrastructure import
// cycle. internal/delivery/http re-exports these under the same names.
// pkg/plugin mirrors a subset for external verifier implementations.
const (
	ScopeChat          = 1
	ScopeTasks         = 2
	ScopeAgentsRead    = 4
	ScopeConfig        = 8
	ScopeAdmin         = 16
	ScopeAgentsWrite   = 32
	ScopeModelsRead    = 64
	ScopeModelsWrite   = 128
	ScopeMCPRead       = 256
	ScopeMCPWrite      = 512
	ScopeTriggersRead  = 1024
	ScopeTriggersWrite = 2048
	ScopeSchemasRead   = 4096
	ScopeSchemasWrite  = 8192

	// Granular scopes added in 1.1.4 to retire legacy RequireAdminSession
	// gates on /sessions, /audit, /settings, /tools/metadata, /resilience.
	// ScopeAdmin (=16) still acts as a superscope and bypasses any specific
	// scope check via RequireScope — admin tooling stays unchanged.
	ScopeSessionsRead    = 16384
	ScopeSessionsWrite   = 32768
	ScopeSettingsRead    = 65536
	ScopeSettingsWrite   = 131072
	ScopeAuditRead       = 262144
	ScopeResilienceRead  = 524288
	ScopeResilienceWrite = 1048576
	ScopeToolsRead       = 2097152

	// ScopeManage is a dedicated bit carrying destructive (delete) authority
	// over provisioned resources through the MCP server endpoint. The existing
	// Scope*Write bits conflate update and delete, so this new bit is the only
	// way to split "may create/update" (provision) from "may also delete"
	// (manage) without repurposing the write bits. It survives the token
	// name→mask conversion, so the MCP per-tool scope table can require it for
	// delete tools while a provision-only token is rejected. ScopeAdmin still
	// implies it (superscope in RequireScope and the MCP scope check).
	ScopeManage = 4194304
)

// ScopeAPIMask is the virtual catch-all integration scope. It is NOT a
// separate bit — it expands into the union of every non-admin operation
// permitted to an integration: chat, tasks, sessions, and read-only access to
// agents, schemas, models, and MCP servers. Admin-only surfaces (agent CRUD,
// schema CRUD, model CRUD, MCP CRUD, config, token management) are
// deliberately excluded so an "api" token cannot reconfigure the tenant it
// runs under.
const ScopeAPIMask = ScopeChat | ScopeTasks | ScopeAgentsRead | ScopeModelsRead | ScopeMCPRead | ScopeTriggersRead | ScopeSchemasRead | ScopeSessionsRead | ScopeSettingsRead | ScopeAuditRead | ScopeResilienceRead | ScopeToolsRead

// ScopeProvisionMask is the composite mask for the "provision" scope granted
// to MCP-server integrations that build agents/schemas/models/MCP servers.
// It is the union of every relevant read bit plus the create/update write
// bits. It deliberately excludes ScopeManage: a provision token may create and
// update, but the MCP per-tool scope table rejects the destructive delete
// tools because they require ScopeManage. The existing Scope*Write bits
// conflate update and delete at the REST layer, so the delete split is
// enforced only at the MCP layer via the scope table + this dedicated bit.
const ScopeProvisionMask = ScopeAgentsRead | ScopeAgentsWrite |
	ScopeSchemasRead | ScopeSchemasWrite |
	ScopeModelsRead | ScopeModelsWrite |
	ScopeMCPRead | ScopeMCPWrite |
	ScopeSessionsRead

// ScopeManageMask is the composite mask for the "manage" scope: everything
// provision grants plus the dedicated ScopeManage bit that unlocks the
// destructive delete tools at the MCP per-tool scope table.
const ScopeManageMask = ScopeProvisionMask | ScopeManage

// ScopeNameToMask maps canonical scope name tokens accepted by
// POST /auth/tokens `scopes: [...]` (and by the JWT `scope` claim) to their
// underlying bitmask.
//
// Granular names ("chat", "tasks", "agents:read", ...) map to a single bit.
// Composite names ("api", "admin") expand into a union.
var ScopeNameToMask = map[string]int{
	"chat":             ScopeChat,
	"tasks":            ScopeTasks,
	"agents:read":      ScopeAgentsRead,
	"agents":           ScopeAgentsRead, // alias: "agents" => read-only
	"agents:write":     ScopeAgentsWrite,
	"config":           ScopeConfig,
	"admin":            ScopeAdmin,
	"models:read":      ScopeModelsRead,
	"models":           ScopeModelsRead,
	"models:write":     ScopeModelsWrite,
	"mcp:read":         ScopeMCPRead,
	"mcp":              ScopeMCPRead,
	"mcp:write":        ScopeMCPWrite,
	"schemas:read":     ScopeSchemasRead,
	"schemas":          ScopeSchemasRead,
	"schemas:write":    ScopeSchemasWrite,
	"sessions:read":    ScopeSessionsRead,
	"sessions":         ScopeSessionsRead,
	"sessions:write":   ScopeSessionsWrite,
	"settings:read":    ScopeSettingsRead,
	"settings":         ScopeSettingsRead,
	"settings:write":   ScopeSettingsWrite,
	"audit:read":       ScopeAuditRead,
	"audit":            ScopeAuditRead,
	"resilience:read":  ScopeResilienceRead,
	"resilience":       ScopeResilienceRead,
	"resilience:write": ScopeResilienceWrite,
	"tools:read":       ScopeToolsRead,
	"tools":            ScopeToolsRead,
	"api":              ScopeAPIMask,
	"provision":        ScopeProvisionMask,
	"manage":           ScopeManageMask,
}

// ScopesToMask converts a list of scope names into a bitmask. Unknown
// names are dropped (no error) — defensive against front-end typos that
// might otherwise privilege-escalate. An empty list returns 0, which is
// still a hard reject at RequireScope time — never a silent escalation.
func ScopesToMask(scopes []string) int {
	mask := 0
	for _, s := range scopes {
		if bit, ok := ScopeNameToMask[s]; ok {
			mask |= bit
		}
	}
	return mask
}

// ParseScopeClaim converts a space-delimited OAuth-style scope string (the
// JWT `scope` claim) into a bitmask. Unlike ScopesToMask it is strict: an
// empty string or any name missing from ScopeNameToMask is an error. JWT
// verification must fail closed — silently dropping an unrecognized scope
// would accept a token with authority other than what its issuer granted.
func ParseScopeClaim(scope string) (int, error) {
	names := strings.Fields(scope)
	if len(names) == 0 {
		return 0, errors.New("empty scope")
	}
	mask := 0
	for _, name := range names {
		bit, ok := ScopeNameToMask[name]
		if !ok {
			return 0, fmt.Errorf("unknown scope %q", name)
		}
		mask |= bit
	}
	return mask, nil
}
