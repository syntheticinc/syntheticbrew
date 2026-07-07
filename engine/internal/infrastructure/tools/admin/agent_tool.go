package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// --- admin_list_agents ---

type adminListAgentsTool struct {
	repo AgentRepository
}

func NewAdminListAgentsTool(repo AgentRepository) tool.InvokableTool {
	return &adminListAgentsTool{repo: repo}
}

func (t *adminListAgentsTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name:        "admin_list_agents",
		Desc:        "Lists all agents configured in the engine. Returns name, lifecycle, model, tool count, and system flag for each agent.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{}),
	}, nil
}

func (t *adminListAgentsTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	agents, err := t.repo.List(ctx)
	if err != nil {
		return fmt.Sprintf("[ERROR] Failed to list agents: %v", err), nil
	}

	if len(agents) == 0 {
		return "No agents configured.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## %d agents\n\n", len(agents)))
	for _, a := range agents {
		sb.WriteString(fmt.Sprintf("- **%s** (lifecycle=%s, model=%s, tools=%d, system=%v)\n",
			a.Name, a.Lifecycle, coalesce(a.ModelName, "none"), len(a.BuiltinTools), a.IsSystem))
	}
	return sb.String(), nil
}

// --- admin_get_agent ---

type adminGetAgentTool struct {
	repo AgentRepository
}

func NewAdminGetAgentTool(repo AgentRepository) tool.InvokableTool {
	return &adminGetAgentTool{repo: repo}
}

func (t *adminGetAgentTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_get_agent",
		Desc: "Returns full details of a single agent by name, including system prompt, tools, MCP servers, and spawn targets.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"name": {Type: schema.String, Desc: "Agent name", Required: true},
		}),
	}, nil
}

type getAgentArgs struct {
	Name string `json:"name"`
}

func (t *adminGetAgentTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args getAgentArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.Name == "" {
		return "[ERROR] name is required", nil
	}

	agent, err := t.repo.GetByName(ctx, args.Name)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Sprintf("Agent not found: %s", args.Name), nil
		}
		return fmt.Sprintf("[ERROR] Failed to get agent: %v", err), nil
	}

	data, err := json.MarshalIndent(agent, "", "  ")
	if err != nil {
		return fmt.Sprintf("[ERROR] failed to serialize result: %v", err), nil
	}
	return string(data), nil
}

// --- admin_create_agent ---

type adminCreateAgentTool struct {
	repo     AgentRepository
	reloader func(context.Context)
}

func NewAdminCreateAgentTool(repo AgentRepository, reloader func(context.Context)) tool.InvokableTool {
	return &adminCreateAgentTool{repo: repo, reloader: reloader}
}

func (t *adminCreateAgentTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_create_agent",
		Desc: "Creates a new agent. Requires name and system_prompt. Optional: model, lifecycle (persistent/ephemeral, default persistent), tool_execution (sequential/parallel), max_steps, builtin_tools array.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"name":           {Type: schema.String, Desc: "Unique agent name (lowercase, hyphens allowed)", Required: true},
			"system_prompt":  {Type: schema.String, Desc: "System prompt for the agent", Required: true},
			"model":          {Type: schema.String, Desc: "Model name to use", Required: false},
			"lifecycle":      {Type: schema.String, Desc: "Agent lifecycle: persistent or ephemeral (default: persistent)", Required: false},
			"tool_execution": {Type: schema.String, Desc: "Tool execution mode: sequential or parallel (default: sequential)", Required: false},
			"max_steps":      {Type: schema.Integer, Desc: "Max steps per turn (default: 0 = unlimited)", Required: false},
			"builtin_tools":  {Type: schema.Array, Desc: "Array of builtin tool names to assign", Required: false},
		}),
	}, nil
}

type createAgentArgs struct {
	Name          string   `json:"name"`
	SystemPrompt  string   `json:"system_prompt"`
	Model         string   `json:"model"`
	Lifecycle     string   `json:"lifecycle"`
	ToolExecution string   `json:"tool_execution"`
	MaxSteps      int      `json:"max_steps"`
	BuiltinTools  []string `json:"builtin_tools"`
}

func (t *adminCreateAgentTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args createAgentArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.Name == "" {
		return "[ERROR] name is required", nil
	}
	if len(args.Name) > 255 || !domain.AgentNameRe.MatchString(args.Name) {
		return fmt.Sprintf("Invalid agent name %q. Must match ^[a-z][a-z0-9-]* (lowercase letters, digits, hyphens; start with letter; max 255 chars).", args.Name), nil
	}
	if args.SystemPrompt == "" {
		return "[ERROR] system_prompt is required", nil
	}
	if msg := rejectManagementTools(args.BuiltinTools); msg != "" {
		return msg, nil
	}

	if args.Lifecycle == "" {
		args.Lifecycle = "persistent"
	}
	if args.ToolExecution == "" {
		args.ToolExecution = "sequential"
	}

	record := &AgentRecord{
		Name:          args.Name,
		SystemPrompt:  args.SystemPrompt,
		ModelName:     args.Model,
		Lifecycle:     args.Lifecycle,
		ToolExecution: args.ToolExecution,
		MaxSteps:      args.MaxSteps,
		BuiltinTools:  args.BuiltinTools,
	}

	if err := t.repo.Create(ctx, record); err != nil {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "already exists") {
			return fmt.Sprintf("Agent with name %q already exists.", args.Name), nil
		}
		return fmt.Sprintf("[ERROR] Failed to create agent: %v", err), nil
	}

	t.reload(ctx)
	slog.InfoContext(ctx, "[AdminCreateAgent] created agent", "name", args.Name)
	return fmt.Sprintf("Agent %q created successfully (lifecycle=%s, model=%s).", args.Name, args.Lifecycle, coalesce(args.Model, "none")), nil
}

func (t *adminCreateAgentTool) reload(ctx context.Context) {
	if t.reloader != nil {
		t.reloader(ctx)
	}
}

// --- admin_update_agent ---

type adminUpdateAgentTool struct {
	repo     AgentRepository
	reloader func(context.Context)
}

func NewAdminUpdateAgentTool(repo AgentRepository, reloader func(context.Context)) tool.InvokableTool {
	return &adminUpdateAgentTool{repo: repo, reloader: reloader}
}

func (t *adminUpdateAgentTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_update_agent",
		Desc: "Updates an existing agent by name. All provided fields replace the current values.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"name":           {Type: schema.String, Desc: "Agent name to update", Required: true},
			"system_prompt":  {Type: schema.String, Desc: "New system prompt", Required: false},
			"model":          {Type: schema.String, Desc: "New model name", Required: false},
			"lifecycle":      {Type: schema.String, Desc: "New lifecycle", Required: false},
			"tool_execution": {Type: schema.String, Desc: "New tool execution mode", Required: false},
			"max_steps":      {Type: schema.Integer, Desc: "New max steps", Required: false},
			"builtin_tools":  {Type: schema.Array, Desc: "New builtin tools array (replaces existing)", Required: false},
		}),
	}, nil
}

type updateAgentArgs struct {
	Name          string   `json:"name"`
	SystemPrompt  string   `json:"system_prompt"`
	Model         string   `json:"model"`
	Lifecycle     string   `json:"lifecycle"`
	ToolExecution string   `json:"tool_execution"`
	MaxSteps      int      `json:"max_steps"`
	BuiltinTools  []string `json:"builtin_tools"`
}

func (t *adminUpdateAgentTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args updateAgentArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.Name == "" {
		return "[ERROR] name is required", nil
	}
	if msg := rejectManagementTools(args.BuiltinTools); msg != "" {
		return msg, nil
	}

	// Fetch existing to merge non-provided fields.
	existing, err := t.repo.GetByName(ctx, args.Name)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Sprintf("Agent not found: %s", args.Name), nil
		}
		return fmt.Sprintf("[ERROR] Failed to get agent: %v", err), nil
	}

	record := &AgentRecord{
		Name:          args.Name,
		SystemPrompt:  coalesce(args.SystemPrompt, existing.SystemPrompt),
		ModelName:     coalesce(args.Model, existing.ModelName),
		Lifecycle:     coalesce(args.Lifecycle, existing.Lifecycle),
		ToolExecution: coalesce(args.ToolExecution, existing.ToolExecution),
		MaxSteps:      args.MaxSteps,
		BuiltinTools:  args.BuiltinTools,
		IsSystem:      existing.IsSystem,
	}
	if record.MaxSteps == 0 {
		record.MaxSteps = existing.MaxSteps
	}
	if record.BuiltinTools == nil {
		record.BuiltinTools = existing.BuiltinTools
	}

	if err := t.repo.Update(ctx, args.Name, record); err != nil {
		return fmt.Sprintf("[ERROR] Failed to update agent: %v", err), nil
	}

	t.reload(ctx)
	slog.InfoContext(ctx, "[AdminUpdateAgent] updated agent", "name", args.Name)
	return fmt.Sprintf("Agent %q updated successfully.", args.Name), nil
}

func (t *adminUpdateAgentTool) reload(ctx context.Context) {
	if t.reloader != nil {
		t.reloader(ctx)
	}
}

// --- admin_delete_agent ---

type adminDeleteAgentTool struct {
	repo     AgentRepository
	reloader func(context.Context)
}

func NewAdminDeleteAgentTool(repo AgentRepository, reloader func(context.Context)) tool.InvokableTool {
	return &adminDeleteAgentTool{repo: repo, reloader: reloader}
}

func (t *adminDeleteAgentTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_delete_agent",
		Desc: "Deletes an agent by name. WARNING: This is destructive and cannot be undone.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"name": {Type: schema.String, Desc: "Agent name to delete", Required: true},
		}),
	}, nil
}

type deleteAgentArgs struct {
	Name string `json:"name"`
}

func (t *adminDeleteAgentTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args deleteAgentArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.Name == "" {
		return "[ERROR] name is required", nil
	}

	existing, err := t.repo.GetByName(ctx, args.Name)
	if err == nil && existing != nil && existing.IsSystem {
		return fmt.Sprintf("System agent %q cannot be deleted. Use the restore endpoint to reset it to factory defaults.", args.Name), nil
	}

	if err := t.repo.Delete(ctx, args.Name); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Sprintf("Agent not found: %s", args.Name), nil
		}
		return fmt.Sprintf("[ERROR] Failed to delete agent: %v", err), nil
	}

	if t.reloader != nil {
		t.reloader(ctx)
	}
	slog.InfoContext(ctx, "[AdminDeleteAgent] deleted agent", "name", args.Name)
	return fmt.Sprintf("Agent %q deleted successfully.", args.Name), nil
}

// coalesce returns the first non-empty string.
func coalesce(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// rejectManagementTools returns a user-facing error message if any name in the
// list is a management-plane tool (admin_*, provision_agent, get_embed_snippet).
// Defense in depth: even though tool resolution already gates these to system
// agents, this stops a dangerous config from ever being stored on a
// user-provisioned agent. Empty string means the list is clean.
func rejectManagementTools(tools []string) string {
	for _, name := range tools {
		if domain.IsManagementTool(name) {
			return fmt.Sprintf("tool %q cannot be granted to an agent", name)
		}
	}
	return ""
}
