package domain

import "testing"

func TestIsManagementTool(t *testing.T) {
	tests := []struct {
		name string
		tool string
		want bool
	}{
		{"admin delete agent", "admin_delete_agent", true},
		{"admin delete model", "admin_delete_model", true},
		{"admin create agent", "admin_create_agent", true},
		{"admin list agents", "admin_list_agents", true},
		{"admin prefix only", "admin_", true},
		{"provision agent", "provision_agent", true},
		{"get embed snippet", "get_embed_snippet", true},
		{"runtime tool", "read_file", false},
		{"knowledge search", "knowledge_search", false},
		{"structured output", "show_structured_output", false},
		{"empty", "", false},
		{"admin substring not prefix", "my_admin_tool", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsManagementTool(tt.tool); got != tt.want {
				t.Fatalf("IsManagementTool(%q) = %v, want %v", tt.tool, got, tt.want)
			}
		})
	}
}
