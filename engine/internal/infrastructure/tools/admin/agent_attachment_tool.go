package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools"
)

// These tools expose granular MCP-attachment and builtin-tool-attachment
// surfaces on top of admin_update_agent. They exist because admin_update_agent
// is a full-row replace — the LLM would otherwise have to re-send every
// existing field just to append one MCP server or one builtin tool to an
// agent, which is fragile and verbose. Every tool here is idempotent: attach
// is a no-op when the link already exists, detach is a no-op when it doesn't.

// --- admin_attach_mcp_server_to_agent ---

type adminAttachMCPServerToAgentTool struct {
	repo     AgentRepository
	reloader func(context.Context)
}

// NewAdminAttachMCPServerToAgentTool wires the attach tool. The repo is used
// for both the read (to fetch the current MCPServers slice) and the write
// (full-row Update with the appended server). Idempotent: attaching the same
// server twice is a no-op.
func NewAdminAttachMCPServerToAgentTool(repo AgentRepository, reloader func(context.Context)) tool.InvokableTool {
	return &adminAttachMCPServerToAgentTool{repo: repo, reloader: reloader}
}

func (t *adminAttachMCPServerToAgentTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_attach_mcp_server_to_agent",
		Desc: "Attaches an MCP server to an agent by name. Idempotent — attaching the same server twice is a no-op.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"agent_name":  {Type: schema.String, Desc: "Agent name", Required: true},
			"server_name": {Type: schema.String, Desc: "MCP server name", Required: true},
		}),
	}, nil
}

type attachMCPServerArgs struct {
	AgentName  string `json:"agent_name"`
	ServerName string `json:"server_name"`
}

func (t *adminAttachMCPServerToAgentTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args attachMCPServerArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.AgentName == "" {
		return "[ERROR] agent_name is required", nil
	}
	if args.ServerName == "" {
		return "[ERROR] server_name is required", nil
	}

	existing, err := t.repo.GetByName(ctx, args.AgentName)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Sprintf("[ERROR] Agent not found: %s", args.AgentName), nil
		}
		return fmt.Sprintf("[ERROR] Failed to get agent: %s", tools.SanitizeDBError(err)), nil
	}

	for _, s := range existing.MCPServers {
		if s == args.ServerName {
			// Idempotent no-op — reloader not triggered because nothing changed.
			return fmt.Sprintf("MCP server %q is already attached to agent %q.", args.ServerName, args.AgentName), nil
		}
	}

	updated := cloneAgentRecord(existing)
	updated.MCPServers = append(updated.MCPServers, args.ServerName)

	if err := t.repo.Update(ctx, args.AgentName, updated); err != nil {
		return fmt.Sprintf("[ERROR] Failed to update agent: %s", tools.SanitizeDBError(err)), nil
	}
	if t.reloader != nil {
		t.reloader(ctx)
	}
	slog.InfoContext(ctx, "[AdminAttachMCPServerToAgent] attached", "agent", args.AgentName, "server", args.ServerName)
	return fmt.Sprintf("Attached MCP server %q to agent %q.", args.ServerName, args.AgentName), nil
}

// --- admin_detach_mcp_server_from_agent ---

type adminDetachMCPServerFromAgentTool struct {
	repo     AgentRepository
	reloader func(context.Context)
}

func NewAdminDetachMCPServerFromAgentTool(repo AgentRepository, reloader func(context.Context)) tool.InvokableTool {
	return &adminDetachMCPServerFromAgentTool{repo: repo, reloader: reloader}
}

func (t *adminDetachMCPServerFromAgentTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_detach_mcp_server_from_agent",
		Desc: "Detaches an MCP server from an agent by name. Idempotent — detaching an unlinked server is a no-op.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"agent_name":  {Type: schema.String, Desc: "Agent name", Required: true},
			"server_name": {Type: schema.String, Desc: "MCP server name", Required: true},
		}),
	}, nil
}

func (t *adminDetachMCPServerFromAgentTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args attachMCPServerArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.AgentName == "" {
		return "[ERROR] agent_name is required", nil
	}
	if args.ServerName == "" {
		return "[ERROR] server_name is required", nil
	}

	existing, err := t.repo.GetByName(ctx, args.AgentName)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Sprintf("[ERROR] Agent not found: %s", args.AgentName), nil
		}
		return fmt.Sprintf("[ERROR] Failed to get agent: %s", tools.SanitizeDBError(err)), nil
	}

	filtered := make([]string, 0, len(existing.MCPServers))
	removed := false
	for _, s := range existing.MCPServers {
		if s == args.ServerName {
			removed = true
			continue
		}
		filtered = append(filtered, s)
	}
	if !removed {
		return fmt.Sprintf("MCP server %q is not attached to agent %q.", args.ServerName, args.AgentName), nil
	}

	updated := cloneAgentRecord(existing)
	updated.MCPServers = filtered

	if err := t.repo.Update(ctx, args.AgentName, updated); err != nil {
		return fmt.Sprintf("[ERROR] Failed to update agent: %s", tools.SanitizeDBError(err)), nil
	}
	if t.reloader != nil {
		t.reloader(ctx)
	}
	slog.InfoContext(ctx, "[AdminDetachMCPServerFromAgent] detached", "agent", args.AgentName, "server", args.ServerName)
	return fmt.Sprintf("Detached MCP server %q from agent %q.", args.ServerName, args.AgentName), nil
}

// --- admin_add_builtin_tool_to_agent ---

type adminAddBuiltinToolToAgentTool struct {
	repo     AgentRepository
	reloader func(context.Context)
}

func NewAdminAddBuiltinToolToAgentTool(repo AgentRepository, reloader func(context.Context)) tool.InvokableTool {
	return &adminAddBuiltinToolToAgentTool{repo: repo, reloader: reloader}
}

func (t *adminAddBuiltinToolToAgentTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_add_builtin_tool_to_agent",
		Desc: "Adds a builtin tool to an agent by name. Idempotent — adding the same tool twice is a no-op.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"agent_name": {Type: schema.String, Desc: "Agent name", Required: true},
			"tool_name":  {Type: schema.String, Desc: "Builtin tool name", Required: true},
		}),
	}, nil
}

type agentBuiltinToolArgs struct {
	AgentName string `json:"agent_name"`
	ToolName  string `json:"tool_name"`
}

func (t *adminAddBuiltinToolToAgentTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args agentBuiltinToolArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.AgentName == "" {
		return "[ERROR] agent_name is required", nil
	}
	if args.ToolName == "" {
		return "[ERROR] tool_name is required", nil
	}

	existing, err := t.repo.GetByName(ctx, args.AgentName)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Sprintf("[ERROR] Agent not found: %s", args.AgentName), nil
		}
		return fmt.Sprintf("[ERROR] Failed to get agent: %s", tools.SanitizeDBError(err)), nil
	}

	for _, name := range existing.BuiltinTools {
		if name == args.ToolName {
			return fmt.Sprintf("Builtin tool %q is already on agent %q.", args.ToolName, args.AgentName), nil
		}
	}

	updated := cloneAgentRecord(existing)
	updated.BuiltinTools = append(updated.BuiltinTools, args.ToolName)

	if err := t.repo.Update(ctx, args.AgentName, updated); err != nil {
		return fmt.Sprintf("[ERROR] Failed to update agent: %s", tools.SanitizeDBError(err)), nil
	}
	if t.reloader != nil {
		t.reloader(ctx)
	}
	slog.InfoContext(ctx, "[AdminAddBuiltinToolToAgent] added", "agent", args.AgentName, "tool", args.ToolName)
	return fmt.Sprintf("Added builtin tool %q to agent %q.", args.ToolName, args.AgentName), nil
}

// --- admin_remove_builtin_tool_from_agent ---

type adminRemoveBuiltinToolFromAgentTool struct {
	repo     AgentRepository
	reloader func(context.Context)
}

func NewAdminRemoveBuiltinToolFromAgentTool(repo AgentRepository, reloader func(context.Context)) tool.InvokableTool {
	return &adminRemoveBuiltinToolFromAgentTool{repo: repo, reloader: reloader}
}

func (t *adminRemoveBuiltinToolFromAgentTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_remove_builtin_tool_from_agent",
		Desc: "Removes a builtin tool from an agent by name. Idempotent — removing a tool that isn't there is a no-op.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"agent_name": {Type: schema.String, Desc: "Agent name", Required: true},
			"tool_name":  {Type: schema.String, Desc: "Builtin tool name", Required: true},
		}),
	}, nil
}

func (t *adminRemoveBuiltinToolFromAgentTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args agentBuiltinToolArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.AgentName == "" {
		return "[ERROR] agent_name is required", nil
	}
	if args.ToolName == "" {
		return "[ERROR] tool_name is required", nil
	}

	existing, err := t.repo.GetByName(ctx, args.AgentName)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Sprintf("[ERROR] Agent not found: %s", args.AgentName), nil
		}
		return fmt.Sprintf("[ERROR] Failed to get agent: %s", tools.SanitizeDBError(err)), nil
	}

	filtered := make([]string, 0, len(existing.BuiltinTools))
	removed := false
	for _, name := range existing.BuiltinTools {
		if name == args.ToolName {
			removed = true
			continue
		}
		filtered = append(filtered, name)
	}
	if !removed {
		return fmt.Sprintf("Builtin tool %q is not on agent %q.", args.ToolName, args.AgentName), nil
	}

	updated := cloneAgentRecord(existing)
	updated.BuiltinTools = filtered

	if err := t.repo.Update(ctx, args.AgentName, updated); err != nil {
		return fmt.Sprintf("[ERROR] Failed to update agent: %s", tools.SanitizeDBError(err)), nil
	}
	if t.reloader != nil {
		t.reloader(ctx)
	}
	slog.InfoContext(ctx, "[AdminRemoveBuiltinToolFromAgent] removed", "agent", args.AgentName, "tool", args.ToolName)
	return fmt.Sprintf("Removed builtin tool %q from agent %q.", args.ToolName, args.AgentName), nil
}

// cloneAgentRecord returns a pointer to a shallow copy of the AgentRecord so
// callers can mutate slice fields without aliasing the repo's returned value.
// Caller is expected to reassign slice fields — they are not deep-copied.
func cloneAgentRecord(src *AgentRecord) *AgentRecord {
	if src == nil {
		return nil
	}
	dup := *src
	return &dup
}
