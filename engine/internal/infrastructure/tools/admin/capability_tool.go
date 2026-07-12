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

// --- admin_add_capability ---

type adminAddCapabilityTool struct {
	repo     CapabilityRepository
	reloader func(context.Context)
}

func NewAdminAddCapabilityTool(repo CapabilityRepository, reloader func(context.Context)) tool.InvokableTool {
	return &adminAddCapabilityTool{repo: repo, reloader: reloader}
}

func (t *adminAddCapabilityTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_add_capability",
		Desc: "Adds a capability to an agent. Types: memory, knowledge. Both auto-inject tools at runtime (memory_recall/memory_store for memory, knowledge_search for knowledge).",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"agent_name":      {Type: schema.String, Desc: "Agent name", Required: true},
			"capability_type": {Type: schema.String, Desc: "Type: memory or knowledge", Required: true},
			"config_json":     {Type: schema.String, Desc: "Optional JSON config string for the capability", Required: false},
		}),
	}, nil
}

type addCapabilityArgs struct {
	AgentName      string `json:"agent_name"`
	CapabilityType string `json:"capability_type"`
	ConfigJSON     string `json:"config_json"`
}

func (t *adminAddCapabilityTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	if t.repo == nil {
		return "[ERROR] Capability management is not available.", nil
	}

	var args addCapabilityArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.AgentName == "" {
		return "[ERROR] agent_name is required", nil
	}
	if args.CapabilityType == "" {
		return "[ERROR] capability_type is required", nil
	}

	validTypes := map[string]bool{"memory": true, "knowledge": true}
	if !validTypes[args.CapabilityType] {
		return fmt.Sprintf("[ERROR] Invalid capability type %q. Must be one of: memory, knowledge.", args.CapabilityType), nil
	}

	var config map[string]interface{}
	if args.ConfigJSON != "" {
		if err := json.Unmarshal([]byte(args.ConfigJSON), &config); err != nil {
			return fmt.Sprintf("[ERROR] Invalid config_json: %v", err), nil
		}
	}

	record := &CapabilityRecord{
		AgentName: args.AgentName,
		Type:      args.CapabilityType,
		Config:    config,
		Enabled:   true,
	}

	if err := t.repo.Create(ctx, record); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Sprintf("[ERROR] Agent not found: %s", args.AgentName), nil
		}
		return fmt.Sprintf("[ERROR] Failed to add capability: %s", tools.SanitizeDBError(err)), nil
	}

	if t.reloader != nil {
		t.reloader(ctx)
	}

	slog.InfoContext(ctx, "[AdminAddCapability] added", "agent", args.AgentName, "type", args.CapabilityType)
	return fmt.Sprintf("Capability %q added to agent %q (id=%s).", args.CapabilityType, args.AgentName, record.ID), nil
}

// --- admin_remove_capability ---

type adminRemoveCapabilityTool struct {
	repo     CapabilityRepository
	reloader func(context.Context)
}

func NewAdminRemoveCapabilityTool(repo CapabilityRepository, reloader func(context.Context)) tool.InvokableTool {
	return &adminRemoveCapabilityTool{repo: repo, reloader: reloader}
}

func (t *adminRemoveCapabilityTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_remove_capability",
		Desc: "Removes a capability from an agent by capability ID.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"capability_id": {Type: schema.String, Desc: "Capability ID to remove", Required: true},
		}),
	}, nil
}

type removeCapabilityArgs struct {
	CapabilityID string `json:"capability_id"`
}

func (t *adminRemoveCapabilityTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	if t.repo == nil {
		return "[ERROR] Capability management is not available.", nil
	}

	var args removeCapabilityArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.CapabilityID == "" {
		return "[ERROR] capability_id is required", nil
	}

	if err := t.repo.Delete(ctx, args.CapabilityID); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Sprintf("[ERROR] Capability not found: %s", args.CapabilityID), nil
		}
		return fmt.Sprintf("[ERROR] Failed to remove capability: %s", tools.SanitizeDBError(err)), nil
	}

	if t.reloader != nil {
		t.reloader(ctx)
	}

	slog.InfoContext(ctx, "[AdminRemoveCapability] removed", "id", args.CapabilityID)
	return fmt.Sprintf("Capability %s removed successfully.", args.CapabilityID), nil
}

// --- admin_update_capability ---

type adminUpdateCapabilityTool struct {
	repo     CapabilityRepository
	reloader func(context.Context)
}

func NewAdminUpdateCapabilityTool(repo CapabilityRepository, reloader func(context.Context)) tool.InvokableTool {
	return &adminUpdateCapabilityTool{repo: repo, reloader: reloader}
}

func (t *adminUpdateCapabilityTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_update_capability",
		Desc: "Updates a capability's config or enabled state by ID.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"capability_id": {Type: schema.String, Desc: "Capability ID to update", Required: true},
			"config_json":   {Type: schema.String, Desc: "New JSON config string", Required: false},
			"enabled":       {Type: schema.Boolean, Desc: "Enable or disable the capability", Required: false},
		}),
	}, nil
}

type updateCapabilityArgs struct {
	CapabilityID string `json:"capability_id"`
	ConfigJSON   string `json:"config_json"`
	Enabled      *bool  `json:"enabled"`
}

func (t *adminUpdateCapabilityTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	if t.repo == nil {
		return "[ERROR] Capability management is not available.", nil
	}

	var args updateCapabilityArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.CapabilityID == "" {
		return "[ERROR] capability_id is required", nil
	}

	var config map[string]interface{}
	if args.ConfigJSON != "" {
		if err := json.Unmarshal([]byte(args.ConfigJSON), &config); err != nil {
			return fmt.Sprintf("[ERROR] Invalid config_json: %v", err), nil
		}
	}

	enabled := true
	if args.Enabled != nil {
		enabled = *args.Enabled
	}

	record := &CapabilityRecord{
		Config:  config,
		Enabled: enabled,
	}

	if err := t.repo.Update(ctx, args.CapabilityID, record); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Sprintf("[ERROR] Capability not found: %s", args.CapabilityID), nil
		}
		return fmt.Sprintf("[ERROR] Failed to update capability: %s", tools.SanitizeDBError(err)), nil
	}

	if t.reloader != nil {
		t.reloader(ctx)
	}

	slog.InfoContext(ctx, "[AdminUpdateCapability] updated", "id", args.CapabilityID)
	return fmt.Sprintf("Capability %s updated successfully.", args.CapabilityID), nil
}
