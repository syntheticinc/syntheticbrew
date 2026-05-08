package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetToolMetadata_KnownTool(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		wantZone SecurityZone
	}{
		{"manage_tasks", "manage_tasks", ZoneSafe},
		{"spawn_agent", "spawn_agent", ZoneSafe},
		{"show_structured_output", "show_structured_output", ZoneSafe},
		{"memory_recall", "memory_recall", ZoneSafe},
		{"memory_store", "memory_store", ZoneSafe},
		{"knowledge_search", "knowledge_search", ZoneSafe},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := GetToolMetadata(tt.toolName)
			assert.Equal(t, tt.toolName, meta.Name)
			assert.Equal(t, tt.wantZone, meta.SecurityZone)
			assert.NotEmpty(t, meta.Description)
		})
	}
}

func TestGetToolMetadata_UnknownTool(t *testing.T) {
	meta := GetToolMetadata("nonexistent_tool")
	assert.Equal(t, "nonexistent_tool", meta.Name)
	// Unknown tools default to caution — MCP tools and custom integrations
	// go through this path.
	assert.Equal(t, ZoneCaution, meta.SecurityZone)
	assert.Equal(t, "Custom tool", meta.Description)
}

func TestGetAllToolMetadata(t *testing.T) {
	all := GetAllToolMetadata()
	require.NotEmpty(t, all)

	zones := map[SecurityZone]int{}
	for _, m := range all {
		zones[m.SecurityZone]++
		assert.NotEmpty(t, m.Name, "every tool must have a name")
		assert.NotEmpty(t, m.Description, "every tool must have a description")
	}
	// Self-hosted tools (caution / dangerous zones) were parked into the
	// archive — the registry now lists only coordination + capability tools.
	assert.Greater(t, zones[ZoneSafe], 0, "should have safe tools")
}
