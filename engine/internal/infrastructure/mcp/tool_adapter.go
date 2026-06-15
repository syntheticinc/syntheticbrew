package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// AdaptMCPTool converts an MCP tool to Eino InvokableTool.
func AdaptMCPTool(client *Client, mcpTool MCPTool) tool.InvokableTool {
	return &mcpToolAdapter{client: client, mcpTool: mcpTool}
}

type mcpToolAdapter struct {
	client  *Client
	mcpTool MCPTool
}

func (a *mcpToolAdapter) Info(_ context.Context) (*schema.ToolInfo, error) {
	params := parseJSONSchemaToParams(a.mcpTool.InputSchema)
	info := &schema.ToolInfo{
		Name:        a.mcpTool.Name,
		Desc:        a.mcpTool.Description,
		ParamsOneOf: schema.NewParamsOneOfByParams(params),
	}
	// Carry the tool's self-declared return-directly intent to the ReAct loop via
	// ToolInfo.Extra. The tool wrappers delegate Info(), so this survives wrapping.
	if a.mcpTool.ReturnsDirectly() {
		info.Extra = map[string]any{domain.ToolExtraReturnDirectly: true}
	}
	return info, nil
}

func (a *mcpToolAdapter) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	result, isError, err := a.client.CallTool(ctx, a.mcpTool.Name, args)
	if err != nil {
		// Transport-level failure (network down, MCP server crashed,
		// malformed JSON-RPC). These are genuine platform errors and
		// should bubble up as Go errors so the agent layer can react.
		return "", err
	}
	if isError {
		// Tool-level error (MCP `isError: true`): the server responded
		// successfully but the tool itself reports a failure. Return as
		// normal content with an [ERROR] marker so callbacks/OnToolEnd
		// lifts it into event.Error via the existing prefix-detection
		// path (tool_event_handler.go:177-179) and Eino does NOT abort
		// the turn. The text from `result` is fully controlled by the
		// MCP server (partner-controlled), so it must never cross into
		// the platform recovery classifier as a Go error.
		return "[ERROR] " + result, nil
	}
	return result, nil
}

// parseJSONSchemaToParams converts JSON Schema to Eino params.
// Handles top-level properties only.
func parseJSONSchemaToParams(schemaJSON json.RawMessage) map[string]*schema.ParameterInfo {
	if len(schemaJSON) == 0 {
		return nil
	}

	var s struct {
		Properties map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		return nil
	}

	requiredSet := make(map[string]bool, len(s.Required))
	for _, r := range s.Required {
		requiredSet[r] = true
	}

	params := make(map[string]*schema.ParameterInfo, len(s.Properties))
	for name, prop := range s.Properties {
		params[name] = &schema.ParameterInfo{
			Type:     schema.DataType(prop.Type),
			Desc:     prop.Description,
			Required: requiredSet[name],
		}
	}
	return params
}
