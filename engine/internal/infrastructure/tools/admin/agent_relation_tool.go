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

// V2 has a single implicit DELEGATION relationship type — orchestrator
// delegates to target agent via a tool call (see
// docs/architecture/agent-first-runtime.md §3.1). The admin tools below
// expose CRUD over `agent_relations` rows; there is no per-row type field.

// --- admin_list_agent_relations ---

type adminListAgentRelationsTool struct {
	repo AgentRelationRepository
}

func NewAdminListAgentRelationsTool(repo AgentRelationRepository) tool.InvokableTool {
	return &adminListAgentRelationsTool{repo: repo}
}

func (t *adminListAgentRelationsTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_list_agent_relations",
		Desc: "Lists all agent relations in a schema. Each relation expresses delegation: source agent may delegate to target agent.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"schema_id": {Type: schema.String, Desc: "Schema ID", Required: true},
		}),
	}, nil
}

type listAgentRelationsArgs struct {
	SchemaID string `json:"schema_id"`
}

func (t *adminListAgentRelationsTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args listAgentRelationsArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.SchemaID == "" {
		return "[ERROR] schema_id is required", nil
	}

	rels, err := t.repo.List(ctx, args.SchemaID)
	if err != nil {
		return fmt.Sprintf("[ERROR] Failed to list agent relations: %s", tools.SanitizeDBError(err)), nil
	}

	if len(rels) == 0 {
		return fmt.Sprintf("No agent relations in schema %s.", args.SchemaID), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## %d agent relations in schema %s\n\n", len(rels), args.SchemaID))
	for _, r := range rels {
		label := ""
		if r.Label != "" {
			label = fmt.Sprintf(" [%s]", r.Label)
		}
		sb.WriteString(fmt.Sprintf("- id=%s: %s -> %s%s\n", r.ID, r.FromAgent, r.ToAgent, label))
	}
	return sb.String(), nil
}

// --- admin_create_agent_relation ---

type adminCreateAgentRelationTool struct {
	repo     AgentRelationRepository
	reloader func(context.Context)
}

func NewAdminCreateAgentRelationTool(repo AgentRelationRepository, reloader func(context.Context)) tool.InvokableTool {
	return &adminCreateAgentRelationTool{repo: repo, reloader: reloader}
}

func (t *adminCreateAgentRelationTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_create_agent_relation",
		Desc: "Creates a delegation relation between two agents in a schema. The source agent may delegate to the target via a tool call.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"schema_id":  {Type: schema.String, Desc: "Schema ID", Required: true},
			"from_agent": {Type: schema.String, Desc: "Source agent name", Required: true},
			"to_agent":   {Type: schema.String, Desc: "Target agent name", Required: true},
			"label":      {Type: schema.String, Desc: "Optional label for the relation", Required: false},
		}),
	}, nil
}

type createAgentRelationArgs struct {
	SchemaID  string `json:"schema_id"`
	FromAgent string `json:"from_agent"`
	ToAgent   string `json:"to_agent"`
	Label     string `json:"label"`
}

func (t *adminCreateAgentRelationTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args createAgentRelationArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.SchemaID == "" {
		return "[ERROR] schema_id is required", nil
	}
	if args.FromAgent == "" {
		return "[ERROR] from_agent is required", nil
	}
	if args.ToAgent == "" {
		return "[ERROR] to_agent is required", nil
	}

	record := &AgentRelationRecord{
		SchemaID:  args.SchemaID,
		FromAgent: args.FromAgent,
		ToAgent:   args.ToAgent,
		Label:     args.Label,
	}

	if err := t.repo.Create(ctx, record); err != nil {
		return fmt.Sprintf("[ERROR] Failed to create agent relation: %s", tools.SanitizeDBError(err)), nil
	}

	if t.reloader != nil {
		t.reloader(ctx)
	}

	slog.InfoContext(ctx, "[AdminCreateAgentRelation] created", "schema_id", args.SchemaID, "from", args.FromAgent, "to", args.ToAgent)
	return fmt.Sprintf("Agent relation created (id=%s): %s -> %s in schema %s.", record.ID, args.FromAgent, args.ToAgent, args.SchemaID), nil
}

// --- admin_delete_agent_relation ---

type adminDeleteAgentRelationTool struct {
	repo     AgentRelationRepository
	reloader func(context.Context)
}

func NewAdminDeleteAgentRelationTool(repo AgentRelationRepository, reloader func(context.Context)) tool.InvokableTool {
	return &adminDeleteAgentRelationTool{repo: repo, reloader: reloader}
}

func (t *adminDeleteAgentRelationTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_delete_agent_relation",
		Desc: "Deletes a delegation relation between two agents by relation ID. Find the ID with admin_list_agent_relations.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"relation_id": {Type: schema.String, Desc: "Agent relation ID to delete", Required: true},
		}),
	}, nil
}

type deleteAgentRelationArgs struct {
	RelationID string `json:"relation_id"`
}

func (t *adminDeleteAgentRelationTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args deleteAgentRelationArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.RelationID == "" {
		return "[ERROR] relation_id is required", nil
	}

	if err := t.repo.Delete(ctx, args.RelationID); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Sprintf("[ERROR] Agent relation not found: %s", args.RelationID), nil
		}
		return fmt.Sprintf("[ERROR] Failed to delete agent relation: %s", tools.SanitizeDBError(err)), nil
	}

	if t.reloader != nil {
		t.reloader(ctx)
	}

	slog.InfoContext(ctx, "[AdminDeleteAgentRelation] deleted", "relation_id", args.RelationID)
	return fmt.Sprintf("Agent relation %s deleted successfully.", args.RelationID), nil
}
