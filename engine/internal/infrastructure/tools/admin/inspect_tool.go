package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools"
)

// --- admin_list_sessions ---

type adminListSessionsTool struct {
	repo SessionRepository
}

func NewAdminListSessionsTool(repo SessionRepository) tool.InvokableTool {
	return &adminListSessionsTool{repo: repo}
}

func (t *adminListSessionsTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name:        "admin_list_sessions",
		Desc:        "Lists recent sessions. Sessions represent conversations between users and agents.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{}),
	}, nil
}

func (t *adminListSessionsTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	if t.repo == nil {
		return "Session inspection is not available.", nil
	}

	sessions, err := t.repo.List(ctx)
	if err != nil {
		return fmt.Sprintf("[ERROR] Failed to list sessions: %s", tools.SanitizeDBError(err)), nil
	}

	if len(sessions) == 0 {
		return "No sessions found.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## %d sessions\n\n", len(sessions)))
	for _, s := range sessions {
		sb.WriteString(fmt.Sprintf("- id=%s user=%s status=%s started=%s\n",
			s.ID, coalesce(s.UserID, "anonymous"), s.Status, s.StartedAt))
	}
	return sb.String(), nil
}

// --- admin_get_session ---

type adminGetSessionTool struct {
	repo SessionRepository
}

func NewAdminGetSessionTool(repo SessionRepository) tool.InvokableTool {
	return &adminGetSessionTool{repo: repo}
}

func (t *adminGetSessionTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_get_session",
		Desc: "Returns a single conversation session by ID, including its metadata and message stats. Use it to inspect or debug an end-user conversation found via admin_list_sessions.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"session_id": {Type: schema.String, Desc: "Session ID", Required: true},
		}),
	}, nil
}

type getSessionArgs struct {
	SessionID string `json:"session_id"`
}

func (t *adminGetSessionTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	if t.repo == nil {
		return "Session inspection is not available.", nil
	}

	var args getSessionArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.SessionID == "" {
		return "[ERROR] session_id is required", nil
	}

	session, err := t.repo.GetByID(ctx, args.SessionID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Sprintf("[ERROR] Session not found: %s", args.SessionID), nil
		}
		return fmt.Sprintf("[ERROR] Failed to get session: %s", tools.SanitizeDBError(err)), nil
	}

	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Sprintf("[ERROR] failed to serialize result: %v", err), nil
	}
	return string(data), nil
}
