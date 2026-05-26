package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdaptMCPTool_Info(t *testing.T) {
	inputSchema, _ := json.Marshal(map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "File path to read",
			},
			"encoding": map[string]interface{}{
				"type":        "string",
				"description": "File encoding",
			},
		},
		"required": []string{"path"},
	})

	mcpTool := MCPTool{
		Name:        "read_file",
		Description: "Read a file from disk",
		InputSchema: inputSchema,
	}

	client := NewClient("test", newMockTransport())
	adapted := AdaptMCPTool(client, mcpTool)

	info, err := adapted.Info(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "read_file", info.Name)
	assert.Equal(t, "Read a file from disk", info.Desc)

	// Verify params via JSON Schema conversion (params field is unexported)
	require.NotNil(t, info.ParamsOneOf)
	jsonSchema, err := info.ParamsOneOf.ToJSONSchema()
	require.NoError(t, err)
	require.NotNil(t, jsonSchema)

	// Check properties exist
	pathProp, ok := jsonSchema.Properties.Get("path")
	require.True(t, ok, "expected 'path' property")
	assert.Equal(t, "string", pathProp.Type)
	assert.Equal(t, "File path to read", pathProp.Description)

	_, ok = jsonSchema.Properties.Get("encoding")
	assert.True(t, ok, "expected 'encoding' property")

	// Check required
	assert.Contains(t, jsonSchema.Required, "path")
	assert.NotContains(t, jsonSchema.Required, "encoding")
}

func TestAdaptMCPTool_InfoEmptySchema(t *testing.T) {
	mcpTool := MCPTool{
		Name:        "no_params_tool",
		Description: "Tool with no params",
	}

	client := NewClient("test", newMockTransport())
	adapted := AdaptMCPTool(client, mcpTool)

	info, err := adapted.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "no_params_tool", info.Name)
}

func TestAdaptMCPTool_InvokableRun(t *testing.T) {
	transport := newMockTransport()
	result, _ := json.Marshal(ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: "success result"}},
	})
	transport.responses["tools/call"] = &Response{JSONRPC: "2.0", ID: 1, Result: result}

	client := NewClient("test", transport)
	mcpTool := MCPTool{Name: "my_tool", Description: "A tool"}
	adapted := AdaptMCPTool(client, mcpTool)

	output, err := adapted.InvokableRun(context.Background(), `{"key": "value"}`)
	require.NoError(t, err)
	assert.Equal(t, "success result", output)
}

func TestAdaptMCPTool_InvokableRunInvalidJSON(t *testing.T) {
	client := NewClient("test", newMockTransport())
	mcpTool := MCPTool{Name: "my_tool", Description: "A tool"}
	adapted := AdaptMCPTool(client, mcpTool)

	_, err := adapted.InvokableRun(context.Background(), "not json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse args")
}

// TestAdaptMCPTool_InvokableRun_IsError verifies the [ERROR]-convention:
// MCP application-level errors (isError: true) are returned as normal
// content with an "[ERROR] " prefix and a nil Go error. This stops the
// tool result text — which is fully controlled by the MCP server (i.e.
// the partner / tool author) — from ever surfacing as a Go error to the
// agent layer, where it would risk being treated as a platform-level
// control-flow signal.
func TestAdaptMCPTool_InvokableRun_IsError(t *testing.T) {
	transport := newMockTransport()
	result, _ := json.Marshal(ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: "permission denied: /etc/shadow"}},
		IsError: true,
	})
	transport.responses["tools/call"] = &Response{JSONRPC: "2.0", ID: 1, Result: result}

	client := NewClient("test", transport)
	mcpTool := MCPTool{Name: "read_file", Description: "Read a file"}
	adapted := AdaptMCPTool(client, mcpTool)

	output, err := adapted.InvokableRun(context.Background(), `{"path": "/etc/shadow"}`)
	require.NoError(t, err, "MCP isError must return as content+nil, not as Go error")
	assert.Equal(t, "[ERROR] permission denied: /etc/shadow", output)
}

// TestAdaptMCPTool_InvokableRun_IsError_PartnerRegression is the
// partner-bug regression guard: an RBAC-style "Permission denied"
// message from a partner MCP server (e.g. Chirp's rule_create with
// insufficient access) must reach the agent layer as a tool-result
// string, NOT as a Go error that the recovery classifier could grep.
func TestAdaptMCPTool_InvokableRun_IsError_PartnerRegression(t *testing.T) {
	transport := newMockTransport()
	rbacMessage := "ERROR: Permission denied. The user does not have access to this resource."
	result, _ := json.Marshal(ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: rbacMessage}},
		IsError: true,
	})
	transport.responses["tools/call"] = &Response{JSONRPC: "2.0", ID: 1, Result: result}

	client := NewClient("test", transport)
	mcpTool := MCPTool{Name: "rule_create", Description: "Create a rule"}
	adapted := AdaptMCPTool(client, mcpTool)

	output, err := adapted.InvokableRun(context.Background(), `{"name": "x"}`)
	require.NoError(t, err, "partner RBAC error must NOT bubble as Go error")
	assert.Equal(t, "[ERROR] "+rbacMessage, output)
}

func TestParseJSONSchemaToParams(t *testing.T) {
	tests := []struct {
		name       string
		schema     string
		wantParams int
	}{
		{
			name:       "empty schema",
			schema:     "",
			wantParams: 0,
		},
		{
			name:       "invalid json",
			schema:     "{invalid",
			wantParams: 0,
		},
		{
			name:       "no properties",
			schema:     `{"type": "object"}`,
			wantParams: 0,
		},
		{
			name:       "with properties",
			schema:     `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"number"}},"required":["a"]}`,
			wantParams: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := parseJSONSchemaToParams(json.RawMessage(tt.schema))
			if tt.wantParams == 0 {
				assert.Empty(t, params)
				return
			}
			assert.Len(t, params, tt.wantParams)
		})
	}
}
