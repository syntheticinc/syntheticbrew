package domain

import (
	"testing"
)

func TestClassifyToolTier(t *testing.T) {
	tests := []struct {
		toolName string
		expected ToolTier
	}{
		// Tier 1 — Core (must mirror builtin_factories.RegisterAllBuiltins +
		// runtime-registered spawn_agent).
		{"show_structured_output", ToolTierCore},
		{"manage_tasks", ToolTierCore},
		{"spawn_agent", ToolTierCore},
		{"spawn_researcher", ToolTierCore}, // spawn_* prefix → Core via fallthrough

		// Regression guard: manage_subtasks was unified into manage_tasks
		// (parent_task_id) and wait became spawn_agent.action="wait". Neither
		// is registered as a builtin; they must NOT classify as Core.
		// See chirp 1.1.2 bug #2.
		{"manage_subtasks", ToolTierMCP},
		{"wait", ToolTierMCP},

		// Tier 2 — Capability
		{"memory_recall", ToolTierCapability},
		{"memory_store", ToolTierCapability},
		{"knowledge_search", ToolTierCapability},

		// Tier 3 — Self-hosted
		{"read_file", ToolTierSelfHosted},
		{"write_file", ToolTierSelfHosted},
		{"edit_file", ToolTierSelfHosted},
		{"execute_command", ToolTierSelfHosted},
		{"glob", ToolTierSelfHosted},
		{"grep_search", ToolTierSelfHosted},
		{"search_code", ToolTierSelfHosted},
		{"get_project_tree", ToolTierSelfHosted},
		{"lsp", ToolTierSelfHosted},
		// admin_* orchestration tools — also self-hosted so the multi-tenant sandbox
		// blocks them by default. Agents must not receive admin privileges
		// through the MCP fallthrough.
		{"admin_create_mcp_server", ToolTierSelfHosted},
		{"admin_list_agents", ToolTierSelfHosted},
		{"admin_reset_system_schema", ToolTierSelfHosted},

		// Tier 4 — MCP (everything else)
		{"web_search", ToolTierMCP},
		{"custom_tool", ToolTierMCP},
		{"google_sheets_read", ToolTierMCP},
	}
	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			got := ClassifyToolTier(tt.toolName)
			if got != tt.expected {
				t.Errorf("ClassifyToolTier(%q) = %d, want %d", tt.toolName, got, tt.expected)
			}
		})
	}
}

func TestCoreToolNames(t *testing.T) {
	names := CoreToolNames()
	if len(names) == 0 {
		t.Fatal("CoreToolNames() returned empty")
	}
	for _, n := range names {
		if ClassifyToolTier(n) != ToolTierCore {
			t.Errorf("CoreToolNames() includes %q which classifies as tier %d", n, ClassifyToolTier(n))
		}
	}
}

func TestCapabilityToolNames(t *testing.T) {
	names := CapabilityToolNames()
	if len(names) == 0 {
		t.Fatal("CapabilityToolNames() returned empty")
	}
	for _, n := range names {
		if ClassifyToolTier(n) != ToolTierCapability {
			t.Errorf("CapabilityToolNames() includes %q which classifies as tier %d", n, ClassifyToolTier(n))
		}
	}
}

func TestSelfHostedToolNames(t *testing.T) {
	names := SelfHostedToolNames()
	if len(names) == 0 {
		t.Fatal("SelfHostedToolNames() returned empty")
	}
	for _, n := range names {
		if ClassifyToolTier(n) != ToolTierSelfHosted {
			t.Errorf("SelfHostedToolNames() includes %q which classifies as tier %d", n, ClassifyToolTier(n))
		}
	}
}
