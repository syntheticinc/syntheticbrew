package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// GenericAgentSpawner is a consumer-side interface for spawn/wait/stop operations.
// Used by the generic SpawnTool (as opposed to AgentPoolForTool used by the legacy spawn_agent).
type GenericAgentSpawner interface {
	SpawnAgent(ctx context.Context, params SpawnParams) (string, error)
	WaitForAgent(ctx context.Context, sessionID, agentID string) (AgentCompletionInfo, error)
	WaitForAllSessionAgents(ctx context.Context, sessionID string) (WaitResult, error)
	HasBlockingWait(sessionID string) bool
	NotifyUserMessage(sessionID, message string)
	StopAgent(agentID string) error
}

// GenericAgentInspector is a consumer-side interface for agent status/list queries.
type GenericAgentInspector interface {
	GetStatusInfo(agentID string) (*AgentInfo, bool)
	GetAllAgentInfos() []AgentInfo
}

// SpawnParams describes parameters for spawning an agent.
type SpawnParams struct {
	SessionID   string
	AgentName   string
	Description string
	TaskID      string
	Blocking    bool
}

// NewSpawnTool creates a generic spawn tool for a specific target agent.
// Tool name will be "spawn_{targetAgentName}".
func NewSpawnTool(targetAgentName string, sessionID string, spawner GenericAgentSpawner, inspector GenericAgentInspector) tool.InvokableTool {
	return &spawnTool{
		targetAgent: targetAgentName,
		sessionID:   sessionID,
		spawner:     spawner,
		inspector:   inspector,
	}
}

type spawnTool struct {
	targetAgent string
	sessionID   string
	spawner     GenericAgentSpawner
	inspector   GenericAgentInspector
}

type spawnToolArgs struct {
	Action      string `json:"action"`
	Description string `json:"description"`
	AgentID     string `json:"agent_id"`
}

func (t *spawnTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "spawn_" + t.targetAgent,
		Desc: fmt.Sprintf("Spawn agent '%s' to handle a subtask. Returns agent summary when done.", t.targetAgent),
		ParamsOneOf: schema.NewParamsOneOfByParams(
			map[string]*schema.ParameterInfo{
				"action": {
					Type:     schema.String,
					Desc:     "Action: spawn (create agent), wait (wait for all agents), status (check agent), list (all agents), stop (terminate agent)",
					Required: true,
					Enum:     []string{"spawn", "wait", "status", "list", "stop"},
				},
				"description": {
					Type: "string",
					Desc: "Task description for the spawned agent (required for 'spawn' action)",
				},
				"agent_id": {
					Type: "string",
					Desc: "Agent ID (for 'status' and 'stop' actions)",
				},
			},
		),
	}, nil
}

func (t *spawnTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var args spawnToolArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		// Application-level error: LLM produced malformed JSON. Surface
		// via [ERROR] convention so the agent loop feeds it back to the
		// model instead of treating it as a platform failure.
		return fmt.Sprintf("[ERROR] parse args: %s", err.Error()), nil
	}

	switch args.Action {
	case "spawn":
		return t.handleSpawn(ctx, args)
	case "wait":
		return t.handleWait(ctx)
	case "status":
		return t.handleStatus(args)
	case "list":
		return t.handleList()
	case "stop":
		return t.handleStop(args)
	default:
		// Application-level: LLM selected an unknown action.
		return fmt.Sprintf("[ERROR] unknown action %q", args.Action), nil
	}
}

func (t *spawnTool) handleSpawn(ctx context.Context, args spawnToolArgs) (string, error) {
	if args.Description == "" {
		// Application-level: LLM forgot a required parameter.
		return "[ERROR] description required for spawn action", nil
	}

	agentID, err := t.spawner.SpawnAgent(ctx, SpawnParams{
		SessionID:   t.sessionID,
		AgentName:   t.targetAgent,
		Description: args.Description,
		Blocking:    true,
	})
	if err != nil {
		return "", fmt.Errorf("spawn agent: %w", err)
	}

	// Block until child agent completes, then return its result to parent LLM
	info, err := t.spawner.WaitForAgent(ctx, t.sessionID, agentID)
	if err != nil {
		return fmt.Sprintf("Agent '%s' spawned but wait failed: %v", t.targetAgent, err), nil
	}

	if info.Status == "failed" || info.Error != "" {
		return fmt.Sprintf("Agent '%s' failed: %s", t.targetAgent, info.Error), nil
	}

	if info.Result != "" {
		return fmt.Sprintf("Agent '%s' completed:\n%s", t.targetAgent, info.Result), nil
	}

	return fmt.Sprintf("Agent '%s' completed (no output)", t.targetAgent), nil
}

func (t *spawnTool) handleWait(ctx context.Context) (string, error) {
	result, err := t.spawner.WaitForAllSessionAgents(ctx, t.sessionID)
	if err != nil {
		return "", fmt.Errorf("wait for agents: %w", err)
	}

	// Build summaries from Results (Summaries field is never populated by the adapter)
	summaries := make([]AgentSummary, 0, len(result.Results))
	for _, info := range result.Results {
		summaries = append(summaries, AgentSummary{
			AgentID:   info.AgentID,
			AgentName: info.AgentID,
			Summary:   info.Result,
			Status:    info.Status,
		})
	}

	data, err := json.Marshal(summaries)
	if err != nil {
		return "", fmt.Errorf("marshal wait result: %w", err)
	}

	return string(data), nil
}

func (t *spawnTool) handleStatus(args spawnToolArgs) (string, error) {
	if args.AgentID == "" {
		// Application-level: LLM forgot a required parameter.
		return "[ERROR] agent_id required for status action", nil
	}

	info, ok := t.inspector.GetStatusInfo(args.AgentID)
	if !ok {
		return fmt.Sprintf("Agent %s not found", args.AgentID), nil
	}

	data, err := json.Marshal(info)
	if err != nil {
		return "", fmt.Errorf("marshal agent info: %w", err)
	}

	return string(data), nil
}

func (t *spawnTool) handleList() (string, error) {
	infos := t.inspector.GetAllAgentInfos()

	data, err := json.Marshal(infos)
	if err != nil {
		return "", fmt.Errorf("marshal agent infos: %w", err)
	}

	return string(data), nil
}

func (t *spawnTool) handleStop(args spawnToolArgs) (string, error) {
	if args.AgentID == "" {
		// Application-level: LLM forgot a required parameter.
		return "[ERROR] agent_id required for stop action", nil
	}

	if err := t.spawner.StopAgent(args.AgentID); err != nil {
		return "", fmt.Errorf("stop agent: %w", err)
	}

	return fmt.Sprintf("Agent %s stopped", args.AgentID), nil
}

// NewGenericSpawnTool creates the legacy Tier-1 spawn_agent tool.
// Unlike per-target spawn_<name> tools, it accepts agent_name as a parameter
// so the LLM can select the target dynamically.
func NewGenericSpawnTool(sessionID string, spawner GenericAgentSpawner, inspector GenericAgentInspector, taskManager EngineTaskManager) tool.InvokableTool {
	return &genericSpawnTool{sessionID: sessionID, spawner: spawner, inspector: inspector, taskManager: taskManager}
}

type genericSpawnTool struct {
	sessionID   string
	spawner     GenericAgentSpawner
	inspector   GenericAgentInspector
	taskManager EngineTaskManager // nil when not wired; task rows silently skipped
}

type genericSpawnArgs struct {
	AgentName string `json:"agent_name"`
	Input     string `json:"input"`
}

func (t *genericSpawnTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "spawn_agent",
		Desc: "Spawn a sub-agent by name to handle a subtask. Blocks until the agent completes and returns its result.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"agent_name": {Type: schema.String, Desc: "Name of the agent to spawn", Required: true},
			"input":      {Type: schema.String, Desc: "Task description or input for the spawned agent", Required: true},
		}),
	}, nil
}

func (t *genericSpawnTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var args genericSpawnArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	if args.AgentName == "" {
		return "[ERROR] agent_name is required", nil
	}

	if t.taskManager == nil {
		slog.WarnContext(ctx, "spawn_agent: task manager not wired, skipping task persistence",
			"session", t.sessionID, "target_agent", args.AgentName)
	} else {
		rootID, err := t.taskManager.CreateTask(ctx, CreateEngineTaskParams{
			Title:     "Agent orchestration",
			SessionID: t.sessionID,
		})
		if err != nil {
			slog.ErrorContext(ctx, "spawn_agent: failed to create root task",
				"session", t.sessionID, "target_agent", args.AgentName, "error", err)
		} else {
			if _, err := t.taskManager.CreateSubTask(ctx, rootID, CreateEngineTaskParams{
				Title:       fmt.Sprintf("Spawn: %s", args.AgentName),
				Description: args.Input,
				SessionID:   t.sessionID,
			}); err != nil {
				slog.ErrorContext(ctx, "spawn_agent: failed to create child task",
					"session", t.sessionID, "root_task_id", rootID, "target_agent", args.AgentName, "error", err)
			}
		}
	}

	agentID, err := t.spawner.SpawnAgent(ctx, SpawnParams{
		SessionID:   t.sessionID,
		AgentName:   args.AgentName,
		Description: args.Input,
		Blocking:    true,
	})
	if err != nil {
		return fmt.Sprintf("[ERROR] spawn agent %q: %v", args.AgentName, err), nil
	}
	info, err := t.spawner.WaitForAgent(ctx, t.sessionID, agentID)
	if err != nil {
		return fmt.Sprintf("Agent %q spawned but wait failed: %v", args.AgentName, err), nil
	}
	if info.Status == "failed" || info.Error != "" {
		return fmt.Sprintf("Agent %q failed: %s", args.AgentName, info.Error), nil
	}
	if info.Result != "" {
		return fmt.Sprintf("Agent %q completed:\n%s", args.AgentName, info.Result), nil
	}
	return fmt.Sprintf("Agent %q completed (no output)", args.AgentName), nil
}

// AgentSummary holds completion summary for an agent (used in WaitResult.Summaries).
type AgentSummary struct {
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name"`
	Summary   string `json:"summary"`
	Status    string `json:"status"`
}
