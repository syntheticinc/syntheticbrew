package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/internal/service/mcp"
)

// allowedMCPTransports matches target-schema.dbml mcp_servers.type CHECK:
//
//	stdio | http | sse | streamable-http
var allowedMCPTransports = map[string]struct{}{
	"stdio":           {},
	"http":            {},
	"sse":             {},
	"streamable-http": {},
}

// isAllowedMCPTransport reports whether the transport value is one of the
// four DBML-permitted values. Legacy "docker" must be rejected here before
// it reaches the DB CHECK constraint.
func isAllowedMCPTransport(t string) bool {
	_, ok := allowedMCPTransports[t]
	return ok
}

// --- admin_list_mcp_servers ---

type adminListMCPServersTool struct {
	repo MCPServerRepository
}

func NewAdminListMCPServersTool(repo MCPServerRepository) tool.InvokableTool {
	return &adminListMCPServersTool{repo: repo}
}

func (t *adminListMCPServersTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name:        "admin_list_mcp_servers",
		Desc:        "Lists all MCP servers. MCP servers provide external tools (web search, APIs, etc.) to agents.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{}),
	}, nil
}

func (t *adminListMCPServersTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	servers, err := t.repo.List(ctx)
	if err != nil {
		return fmt.Sprintf("[ERROR] Failed to list MCP servers: %v", err), nil
	}

	if len(servers) == 0 {
		return "No MCP servers configured.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## %d MCP servers\n\n", len(servers)))
	for _, s := range servers {
		// Mask env var values for security.
		envCount := len(s.EnvVars)
		sb.WriteString(fmt.Sprintf("- id=%s **%s** (type=%s, env_vars=%d, enabled=%v)\n", s.ID, s.Name, s.Type, envCount, s.Enabled))
	}
	return sb.String(), nil
}

// --- admin_create_mcp_server ---

type adminCreateMCPServerTool struct {
	repo     MCPServerRepository
	reloader func(context.Context)
	policy   mcp.TransportPolicy
	syncer   MCPClientSyncer
}

func NewAdminCreateMCPServerTool(repo MCPServerRepository, reloader func(context.Context), policy mcp.TransportPolicy, syncer MCPClientSyncer) tool.InvokableTool {
	return &adminCreateMCPServerTool{repo: repo, reloader: reloader, policy: policy, syncer: syncer}
}

func (t *adminCreateMCPServerTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_create_mcp_server",
		Desc: "Creates an MCP server configuration. For stdio: provide command and args. For sse/http/streamable-http: provide url. enabled defaults to true when omitted.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"name":     {Type: schema.String, Desc: "Server name", Required: true},
			"type":     {Type: schema.String, Desc: "Transport type: stdio, http, sse, streamable-http", Required: true},
			"command":  {Type: schema.String, Desc: "Command to run (for stdio)", Required: false},
			"url":      {Type: schema.String, Desc: "Server URL (for sse/http)", Required: false},
			"args":     {Type: schema.Array, Desc: "Command arguments array", Required: false},
			"env_vars": {Type: schema.Object, Desc: "Environment variables as key-value pairs", Required: false},
			"enabled":  {Type: schema.Boolean, Desc: "Whether the server is active (default true).", Required: false},
		}),
	}, nil
}

type createMCPServerArgs struct {
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	Command string            `json:"command"`
	URL     string            `json:"url"`
	Args    []string          `json:"args"`
	EnvVars map[string]string `json:"env_vars"`
	Enabled *bool             `json:"enabled,omitempty"`
}

func (t *adminCreateMCPServerTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args createMCPServerArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.Name == "" {
		return "[ERROR] name is required", nil
	}
	if args.Type == "" {
		return "[ERROR] type is required", nil
	}
	if !isAllowedMCPTransport(args.Type) {
		return "[ERROR] invalid transport type: must be one of stdio, http, sse, streamable-http", nil
	}
	if err := t.policy.IsAllowed(args.Type); err != nil {
		return "[ERROR] " + err.Error(), nil
	}

	// Default to enabled=true when the caller omits the flag — matches the
	// DB-level DEFAULT TRUE semantics so admin tools and raw INSERTs behave
	// identically.
	enabled := true
	if args.Enabled != nil {
		enabled = *args.Enabled
	}

	record := &MCPServerRecord{
		Name:    args.Name,
		Type:    args.Type,
		Command: args.Command,
		URL:     args.URL,
		Args:    args.Args,
		EnvVars: args.EnvVars,
		Enabled: enabled,
	}

	if err := t.repo.Create(ctx, record); err != nil {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Sprintf("MCP server with name %q already exists.", args.Name), nil
		}
		return fmt.Sprintf("[ERROR] Failed to create MCP server: %v", err), nil
	}

	if t.reloader != nil {
		t.reloader(ctx)
	}
	// Dial the freshly created server into the live per-tenant registry so
	// the very next turn resolves its tools without a restart. Fail-soft:
	// the DB is the source of truth and any subsequent reload picks it up.
	if t.syncer != nil {
		if err := t.syncer.ReconnectServer(ctx, args.Name); err != nil {
			slog.WarnContext(ctx, "[AdminCreateMCPServer] client sync failed", "name", args.Name, "error", err)
		}
	}

	slog.InfoContext(ctx, "[AdminCreateMCPServer] created", "name", args.Name, "type", args.Type)
	return fmt.Sprintf("MCP server %q created (id=%s, type=%s).", args.Name, record.ID, args.Type), nil
}

// --- admin_update_mcp_server ---

type adminUpdateMCPServerTool struct {
	repo     MCPServerRepository
	reloader func(context.Context)
	policy   mcp.TransportPolicy
	syncer   MCPClientSyncer
}

func NewAdminUpdateMCPServerTool(repo MCPServerRepository, reloader func(context.Context), policy mcp.TransportPolicy, syncer MCPClientSyncer) tool.InvokableTool {
	return &adminUpdateMCPServerTool{repo: repo, reloader: reloader, policy: policy, syncer: syncer}
}

func (t *adminUpdateMCPServerTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_update_mcp_server",
		Desc: "Updates an MCP server by ID. Passing enabled=false keeps the server configured but blocks it from being injected into agents.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"server_id": {Type: schema.String, Desc: "MCP server ID to update", Required: true},
			"name":      {Type: schema.String, Desc: "New name", Required: false},
			"type":      {Type: schema.String, Desc: "New type", Required: false},
			"command":   {Type: schema.String, Desc: "New command", Required: false},
			"url":       {Type: schema.String, Desc: "New URL", Required: false},
			"args":      {Type: schema.Array, Desc: "New args", Required: false},
			"env_vars":  {Type: schema.Object, Desc: "New env vars", Required: false},
			"enabled":   {Type: schema.Boolean, Desc: "New enabled state (omit to preserve).", Required: false},
		}),
	}, nil
}

type updateMCPServerArgs struct {
	ServerID string            `json:"server_id"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Command  string            `json:"command"`
	URL      string            `json:"url"`
	Args     []string          `json:"args"`
	EnvVars  map[string]string `json:"env_vars"`
	Enabled  *bool             `json:"enabled,omitempty"`
}

func (t *adminUpdateMCPServerTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args updateMCPServerArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.ServerID == "" {
		return "[ERROR] server_id is required", nil
	}
	if args.Type != "" && !isAllowedMCPTransport(args.Type) {
		return "[ERROR] invalid transport type: must be one of stdio, http, sse, streamable-http", nil
	}
	if args.Type != "" {
		if err := t.policy.IsAllowed(args.Type); err != nil {
			return "[ERROR] " + err.Error(), nil
		}
	}

	existing, err := t.repo.GetByID(ctx, args.ServerID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Sprintf("MCP server not found: %s", args.ServerID), nil
		}
		return fmt.Sprintf("[ERROR] Failed to get MCP server: %v", err), nil
	}

	record := &MCPServerRecord{
		Name:    coalesce(args.Name, existing.Name),
		Type:    coalesce(args.Type, existing.Type),
		Command: coalesce(args.Command, existing.Command),
		URL:     coalesce(args.URL, existing.URL),
		Args:    args.Args,
		EnvVars: args.EnvVars,
		Enabled: existing.Enabled,
	}
	if args.Enabled != nil {
		record.Enabled = *args.Enabled
	}
	if record.Args == nil {
		record.Args = existing.Args
	}
	if record.EnvVars == nil {
		record.EnvVars = existing.EnvVars
	}

	if err := t.repo.Update(ctx, args.ServerID, record); err != nil {
		return fmt.Sprintf("[ERROR] Failed to update MCP server: %v", err), nil
	}

	if t.reloader != nil {
		t.reloader(ctx)
	}
	// Redial under the post-write name (the update may have renamed it) so the
	// live registry reflects the new config. Fail-soft — DB is source of truth.
	if t.syncer != nil {
		if err := t.syncer.ReconnectServer(ctx, record.Name); err != nil {
			slog.WarnContext(ctx, "[AdminUpdateMCPServer] client sync failed", "name", record.Name, "error", err)
		}
	}

	slog.InfoContext(ctx, "[AdminUpdateMCPServer] updated", "id", args.ServerID)
	return fmt.Sprintf("MCP server %s updated successfully.", args.ServerID), nil
}

// --- admin_delete_mcp_server ---

type adminDeleteMCPServerTool struct {
	repo     MCPServerRepository
	reloader func(context.Context)
	syncer   MCPClientSyncer
}

func NewAdminDeleteMCPServerTool(repo MCPServerRepository, reloader func(context.Context), syncer MCPClientSyncer) tool.InvokableTool {
	return &adminDeleteMCPServerTool{repo: repo, reloader: reloader, syncer: syncer}
}

func (t *adminDeleteMCPServerTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_delete_mcp_server",
		Desc: "Deletes an MCP server by ID.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"server_id": {Type: schema.String, Desc: "MCP server ID to delete", Required: true},
		}),
	}, nil
}

type deleteMCPServerArgs struct {
	ServerID string `json:"server_id"`
}

func (t *adminDeleteMCPServerTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args deleteMCPServerArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.ServerID == "" {
		return "[ERROR] server_id is required", nil
	}

	// Capture the name BEFORE deleting the row so we can drop the matching
	// live client afterwards (Delete is keyed by ID, DisconnectServer by name).
	// A GetByID miss is non-fatal — proceed with the delete and skip the sync.
	var serverName string
	if existing, err := t.repo.GetByID(ctx, args.ServerID); err != nil {
		slog.WarnContext(ctx, "[AdminDeleteMCPServer] pre-delete lookup failed, skipping client sync",
			"id", args.ServerID, "error", err)
	} else {
		serverName = existing.Name
	}

	if err := t.repo.Delete(ctx, args.ServerID); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Sprintf("MCP server not found: %s", args.ServerID), nil
		}
		return fmt.Sprintf("[ERROR] Failed to delete MCP server: %v", err), nil
	}

	if t.reloader != nil {
		t.reloader(ctx)
	}
	// Drop the deleted server's live client so its tools stop resolving.
	// Fail-soft — DB is source of truth.
	if t.syncer != nil && serverName != "" {
		if err := t.syncer.DisconnectServer(ctx, serverName); err != nil {
			slog.WarnContext(ctx, "[AdminDeleteMCPServer] client sync failed", "name", serverName, "error", err)
		}
	}

	slog.InfoContext(ctx, "[AdminDeleteMCPServer] deleted", "id", args.ServerID)
	return fmt.Sprintf("MCP server %s deleted successfully.", args.ServerID), nil
}

// --- admin_set_mcp_server_enabled ---

type adminSetMCPServerEnabledTool struct {
	repo     MCPServerRepository
	reloader func(context.Context)
	syncer   MCPClientSyncer
}

// NewAdminSetMCPServerEnabledTool exposes a name-addressed toggle for the
// mcp_servers.enabled column. The builder-assistant prefers names over UUIDs,
// so we resolve via List — tenant scope is enforced by the repo layer.
func NewAdminSetMCPServerEnabledTool(repo MCPServerRepository, reloader func(context.Context), syncer MCPClientSyncer) tool.InvokableTool {
	return &adminSetMCPServerEnabledTool{repo: repo, reloader: reloader, syncer: syncer}
}

func (t *adminSetMCPServerEnabledTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_set_mcp_server_enabled",
		Desc: "Toggles an MCP server on/off by name. Disabled servers stay configured but are not injected into any agent at runtime.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"server_name": {Type: schema.String, Desc: "MCP server name", Required: true},
			"enabled":     {Type: schema.Boolean, Desc: "Desired enabled state", Required: true},
		}),
	}, nil
}

type setMCPServerEnabledArgs struct {
	ServerName string `json:"server_name"`
	Enabled    *bool  `json:"enabled"`
}

func (t *adminSetMCPServerEnabledTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args setMCPServerEnabledArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.ServerName == "" {
		return "[ERROR] server_name is required", nil
	}
	if args.Enabled == nil {
		return "[ERROR] enabled is required (pass true or false)", nil
	}

	servers, err := t.repo.List(ctx)
	if err != nil {
		return fmt.Sprintf("[ERROR] Failed to list MCP servers: %v", err), nil
	}

	var target *MCPServerRecord
	for i := range servers {
		if servers[i].Name == args.ServerName {
			target = &servers[i]
			break
		}
	}
	if target == nil {
		return fmt.Sprintf("MCP server not found: %s", args.ServerName), nil
	}

	// Preserve every other field — the repo Update is a full-row replace.
	target.Enabled = *args.Enabled
	if err := t.repo.Update(ctx, target.ID, target); err != nil {
		return fmt.Sprintf("[ERROR] Failed to update MCP server: %v", err), nil
	}

	if t.reloader != nil {
		t.reloader(ctx)
	}
	// Redial so the live registry tracks the row. The enabled flag governs
	// tool injection at the agent-config layer, not client dialing, so a
	// disabled server stays dialled (active-disconnect on disable is deferred).
	// Fail-soft — DB is source of truth.
	if t.syncer != nil {
		if err := t.syncer.ReconnectServer(ctx, args.ServerName); err != nil {
			slog.WarnContext(ctx, "[AdminSetMCPServerEnabled] client sync failed", "name", args.ServerName, "error", err)
		}
	}

	slog.InfoContext(ctx, "[AdminSetMCPServerEnabled] updated", "name", args.ServerName, "enabled", *args.Enabled)
	return fmt.Sprintf("MCP server %q enabled=%v.", args.ServerName, *args.Enabled), nil
}
