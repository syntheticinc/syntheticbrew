package admin

import (
	"github.com/cloudwego/eino/components/tool"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools"
)

// RegisterAdminTools registers all admin tools into the builtin store using closure capture.
// Admin deps (repos, reloader) are captured at registration time.
// The ToolDependencies arg in each factory is intentionally ignored -- admin tools
// do not need per-session deps.
func RegisterAdminTools(store *tools.BuiltinToolStore, deps AdminToolDependencies) {
	reloader := deps.Reloader

	// Agent tools
	store.Register("admin_list_agents", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminListAgentsTool(deps.AgentRepo)
	})
	store.Register("admin_get_agent", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminGetAgentTool(deps.AgentRepo)
	})
	store.Register("admin_create_agent", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminCreateAgentTool(deps.AgentRepo, reloader)
	})
	store.Register("admin_update_agent", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminUpdateAgentTool(deps.AgentRepo, reloader)
	})
	store.Register("admin_delete_agent", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminDeleteAgentTool(deps.AgentRepo, reloader)
	})

	// Schema tools
	store.Register("admin_list_schemas", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminListSchemasTool(deps.SchemaRepo)
	})
	store.Register("admin_get_schema", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminGetSchemaTool(deps.SchemaRepo)
	})
	store.Register("admin_create_schema", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminCreateSchemaTool(deps.SchemaRepo, reloader)
	})
	store.Register("admin_update_schema", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminUpdateSchemaTool(deps.SchemaRepo, deps.AgentRepo, reloader)
	})
	store.Register("admin_delete_schema", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminDeleteSchemaTool(deps.SchemaRepo, reloader)
	})

	// V2: schema membership is derived from agent_relations (see
	// docs/architecture/agent-first-runtime.md §2.1) — admin agents add a
	// member to a schema by creating a delegation relation via the
	// agent_relation tools below.

	// AgentRelation tools (V2: edges→agent_relations, single implicit DELEGATION type)
	store.Register("admin_list_agent_relations", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminListAgentRelationsTool(deps.AgentRelationRepo)
	})
	store.Register("admin_create_agent_relation", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminCreateAgentRelationTool(deps.AgentRelationRepo, reloader)
	})
	store.Register("admin_delete_agent_relation", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminDeleteAgentRelationTool(deps.AgentRelationRepo, reloader)
	})

	// MCP server tools
	store.Register("admin_list_mcp_servers", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminListMCPServersTool(deps.MCPServerRepo)
	})
	store.Register("admin_create_mcp_server", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminCreateMCPServerTool(deps.MCPServerRepo, reloader, deps.TransportPolicy)
	})
	store.Register("admin_update_mcp_server", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminUpdateMCPServerTool(deps.MCPServerRepo, reloader, deps.TransportPolicy)
	})
	store.Register("admin_delete_mcp_server", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminDeleteMCPServerTool(deps.MCPServerRepo, reloader)
	})
	store.Register("admin_set_mcp_server_enabled", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminSetMCPServerEnabledTool(deps.MCPServerRepo, reloader)
	})

	// Granular agent attachment tools (append-style wrappers around admin_update_agent)
	store.Register("admin_attach_mcp_server_to_agent", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminAttachMCPServerToAgentTool(deps.AgentRepo, reloader)
	})
	store.Register("admin_detach_mcp_server_from_agent", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminDetachMCPServerFromAgentTool(deps.AgentRepo, reloader)
	})
	store.Register("admin_add_builtin_tool_to_agent", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminAddBuiltinToolToAgentTool(deps.AgentRepo, reloader)
	})
	store.Register("admin_remove_builtin_tool_from_agent", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminRemoveBuiltinToolFromAgentTool(deps.AgentRepo, reloader)
	})

	// Model tools
	store.Register("admin_list_models", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminListModelsTool(deps.ModelRepo)
	})
	store.Register("admin_create_model", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminCreateModelTool(deps.ModelRepo, reloader)
	})
	store.Register("admin_update_model", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminUpdateModelTool(deps.ModelRepo, reloader)
	})
	store.Register("admin_delete_model", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminDeleteModelTool(deps.ModelRepo, reloader)
	})
	store.Register("admin_set_default_model", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminSetDefaultModelTool(deps.ModelRepo, reloader)
	})

	// Capability tools
	store.Register("admin_add_capability", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminAddCapabilityTool(deps.CapabilityRepo, reloader)
	})
	store.Register("admin_remove_capability", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminRemoveCapabilityTool(deps.CapabilityRepo, reloader)
	})
	store.Register("admin_update_capability", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminUpdateCapabilityTool(deps.CapabilityRepo, reloader)
	})

	// Inspect tools
	store.Register("admin_list_sessions", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminListSessionsTool(deps.SessionRepo)
	})
	store.Register("admin_get_session", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewAdminGetSessionTool(deps.SessionRepo)
	})

	// Provisioning tools — high-level one-shot helpers for external MCP clients.
	store.Register("provision_agent", func(_ tools.ToolDependencies) tool.InvokableTool {
		return NewProvisionAgentTool(deps.AgentRepo, deps.SchemaRepo, reloader)
	})
	// get_embed_snippet needs a token minter; skip registration when absent so
	// the tool never surfaces without the ability to mint a key.
	if deps.WidgetTokenMinter != nil {
		store.Register("get_embed_snippet", func(_ tools.ToolDependencies) tool.InvokableTool {
			return NewGetEmbedSnippetTool(deps.SchemaRepo, deps.WidgetTokenMinter)
		})
	}
}
