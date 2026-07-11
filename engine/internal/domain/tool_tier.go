package domain

import "strings"

// ToolTier represents the tier classification of a tool.
type ToolTier int

const (
	// ToolTierCore (Tier 1) — always available: manage_tasks, show_structured_output, spawn_agent.
	// Note: subtasks are unified into manage_tasks via parent_task_id (no separate
	// manage_subtasks tool). The wait flow is exposed as spawn_agent.action="wait"
	// (no separate wait tool). See `infrastructure/tools/builtin_factories.go` for
	// the actual registry — CoreToolNames() must mirror it.
	ToolTierCore ToolTier = 1

	// ToolTierCapability (Tier 2) — auto-injected by capabilities: memory_recall, memory_store, knowledge_search
	ToolTierCapability ToolTier = 2

	// ToolTierSelfHosted (Tier 3) — CE only, blocked in multi-tenant mode: read_file, write_file, execute_command, etc.
	ToolTierSelfHosted ToolTier = 3

	// ToolTierMCP (Tier 4) — from connected MCP servers: web_search, external APIs, etc.
	ToolTierMCP ToolTier = 4
)

// CoreToolNames returns the Tier 1 tool names that are always available.
//
// MUST mirror what `infrastructure/tools/builtin_factories.RegisterAllBuiltins`
// registers plus the runtime-registered `spawn_agent`. Drift between this list
// and the actual registry surfaces to operators as `unknown builtin tool` at
// agent runtime (lying CoreToolNames was the chirp 1.1.2 bug #2).
//
// `manage_subtasks` was unified into `manage_tasks` via parent_task_id — use
// `manage_tasks(action=create_subtask|list_subtasks|get_ready, parent_task_id=…)`.
// `wait` was folded into `spawn_agent` action="wait".
func CoreToolNames() []string {
	return []string{
		"manage_tasks",
		"show_structured_output",
		"spawn_agent",
	}
}

// CapabilityToolNames returns the Tier 2 tool names injected by capabilities.
func CapabilityToolNames() []string {
	return []string{
		"memory_recall",
		"memory_store",
		"knowledge_search",
	}
}

// SelfHostedToolNames returns the Tier 3 tool names blocked in multi-tenant mode.
func SelfHostedToolNames() []string {
	return []string{
		"read_file",
		"write_file",
		"edit_file",
		"glob",
		"grep_search",
		"search_code",
		"smart_search",
		"get_project_tree",
		"get_function",
		"get_class",
		"get_file_structure",
		"lsp",
		"execute_command",
	}
}

// ClassifyToolTier returns the tier for a given tool name.
func ClassifyToolTier(toolName string) ToolTier {
	for _, name := range CoreToolNames() {
		if name == toolName {
			return ToolTierCore
		}
	}
	for _, name := range CapabilityToolNames() {
		if name == toolName {
			return ToolTierCapability
		}
	}
	for _, name := range SelfHostedToolNames() {
		if name == toolName {
			return ToolTierSelfHosted
		}
	}
	// spawn_* tools are also Tier 1
	if strings.HasPrefix(toolName, "spawn_") {
		return ToolTierCore
	}
	// admin_* tools — orchestration over other platform objects. Treated as
	// self-hosted so that tenants in multi-tenant deployments don't grant arbitrary admin tool use
	// to agents through the default MCP fallthrough. Admin HTTP layer still
	// rejects these names at agent create/update time; this extra guard keeps
	// seed agents / runtime-built tool lists honest.
	if strings.HasPrefix(toolName, "admin_") {
		return ToolTierSelfHosted
	}
	return ToolTierMCP
}
