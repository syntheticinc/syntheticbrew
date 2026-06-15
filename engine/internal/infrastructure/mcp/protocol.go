package mcp

import (
	"context"
	"encoding/json"
)

// JSON-RPC 2.0 types for MCP protocol.

// Request represents a JSON-RPC 2.0 request or notification.
type Request struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"` // nil for notifications
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// Response represents a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError represents a JSON-RPC 2.0 error.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return e.Message }

// metaKeyReturnDirectly is the MCP `_meta` key an MCP tool sets to declare itself
// terminal: after it runs the agent turn ends with the tool result as the final
// answer, no follow-up model call. The key is namespaced under a domain we own,
// per the MCP spec's `_meta` naming rules (prefix = dotted labels + "/"), so it
// never collides with the reserved `mcp`/`modelcontextprotocol` namespace.
const metaKeyReturnDirectly = "syntheticbrew.ai/return-directly"

// MCPTool describes a tool provided by an MCP server.
type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"` // JSON Schema
	Meta        map[string]any  `json:"_meta,omitempty"`
}

// ReturnsDirectly reports whether the tool declared return-directly via its MCP
// `_meta`. Strictly a boolean true counts — any other shape (missing, string,
// number, null) is treated as not declared, so a malformed `_meta` degrades to
// the default (off) rather than failing ingestion.
func (t MCPTool) ReturnsDirectly() bool {
	v, ok := t.Meta[metaKeyReturnDirectly]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

// ToolsListResult is the result of tools/list.
type ToolsListResult struct {
	Tools []MCPTool `json:"tools"`
}

// ToolCallResult is the result of tools/call.
type ToolCallResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ToolContent represents a content block in a tool call result.
type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Transport is the interface for MCP server communication.
type Transport interface {
	Start(ctx context.Context) error
	Send(ctx context.Context, req *Request) (*Response, error)
	Notify(ctx context.Context, req *Request)
	Close() error
}

func parseToolsFromResponse(resp *Response) ([]MCPTool, error) {
	if resp.Error != nil {
		return nil, resp.Error
	}
	var result ToolsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

func extractToolResult(resp *Response) (string, bool, error) {
	if resp.Error != nil {
		return "", false, resp.Error
	}
	var result ToolCallResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return string(resp.Result), false, nil // fallback to raw
	}
	var texts []string
	for _, c := range result.Content {
		if c.Type == "text" {
			texts = append(texts, c.Text)
		}
	}
	if len(texts) == 0 {
		return string(resp.Result), result.IsError, nil
	}
	return texts[0], result.IsError, nil
}
