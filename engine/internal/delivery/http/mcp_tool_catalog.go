package http

import (
	"context"

	"github.com/cloudwego/eino/components/tool"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools"
)

// mcpToolStore is the minimal view of the builtin tool store the MCP server
// endpoint needs. Consumer-side interface: only the raw factory lookup is
// required, so the endpoint can never reach the agent-resolution path that
// wraps confirm_before tools with the SSE-blocking ConfirmationWrapper.
type mcpToolStore interface {
	Get(name string) (tools.BuiltinToolFactory, bool)
}

// mcpToolCatalog is the fixed allowlist + per-tool required-scope table for the
// MCP server endpoint. It exposes ONLY the admin_* tools plus the two
// provisioning tools — runtime tools (show_structured_output, memory_*, spawn_*,
// knowledge_search, etc.) are never reachable through this endpoint.
type mcpToolCatalog struct {
	store mcpToolStore
	// scopes maps an allowlisted tool name to the bitmask required to call it.
	// Non-delete tools require the provision write bits; destructive delete
	// tools require ScopeManage. ScopeAdmin is a superscope checked separately.
	scopes map[string]int
}

// newMCPToolCatalog builds the catalog from the builtin store. The allowlist is
// hard-coded here (not derived from the store) so a newly registered runtime
// tool can never silently leak onto the MCP surface.
func newMCPToolCatalog(store mcpToolStore) *mcpToolCatalog {
	return &mcpToolCatalog{store: store, scopes: mcpToolScopeTable()}
}

// mcpToolScopeTable returns the per-tool required-scope mapping. Read + create
// + update tools require ScopeProvisionMask; delete tools require ScopeManage.
// The provisioning helpers require the provision mask.
func mcpToolScopeTable() map[string]int {
	provision := ScopeProvisionMask
	del := ScopeManage
	return map[string]int{
		// Agents
		"admin_list_agents":  provision,
		"admin_get_agent":    provision,
		"admin_create_agent": provision,
		"admin_update_agent": provision,
		"admin_delete_agent": del,

		// Schemas
		"admin_list_schemas":  provision,
		"admin_get_schema":    provision,
		"admin_create_schema": provision,
		"admin_update_schema": provision,
		"admin_delete_schema": del,

		// Agent relations
		"admin_list_agent_relations":  provision,
		"admin_create_agent_relation": provision,
		"admin_delete_agent_relation": del,

		// MCP servers
		"admin_list_mcp_servers":       provision,
		"admin_create_mcp_server":      provision,
		"admin_update_mcp_server":      provision,
		"admin_delete_mcp_server":      del,
		"admin_set_mcp_server_enabled": provision,

		// Agent attachment helpers (update-style, non-destructive)
		"admin_attach_mcp_server_to_agent":     provision,
		"admin_detach_mcp_server_from_agent":   provision,
		"admin_add_builtin_tool_to_agent":      provision,
		"admin_remove_builtin_tool_from_agent": provision,

		// Models
		"admin_list_models":       provision,
		"admin_create_model":      provision,
		"admin_update_model":      provision,
		"admin_delete_model":      del,
		"admin_set_default_model": provision,

		// Capabilities
		"admin_add_capability":    provision,
		"admin_remove_capability": del,
		"admin_update_capability": provision,

		// Inspect (read-only)
		"admin_list_sessions": provision,
		"admin_get_session":   provision,

		// Provisioning helpers
		"provision_agent":   provision,
		"get_embed_snippet": provision,

		// Knowledge base (create / add / link / list are provision-scoped;
		// delete_document is symmetric with add — same KB lifecycle, low blast
		// radius — so it is provision-scoped too, NOT the broad manage scope).
		"admin_create_knowledge_base": provision,
		"admin_add_document":          provision,
		"admin_delete_document":       provision,
		"admin_link_knowledge_base":   provision,
		"admin_list_documents":        provision,
	}
}

// names returns the allowlisted tool names in stable (map-iteration-independent)
// order is NOT guaranteed here; callers that need determinism sort the result.
func (c *mcpToolCatalog) names() []string {
	out := make([]string, 0, len(c.scopes))
	for name := range c.scopes {
		out = append(out, name)
	}
	return out
}

// allows reports whether the tool is on the allowlist.
func (c *mcpToolCatalog) allows(name string) bool {
	_, ok := c.scopes[name]
	return ok
}

// requiredScope returns the bitmask required to call the tool. The bool is
// false when the tool is not allowlisted.
func (c *mcpToolCatalog) requiredScope(name string) (int, bool) {
	s, ok := c.scopes[name]
	return s, ok
}

// resolve returns a raw (unwrapped) tool instance for an allowlisted name.
// It builds the tool from the raw store factory with zero-value dependencies —
// no ConfirmRequester — so external calls never trigger the SSE confirmation
// round-trip. Returns nil when the name is not allowlisted or not registered.
func (c *mcpToolCatalog) resolve(_ context.Context, name string) tool.InvokableTool {
	if !c.allows(name) {
		return nil
	}
	factory, ok := c.store.Get(name)
	if !ok {
		return nil
	}
	// Zero-value deps: admin/provisioning tools ignore per-session deps, and a
	// nil ConfirmRequester guarantees no confirmation wrapping.
	return factory(tools.ToolDependencies{})
}
