package tools

import "github.com/syntheticinc/bytebrew/engine/internal/domain"

// DefaultToolClassifier implements domain.ToolClassifier for the runtime
// streaming layer. After self-hosted tools were parked and ask_user was
// removed, every builtin tool runs server-side; the proxied set is empty
// but kept for forward compatibility (e.g. future client-proxy tools).
type DefaultToolClassifier struct {
	proxiedTools    map[string]bool
	serverSideTools map[string]bool
}

// NewToolClassifier creates a new DefaultToolClassifier with predefined tool classifications.
func NewToolClassifier() *DefaultToolClassifier {
	return &DefaultToolClassifier{
		proxiedTools: map[string]bool{},
		serverSideTools: map[string]bool{
			"manage_tasks": true,
			"spawn_agent":  true,
		},
	}
}

// ClassifyTool returns the type of the given tool.
func (c *DefaultToolClassifier) ClassifyTool(toolName string) domain.ToolType {
	if c.proxiedTools[toolName] {
		return domain.ToolTypeProxied
	}
	if c.serverSideTools[toolName] {
		return domain.ToolTypeServerSide
	}
	// Default to server-side for unknown tools (capability + MCP tools).
	return domain.ToolTypeServerSide
}

// IsProxied returns true if the tool is executed on the client side.
func (c *DefaultToolClassifier) IsProxied(toolName string) bool {
	return c.ClassifyTool(toolName) == domain.ToolTypeProxied
}

// IsServerSide returns true if the tool is executed on the server side.
func (c *DefaultToolClassifier) IsServerSide(toolName string) bool {
	return c.ClassifyTool(toolName) == domain.ToolTypeServerSide
}
