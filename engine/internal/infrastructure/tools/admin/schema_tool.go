package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// --- admin_list_schemas ---

type adminListSchemasTool struct {
	repo SchemaRepository
}

func NewAdminListSchemasTool(repo SchemaRepository) tool.InvokableTool {
	return &adminListSchemasTool{repo: repo}
}

func (t *adminListSchemasTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name:        "admin_list_schemas",
		Desc:        "Lists all schemas. A schema groups agents into a workflow with edges and triggers.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{}),
	}, nil
}

func (t *adminListSchemasTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	schemas, err := t.repo.List(ctx)
	if err != nil {
		return fmt.Sprintf("[ERROR] Failed to list schemas: %v", err), nil
	}

	if len(schemas) == 0 {
		return "No schemas configured.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## %d schemas\n\n", len(schemas)))
	for _, s := range schemas {
		agents := "none"
		if len(s.AgentNames) > 0 {
			agents = strings.Join(s.AgentNames, ", ")
		}
		sb.WriteString(fmt.Sprintf("- **%s** (id=%s, agents=[%s]) — %s\n", s.Name, s.ID, agents, s.Description))
	}
	return sb.String(), nil
}

// --- admin_get_schema ---

type adminGetSchemaTool struct {
	repo SchemaRepository
}

func NewAdminGetSchemaTool(repo SchemaRepository) tool.InvokableTool {
	return &adminGetSchemaTool{repo: repo}
}

func (t *adminGetSchemaTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_get_schema",
		Desc: "Returns full details of a schema by ID, including assigned agents.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"schema_id": {Type: schema.String, Desc: "Schema ID", Required: true},
		}),
	}, nil
}

type getSchemaArgs struct {
	SchemaID string `json:"schema_id"`
}

func (t *adminGetSchemaTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args getSchemaArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.SchemaID == "" {
		return "[ERROR] schema_id is required", nil
	}

	s, err := t.repo.GetByID(ctx, args.SchemaID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Sprintf("Schema not found: %s", args.SchemaID), nil
		}
		return fmt.Sprintf("[ERROR] Failed to get schema: %v", err), nil
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Sprintf("[ERROR] failed to serialize result: %v", err), nil
	}
	return string(data), nil
}

// --- admin_create_schema ---

type adminCreateSchemaTool struct {
	creator  SchemaCreator
	reloader func(context.Context)
}

func NewAdminCreateSchemaTool(creator SchemaCreator, reloader func(context.Context)) tool.InvokableTool {
	return &adminCreateSchemaTool{creator: creator, reloader: reloader}
}

func (t *adminCreateSchemaTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_create_schema",
		Desc: "Creates a new schema (workflow). Requires name. Optional: description.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"name":        {Type: schema.String, Desc: "Schema name", Required: true},
			"description": {Type: schema.String, Desc: "Schema description", Required: false},
		}),
	}, nil
}

type createSchemaArgs struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (t *adminCreateSchemaTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args createSchemaArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.Name == "" {
		return "[ERROR] name is required", nil
	}

	record, err := t.creator.CreateSchema(ctx, args.Name, args.Description)
	if err != nil {
		return renderSchemaCreateErr(args.Name, err), nil
	}

	if t.reloader != nil {
		t.reloader(ctx)
	}

	slog.InfoContext(ctx, "[AdminCreateSchema] created schema", "name", args.Name, "id", record.ID)
	return fmt.Sprintf("Schema %q created (id=%s).", args.Name, record.ID), nil
}

// renderSchemaCreateErr turns a guarded-creation failure into the LLM-facing
// tool result. The quota case carries a stable machine-readable sentinel so
// programmatic MCP clients can distinguish it from a generic failure.
func renderSchemaCreateErr(name string, err error) string {
	var domainErr *pkgerrors.DomainError
	if errors.As(err, &domainErr) {
		switch domainErr.Code {
		case pkgerrors.CodeUsageLimited:
			return "[quota:schema_limit_reached] Your plan's schema limit is reached. Upgrade the plan or remove an existing schema, then retry."
		case pkgerrors.CodeAlreadyExists:
			return fmt.Sprintf("Schema with name %q already exists.", name)
		}
	}
	return fmt.Sprintf("[ERROR] Failed to create schema: %v", err)
}

// --- admin_update_schema ---

type adminUpdateSchemaTool struct {
	repo      SchemaRepository
	agentRepo AgentRepository
	reloader  func(context.Context)
}

// NewAdminUpdateSchemaTool wires the update-schema tool. agentRepo is used to
// resolve the `entry_agent_id` parameter when it comes in as an agent name
// (the LLM finds name resolution more natural than UUIDs). Passing nil is
// allowed — the tool then requires entry_agent_id to be a UUID.
func NewAdminUpdateSchemaTool(repo SchemaRepository, agentRepo AgentRepository, reloader func(context.Context)) tool.InvokableTool {
	return &adminUpdateSchemaTool{repo: repo, agentRepo: agentRepo, reloader: reloader}
}

func (t *adminUpdateSchemaTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_update_schema",
		Desc: "Updates an existing schema by ID. Set chat_enabled=true to let end users chat with this schema; set entry_agent_id (agent name or UUID) to point chat at the delegator root.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"schema_id":      {Type: schema.String, Desc: "Schema ID to update", Required: true},
			"name":           {Type: schema.String, Desc: "New name", Required: false},
			"description":    {Type: schema.String, Desc: "New description", Required: false},
			"entry_agent_id": {Type: schema.String, Desc: "Entry agent: accepts either the agent name or its UUID. Pass empty string to clear.", Required: false},
			"chat_enabled":   {Type: schema.Boolean, Desc: "When true, end-user chat through this schema is allowed.", Required: false},
		}),
	}, nil
}

type updateSchemaArgs struct {
	SchemaID     string  `json:"schema_id"`
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	EntryAgentID *string `json:"entry_agent_id,omitempty"`
	ChatEnabled  *bool   `json:"chat_enabled,omitempty"`
}

func (t *adminUpdateSchemaTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args updateSchemaArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.SchemaID == "" {
		return "[ERROR] schema_id is required", nil
	}

	record := &SchemaRecord{
		Name:        args.Name,
		Description: args.Description,
		ChatEnabled: args.ChatEnabled,
	}

	if args.EntryAgentID != nil {
		ref := *args.EntryAgentID
		if ref == "" {
			empty := ""
			record.EntryAgentID = &empty
		} else {
			resolved, err := resolveAgentReference(ctx, t.agentRepo, ref)
			if err != nil {
				return fmt.Sprintf("[ERROR] %v", err), nil
			}
			record.EntryAgentID = &resolved
		}
	}

	if err := t.repo.Update(ctx, args.SchemaID, record); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Sprintf("Schema not found: %s", args.SchemaID), nil
		}
		return fmt.Sprintf("[ERROR] Failed to update schema: %v", err), nil
	}

	if t.reloader != nil {
		t.reloader(ctx)
	}

	slog.InfoContext(ctx, "[AdminUpdateSchema] updated schema", "id", args.SchemaID)
	return fmt.Sprintf("Schema %s updated successfully.", args.SchemaID), nil
}

// resolveAgentReference returns a UUID given either a canonical UUID or an
// agent name. A canonical UUID is short-circuited with a cheap shape check.
// Name resolution goes through the supplied AgentRepository — when repo is
// nil, name-shaped refs are rejected with a clear error.
func resolveAgentReference(ctx context.Context, repo AgentRepository, ref string) (string, error) {
	if isLikelyUUID(ref) {
		return ref, nil
	}
	if repo == nil {
		return "", fmt.Errorf("agent reference %q looks like a name but no AgentRepository is wired; pass a UUID instead", ref)
	}
	rec, err := repo.GetByName(ctx, ref)
	if err != nil || rec == nil {
		return "", fmt.Errorf("agent not found: %s", ref)
	}
	if rec.ID == "" {
		return "", fmt.Errorf("agent %q has no resolvable UUID", ref)
	}
	return rec.ID, nil
}

// isLikelyUUID does a cheap length+dash check. Good enough to distinguish
// "researcher" from "7f6b6b62-4be1-4d3f-b1c6-3c54c71b6d9d" at this layer —
// the DB layer still validates the final value.
func isLikelyUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	return s[8] == '-' && s[13] == '-' && s[18] == '-' && s[23] == '-'
}

// --- admin_delete_schema ---

type adminDeleteSchemaTool struct {
	repo     SchemaRepository
	reloader func(context.Context)
}

func NewAdminDeleteSchemaTool(repo SchemaRepository, reloader func(context.Context)) tool.InvokableTool {
	return &adminDeleteSchemaTool{repo: repo, reloader: reloader}
}

func (t *adminDeleteSchemaTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_delete_schema",
		Desc: "Deletes a schema by ID. WARNING: This removes all edges and agent associations in the schema.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"schema_id": {Type: schema.String, Desc: "Schema ID to delete", Required: true},
		}),
	}, nil
}

type deleteSchemaArgs struct {
	SchemaID string `json:"schema_id"`
}

func (t *adminDeleteSchemaTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args deleteSchemaArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.SchemaID == "" {
		return "[ERROR] schema_id is required", nil
	}

	if err := t.repo.Delete(ctx, args.SchemaID); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Sprintf("Schema not found: %s", args.SchemaID), nil
		}
		return fmt.Sprintf("[ERROR] Failed to delete schema: %v", err), nil
	}

	if t.reloader != nil {
		t.reloader(ctx)
	}

	slog.InfoContext(ctx, "[AdminDeleteSchema] deleted schema", "id", args.SchemaID)
	return fmt.Sprintf("Schema %s deleted successfully.", args.SchemaID), nil
}
