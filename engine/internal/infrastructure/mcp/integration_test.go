package mcp

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPIntegration_StdioEchoServer(t *testing.T) {
	// Skip if node not available
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found, skipping MCP integration test")
	}

	transport := NewStdioTransport("node", []string{"../../../testdata/echo-mcp-server.js"}, nil)
	client := NewClient("echo-test", transport)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// AC-3.1: Connect and discover tools
	err := client.Connect(ctx)
	require.NoError(t, err, "AC-3.1: MCP stdio connect should succeed")

	tools := client.ListTools()
	assert.Len(t, tools, 1, "AC-3.1: Should discover 1 tool (echo)")
	assert.Equal(t, "echo", tools[0].Name)
	t.Logf("AC-3.1 PASS: Connected, discovered %d tools: %s", len(tools), tools[0].Name)

	// AC-3.2: Call tool
	result, _, err := client.CallTool(ctx, "echo", map[string]interface{}{"message": "hello from SyntheticBrew"})
	require.NoError(t, err, "AC-3.2: Tool call should succeed")
	assert.Contains(t, result, "Echo: hello from SyntheticBrew")
	t.Logf("AC-3.2 PASS: Tool result: %s", result)

	// Cleanup
	client.Close()
}
