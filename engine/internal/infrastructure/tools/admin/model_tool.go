package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// --- admin_list_models ---

type adminListModelsTool struct {
	repo ModelRepository
}

func NewAdminListModelsTool(repo ModelRepository) tool.InvokableTool {
	return &adminListModelsTool{repo: repo}
}

func (t *adminListModelsTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_list_models",
		Desc: "Lists all LLM model configurations. API keys are never shown.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{}),
	}, nil
}

func (t *adminListModelsTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	models, err := t.repo.List(ctx)
	if err != nil {
		return fmt.Sprintf("[ERROR] Failed to list models: %v", err), nil
	}

	if len(models) == 0 {
		return "No models configured.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## %d models\n\n", len(models)))
	for _, m := range models {
		hasKey := "no"
		if m.APIKey != "" {
			hasKey = "yes"
		}
		defaultMark := ""
		if m.IsDefault {
			defaultMark = " [default]"
		}
		sb.WriteString(fmt.Sprintf("- id=%s **%s**%s (type=%s, model=%s, base_url=%s, has_api_key=%s)\n",
			m.ID, m.Name, defaultMark, m.Type, m.ModelName, coalesce(m.BaseURL, "default"), hasKey))
	}
	return sb.String(), nil
}

// --- admin_create_model ---

type adminCreateModelTool struct {
	repo     ModelRepository
	reloader func(context.Context)
}

func NewAdminCreateModelTool(repo ModelRepository, reloader func(context.Context)) tool.InvokableTool {
	return &adminCreateModelTool{repo: repo, reloader: reloader}
}

func (t *adminCreateModelTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_create_model",
		Desc: "Creates an LLM model configuration. Requires name, type, and model_name. Optional: base_url, api_key, is_default (promote to tenant default chat model).",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"name":       {Type: schema.String, Desc: "Unique model config name", Required: true},
			"type":       {Type: schema.String, Desc: "Provider type: openai_compatible, anthropic, etc.", Required: true},
			"model_name": {Type: schema.String, Desc: "Model identifier (e.g. gpt-4, claude-3)", Required: true},
			"base_url":   {Type: schema.String, Desc: "Base URL for the API endpoint", Required: false},
			"api_key":    {Type: schema.String, Desc: "API key (stored at rest in the engine database; never returned in API responses)", Required: false},
			"is_default": {Type: schema.Boolean, Desc: "Mark this model as the tenant default chat model", Required: false},
		}),
	}, nil
}

type createModelArgs struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	ModelName string `json:"model_name"`
	BaseURL   string `json:"base_url"`
	APIKey    string `json:"api_key"`
	IsDefault bool   `json:"is_default"`
}

func (t *adminCreateModelTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args createModelArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.Name == "" {
		return "[ERROR] name is required", nil
	}
	if args.Type == "" {
		return "[ERROR] type is required", nil
	}
	if args.ModelName == "" {
		return "[ERROR] model_name is required", nil
	}

	record := &ModelRecord{
		Name:      args.Name,
		Type:      args.Type,
		ModelName: args.ModelName,
		BaseURL:   args.BaseURL,
		APIKey:    args.APIKey,
		IsDefault: args.IsDefault,
	}

	if err := t.repo.Create(ctx, record); err != nil {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Sprintf("Model with name %q already exists.", args.Name), nil
		}
		return fmt.Sprintf("[ERROR] Failed to create model: %v", err), nil
	}

	if t.reloader != nil {
		t.reloader(ctx)
	}

	slog.InfoContext(ctx, "[AdminCreateModel] created", "name", args.Name, "type", args.Type, "model", args.ModelName, "is_default", args.IsDefault)
	defaultNote := ""
	if args.IsDefault {
		defaultNote = " [default]"
	}
	return fmt.Sprintf("Model %q created (id=%s, type=%s, model=%s)%s.", args.Name, record.ID, args.Type, args.ModelName, defaultNote), nil
}

// --- admin_update_model ---

type adminUpdateModelTool struct {
	repo     ModelRepository
	reloader func(context.Context)
}

func NewAdminUpdateModelTool(repo ModelRepository, reloader func(context.Context)) tool.InvokableTool {
	return &adminUpdateModelTool{repo: repo, reloader: reloader}
}

func (t *adminUpdateModelTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_update_model",
		Desc: "Updates an LLM model configuration by ID. Provide only fields to change. API key is only updated if provided. Setting is_default=true promotes this model to the tenant default (atomic swap).",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"model_id":   {Type: schema.String, Desc: "Model config ID to update", Required: true},
			"name":       {Type: schema.String, Desc: "New name", Required: false},
			"type":       {Type: schema.String, Desc: "New type", Required: false},
			"model_name": {Type: schema.String, Desc: "New model identifier", Required: false},
			"base_url":   {Type: schema.String, Desc: "New base URL", Required: false},
			"api_key":    {Type: schema.String, Desc: "New API key", Required: false},
			"is_default": {Type: schema.Boolean, Desc: "Promote this model to the tenant default chat model (atomic)", Required: false},
		}),
	}, nil
}

type updateModelArgs struct {
	ModelID   string `json:"model_id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	ModelName string `json:"model_name"`
	BaseURL   string `json:"base_url"`
	APIKey    string `json:"api_key"`
	IsDefault bool   `json:"is_default"`
}

func (t *adminUpdateModelTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args updateModelArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.ModelID == "" {
		return "[ERROR] model_id is required", nil
	}

	existing, err := t.repo.GetByID(ctx, args.ModelID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Sprintf("Model not found: %s", args.ModelID), nil
		}
		return fmt.Sprintf("[ERROR] Failed to get model: %v", err), nil
	}

	record := &ModelRecord{
		Name:      coalesce(args.Name, existing.Name),
		Type:      coalesce(args.Type, existing.Type),
		ModelName: coalesce(args.ModelName, existing.ModelName),
		BaseURL:   coalesce(args.BaseURL, existing.BaseURL),
		APIKey:    args.APIKey, // Only update if explicitly provided
		// Don't pass IsDefault through the generic Update path — promotion
		// must go through SetDefault to maintain the atomic-swap invariant.
		IsDefault: existing.IsDefault,
	}

	if err := t.repo.Update(ctx, args.ModelID, record); err != nil {
		return fmt.Sprintf("[ERROR] Failed to update model: %v", err), nil
	}

	if args.IsDefault && !existing.IsDefault {
		if err := t.repo.SetDefault(ctx, args.ModelID); err != nil {
			return fmt.Sprintf("[ERROR] Updated model fields, but promoting to default failed: %v", err), nil
		}
	}

	if t.reloader != nil {
		t.reloader(ctx)
	}

	slog.InfoContext(ctx, "[AdminUpdateModel] updated", "id", args.ModelID, "promoted_default", args.IsDefault && !existing.IsDefault)
	return fmt.Sprintf("Model %s updated successfully.", args.ModelID), nil
}

// --- admin_set_default_model ---

// adminSetDefaultModelTool promotes the given model (by name or ID) to the
// tenant's default chat model. The operation is atomic at the DB level
// (SetDefault runs in a transaction + partial unique index).
type adminSetDefaultModelTool struct {
	repo     ModelRepository
	reloader func(context.Context)
}

// NewAdminSetDefaultModelTool wires the set-default tool.
func NewAdminSetDefaultModelTool(repo ModelRepository, reloader func(context.Context)) tool.InvokableTool {
	return &adminSetDefaultModelTool{repo: repo, reloader: reloader}
}

func (t *adminSetDefaultModelTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_set_default_model",
		Desc: "Promotes a chat model to the tenant default (atomic swap). Provide either name or model_id.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"name":     {Type: schema.String, Desc: "Model name to promote (alternative to model_id)", Required: false},
			"model_id": {Type: schema.String, Desc: "Model UUID to promote (alternative to name)", Required: false},
		}),
	}, nil
}

type setDefaultModelArgs struct {
	Name    string `json:"name"`
	ModelID string `json:"model_id"`
}

func (t *adminSetDefaultModelTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args setDefaultModelArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.Name == "" && args.ModelID == "" {
		return "[ERROR] either name or model_id is required", nil
	}

	id := args.ModelID
	name := args.Name

	if id == "" {
		// Resolve name → ID via List (same pattern as admin_delete_model path).
		models, err := t.repo.List(ctx)
		if err != nil {
			return fmt.Sprintf("[ERROR] Failed to list models: %v", err), nil
		}
		for _, m := range models {
			if m.Name == name {
				id = m.ID
				break
			}
		}
		if id == "" {
			return fmt.Sprintf("Model not found: %s", name), nil
		}
	}

	if err := t.repo.SetDefault(ctx, id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Sprintf("Model not found: %s", coalesce(name, id)), nil
		}
		return fmt.Sprintf("[ERROR] Failed to set default model: %v", err), nil
	}

	if t.reloader != nil {
		t.reloader(ctx)
	}

	if name == "" {
		// Best-effort: resolve name for nicer output.
		if rec, err := t.repo.GetByID(ctx, id); err == nil && rec != nil {
			name = rec.Name
		}
	}
	slog.InfoContext(ctx, "[AdminSetDefaultModel] promoted", "id", id, "name", name)
	if name == "" {
		name = id
	}
	return fmt.Sprintf("Model %q is now the default chat model.", name), nil
}

// --- admin_delete_model ---

type adminDeleteModelTool struct {
	repo     ModelRepository
	reloader func(context.Context)
}

func NewAdminDeleteModelTool(repo ModelRepository, reloader func(context.Context)) tool.InvokableTool {
	return &adminDeleteModelTool{repo: repo, reloader: reloader}
}

func (t *adminDeleteModelTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_delete_model",
		Desc: "Deletes an LLM model configuration by ID. Agents using this model will need reassignment.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"model_id": {Type: schema.String, Desc: "Model config ID to delete", Required: true},
		}),
	}, nil
}

type deleteModelArgs struct {
	ModelID string `json:"model_id"`
}

func (t *adminDeleteModelTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args deleteModelArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.ModelID == "" {
		return "[ERROR] model_id is required", nil
	}

	if err := t.repo.Delete(ctx, args.ModelID); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Sprintf("Model not found: %s", args.ModelID), nil
		}
		return fmt.Sprintf("[ERROR] Failed to delete model: %v", err), nil
	}

	if t.reloader != nil {
		t.reloader(ctx)
	}

	slog.InfoContext(ctx, "[AdminDeleteModel] deleted", "id", args.ModelID)
	return fmt.Sprintf("Model %s deleted successfully.", args.ModelID), nil
}
