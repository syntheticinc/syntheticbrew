package capability

import (
	"testing"
)

func TestTierEnforcer_CE_AllowsAll(t *testing.T) {
	enforcer := NewTierEnforcer(DeploymentModeCE)

	tools := []string{"show_structured_output", "memory_recall", "read_file", "execute_command", "web_search"}
	for _, tool := range tools {
		if err := enforcer.IsAllowed(tool); err != nil {
			t.Errorf("CE mode should allow %q, got: %v", tool, err)
		}
	}
}

func TestTierEnforcer_Cloud_BlocksTier3(t *testing.T) {
	enforcer := NewTierEnforcer(DeploymentModeCloud)

	blocked := []string{"read_file", "write_file", "edit_file", "execute_command", "glob", "grep_search", "lsp"}
	for _, tool := range blocked {
		if err := enforcer.IsAllowed(tool); err == nil {
			t.Errorf("Cloud mode should block %q", tool)
		}
	}
}

func TestTierEnforcer_Cloud_AllowsTier1(t *testing.T) {
	enforcer := NewTierEnforcer(DeploymentModeCloud)

	allowed := []string{"show_structured_output", "manage_tasks", "spawn_agent"}
	for _, tool := range allowed {
		if err := enforcer.IsAllowed(tool); err != nil {
			t.Errorf("Cloud mode should allow Tier 1 %q, got: %v", tool, err)
		}
	}
}

func TestTierEnforcer_Cloud_AllowsTier2(t *testing.T) {
	enforcer := NewTierEnforcer(DeploymentModeCloud)

	allowed := []string{"memory_recall", "memory_store", "knowledge_search"}
	for _, tool := range allowed {
		if err := enforcer.IsAllowed(tool); err != nil {
			t.Errorf("Cloud mode should allow Tier 2 %q, got: %v", tool, err)
		}
	}
}

func TestTierEnforcer_Cloud_AllowsTier4(t *testing.T) {
	enforcer := NewTierEnforcer(DeploymentModeCloud)

	allowed := []string{"web_search", "custom_mcp_tool", "google_sheets_read"}
	for _, tool := range allowed {
		if err := enforcer.IsAllowed(tool); err != nil {
			t.Errorf("Cloud mode should allow Tier 4 %q, got: %v", tool, err)
		}
	}
}

func TestTierEnforcer_FilterAllowed(t *testing.T) {
	enforcer := NewTierEnforcer(DeploymentModeCloud)

	tools := []string{"show_structured_output", "read_file", "memory_recall", "execute_command", "web_search"}
	allowed, blocked := enforcer.FilterAllowed(tools)

	if len(allowed) != 3 {
		t.Errorf("expected 3 allowed, got %d: %v", len(allowed), allowed)
	}
	if len(blocked) != 2 {
		t.Errorf("expected 2 blocked, got %d: %v", len(blocked), blocked)
	}
}

func TestClassifyTool(t *testing.T) {
	// Just verify the convenience wrapper works
	tier := ClassifyTool("show_structured_output")
	if tier != 1 {
		t.Errorf("expected tier 1, got %d", tier)
	}
}
