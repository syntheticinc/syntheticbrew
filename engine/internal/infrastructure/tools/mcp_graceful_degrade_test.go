package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/stretchr/testify/require"
)

// erroringMCPProvider always fails GetMCPTools — simulates an unreachable or
// misconfigured MCP server (bad URL, server down, not yet dialed).
type erroringMCPProvider struct{}

func (erroringMCPProvider) GetMCPTools(_ context.Context, name string) ([]tool.InvokableTool, error) {
	return nil, errors.New("dial " + name + ": connection refused")
}

// TestResolve_UnreachableMCPServer_SkippedNotFatal is the F2 regression guard.
//
// The legacy Resolve path (used by the chat turn) aborted ALL tool resolution
// when any attached MCP server failed to resolve, surfacing an opaque
// INTERNAL_ERROR and dropping the answer — one bad MCP server bricked the whole
// turn. The fix skips the unreachable server (WARN) and resolves the rest.
//
// RED (pre-fix): Resolve returns (nil, error).
// GREEN (post-fix): Resolve returns the builtin tool with no error.
func TestResolve_UnreachableMCPServer_SkippedNotFatal(t *testing.T) {
	store := NewBuiltinToolStore()
	store.Register("test_tool", func(_ ToolDependencies) tool.InvokableTool {
		return &stubTool{name: "test_tool"}
	})

	resolver := NewAgentToolResolver(store)
	resolver.SetMCPProvider(erroringMCPProvider{})

	tools, err := resolver.Resolve(context.Background(), []string{"test_tool"}, ToolDependencies{
		AgentName:  "support-bot",
		MCPServers: []string{"unreachable-mcp"},
	})

	require.NoError(t, err, "an unreachable attached MCP server must not fail the whole resolve")
	require.Len(t, tools, 1, "the builtin tool must still resolve when the unreachable MCP server is skipped")
}
