package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// AgentSpawner defines operations for Code Agent management (consumer-side)
type AgentSpawner interface {
	Spawn(ctx context.Context, sessionID, projectKey, taskID string) (agentID string, err error)
	GetStatus(agentID string) (agent interface{ GetID() string }, ok bool)
	GetAllAgents() []AgentInfo
	StopAgent(agentID string) error
	RestartAgent(ctx context.Context, agentID string) (newAgentID string, err error)
}

// AgentInfo holds minimal agent info for listing
type AgentInfo struct {
	ID        string
	SubtaskID string
	Status    string
	Result    string
	Error     string
}

// WaitResult describes the result of waiting for session agents
type WaitResult struct {
	AllDone              bool                           // true if all agents completed
	Interrupted          bool                           // true if interrupted by user message
	IsInterruptResponder bool                           // true = this call should return full INTERRUPT
	UserMessage          string                         // user message that caused interrupt
	StillRunning         []string                       // agent IDs still running
	Results              map[string]AgentCompletionInfo // completed agents
	Summaries            []AgentSummary                 // agent summaries (used by generic SpawnTool)
}

// AgentCompletionInfo holds completion info for an agent
type AgentCompletionInfo struct {
	AgentID   string
	SubtaskID string
	Status    string
	Result    string
	Error     string
}

// spawnAgentArgs represents tool arguments
type spawnAgentArgs struct {
	Action          string `json:"action"` // spawn, status, list, stop, restart, wait
	SubtaskID       string `json:"subtask_id,omitempty"`
	AgentID         string `json:"agent_id,omitempty"`
	ProjectKey      string `json:"project_key,omitempty"`
	FlowType        string `json:"flow_type,omitempty"`        // coder (default), researcher, reviewer
	TaskDescription string `json:"task_description,omitempty"` // task description for researcher/reviewer agents
}

// AgentPoolForTool is a simplified interface for the tool to use (consumer-side).
// Implemented by agent.AgentPoolAdapter.
type AgentPoolForTool interface {
	Spawn(ctx context.Context, sessionID, projectKey, subtaskID string, blocking bool) (string, error)
	SpawnWithDescription(ctx context.Context, sessionID, projectKey string, agentType string, description string, blocking bool) (string, error)
	WaitForAllSessionAgents(ctx context.Context, sessionID string) (WaitResult, error)
	HasBlockingWait(sessionID string) bool
	NotifyUserMessage(sessionID, message string)
	GetStatusInfo(sessionID, agentID string) (*AgentInfo, bool)
	GetAllAgentInfos(sessionID string) []AgentInfo
	StopAgent(sessionID, agentID string) error
	RestartAgent(ctx context.Context, agentID string, blocking bool) (string, error)
}

// SpawnAgentTool implements async Code Agent spawning
type SpawnAgentTool struct {
	pool       AgentPoolForTool
	sessionID  string
	projectKey string
}

// NewSpawnAgentTool creates the spawn_agent tool
func NewSpawnAgentTool(pool AgentPoolForTool, sessionID, projectKey string) tool.InvokableTool {
	return &SpawnAgentTool{pool: pool, sessionID: sessionID, projectKey: projectKey}
}

func (t *SpawnAgentTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "spawn_agent",
		Desc: `Spawn and manage Code Agent workers (BLOCKING).

BLOCKING BEHAVIOR (CRITICAL):
- "spawn" now BLOCKS until the agent completes OR user sends a message
- If user interrupts (sends message while waiting): you will receive [INTERRUPT] with the user's message
- After handling the user's message, call spawn_agent(action=wait) to resume waiting for agents
- Parallel spawns: if you spawn multiple agents in parallel, only ONE will get [INTERRUPT], others get [PAUSED]

Actions:
- "spawn": Start an agent (coder requires subtask_id; researcher/reviewer require task_description and flow_type). BLOCKS until completion or user interrupt.
- "wait": Resume waiting after handling user interrupt. BLOCKS until all agents complete or next interrupt.
- "status": Check agent status (requires agent_id). Returns: running/completed/failed/stopped.
- "list": List all agents and their statuses.
- "stop": Stop a running agent (requires agent_id).
- "restart": Restart a failed/stopped agent (requires agent_id). BLOCKS until completion or interrupt.

Typical workflow:
1. spawn(subtask_id) → BLOCKS → either [SUCCESS] or [INTERRUPT]
2. If [INTERRUPT]: handle user message, then call wait() to resume
3. wait() → BLOCKS → either [SUCCESS] (all done) or [INTERRUPT] (another user message)

Code Agents execute subtasks autonomously. They have access to: read_file, write_file, edit_file, execute_command, search_code, smart_search, get_project_tree.`,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"action":           {Type: schema.String, Desc: "Action: spawn, wait, status, list, stop, restart", Required: true},
			"subtask_id":       {Type: schema.String, Desc: "Subtask ID to assign to agent (for spawn with coder)"},
			"agent_id":         {Type: schema.String, Desc: "Agent ID (for status, stop, restart)"},
			"flow_type":        {Type: schema.String, Desc: "Agent type: coder (default), researcher, reviewer"},
			"task_description": {Type: schema.String, Desc: "Task description for researcher/reviewer agents (used instead of subtask_id)"},
		}),
	}, nil
}

func (t *SpawnAgentTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var args spawnAgentArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid JSON: %v", err), nil
	}

	slog.InfoContext(ctx, "[spawn_agent] invoked", "action", args.Action)

	if args.Action == "" {
		return `[ERROR] "action" field is empty. You MUST specify an action.
Valid actions: spawn, wait, status, list, stop, restart.
Typical workflow: manage_tasks(action=create_subtask, parent_task_id=<parent>) → spawn_agent(action=spawn, subtask_id=<ID from create_subtask response>).
Example: {"action": "spawn", "subtask_id": "abc123"}`, nil
	}

	switch args.Action {
	case "spawn":
		agentType := "coder"
		if args.FlowType != "" {
			agentType = args.FlowType
		}

		// Validate agent type
		validTypes := map[string]bool{
			"coder":      true,
			"researcher": true,
			"reviewer":   true,
		}
		if !validTypes[agentType] {
			return fmt.Sprintf("[ERROR] invalid flow_type: %s. Must be: coder, researcher, reviewer", args.FlowType), nil
		}

		var agentID string
		var err error

		if agentType == "coder" {
			if args.SubtaskID == "" {
				return "[ERROR] subtask_id is required for coder agent", nil
			}
			agentID, err = t.pool.Spawn(ctx, t.sessionID, t.projectKey, args.SubtaskID, true)
		} else {
			if args.TaskDescription == "" {
				return fmt.Sprintf("[ERROR] task_description is required for %s agent", args.FlowType), nil
			}
			agentID, err = t.pool.SpawnWithDescription(ctx, t.sessionID, t.projectKey, agentType, args.TaskDescription, true)
		}

		if err != nil {
			if strings.Contains(err.Error(), "max concurrent agents reached") {
				return fmt.Sprintf("Cannot spawn new agent: %s. Use spawn_agent(action=list) to check running agents, or spawn_agent(action=stop, agent_id=...) to stop one.", err.Error()), nil
			}
			if agentType == "coder" {
				return fmt.Sprintf("[ERROR] %v. "+
					"subtask_id MUST be the exact ID returned by manage_tasks(action=create_subtask, parent_task_id=...), e.g. \"a1b2c3d4\". "+
					"Do NOT invent IDs. Use manage_tasks(action=get_ready, parent_task_id=...) to list available subtask IDs.", err), nil
			}
			return fmt.Sprintf("[ERROR] %v", err), nil
		}

		// Blocking: wait for all agents
		result, err := t.pool.WaitForAllSessionAgents(ctx, t.sessionID)
		if err != nil {
			return fmt.Sprintf("[ERROR] wait failed: %v", err), nil
		}

		if result.Interrupted {
			if result.IsInterruptResponder {
				return fmt.Sprintf("[INTERRUPT] User message received while agents work.\n"+
					"User said: %s\n"+
					"Agents still running: %s\n\n"+
					"Handle the user's message, then call spawn_agent(action=wait) to resume waiting.",
					result.UserMessage, strings.Join(result.StillRunning, ", ")), nil
			}
			// Not the responder — another parallel spawn handles the interrupt
			return "[PAUSED] Interrupt handled by parallel spawn call. " +
				"See the [INTERRUPT] result in the other spawn response.", nil
		}

		// All done — format THIS agent's result
		if agentResult, ok := result.Results[agentID]; ok {
			return formatAgentCompletion(agentID, agentResult), nil
		}
		return fmt.Sprintf("Agent %s completed (no result in map).", agentID), nil

	case "wait":
		// Resume waiting after interrupt
		result, err := t.pool.WaitForAllSessionAgents(ctx, t.sessionID)
		if err != nil {
			return fmt.Sprintf("[ERROR] wait failed: %v", err), nil
		}

		if result.Interrupted {
			if result.IsInterruptResponder {
				return fmt.Sprintf("[INTERRUPT] User message received while agents work.\n"+
					"User said: %s\n"+
					"Agents still running: %s\n\n"+
					"Handle the user's message, then call spawn_agent(action=wait) to resume waiting.",
					result.UserMessage, strings.Join(result.StillRunning, ", ")), nil
			}
			return "[PAUSED] Interrupt handled by parallel spawn call.", nil
		}

		// All done — format all results
		if len(result.Results) == 0 {
			return "All agents completed (no results).", nil
		}
		var parts []string
		for _, info := range result.Results {
			parts = append(parts, formatAgentCompletion(info.AgentID, info))
		}
		return strings.Join(parts, "\n---\n"), nil

	case "status":
		if args.AgentID == "" {
			return "[ERROR] agent_id is required for status", nil
		}
		info, ok := t.pool.GetStatusInfo(t.sessionID, args.AgentID)
		if !ok {
			return fmt.Sprintf("[STALE] Agent %s no longer exists (server was restarted). "+
				"If the subtask is still in_progress, fail it and create a new subtask with spawn action.", args.AgentID), nil
		}
		result := fmt.Sprintf("Agent: %s\nSubtask: %s\nStatus: %s", info.ID, info.SubtaskID, info.Status)
		if info.Result != "" {
			result += fmt.Sprintf("\nResult: %s", info.Result)
		}
		if info.Error != "" {
			result += fmt.Sprintf("\nError: %s", info.Error)
		}
		return result, nil

	case "list":
		agents := t.pool.GetAllAgentInfos(t.sessionID)
		if len(agents) == 0 {
			return "No agents running.", nil
		}
		result := fmt.Sprintf("Agents (%d):\n", len(agents))
		for _, a := range agents {
			result += fmt.Sprintf("  [%s] subtask=%s status=%s\n", a.ID, a.SubtaskID, a.Status)
		}
		return result, nil

	case "stop":
		if args.AgentID == "" {
			return "[ERROR] agent_id is required for stop", nil
		}
		if err := t.pool.StopAgent(t.sessionID, args.AgentID); err != nil {
			return fmt.Sprintf("[ERROR] %v", err), nil
		}
		return fmt.Sprintf("Agent %s stopped.", args.AgentID), nil

	case "restart":
		if args.AgentID == "" {
			return "[ERROR] agent_id is required for restart", nil
		}
		newID, err := t.pool.RestartAgent(ctx, args.AgentID, true)
		if err != nil {
			return fmt.Sprintf("[ERROR] %v", err), nil
		}

		// Blocking: wait for all agents
		result, err := t.pool.WaitForAllSessionAgents(ctx, t.sessionID)
		if err != nil {
			return fmt.Sprintf("[ERROR] wait failed: %v", err), nil
		}

		if result.Interrupted {
			if result.IsInterruptResponder {
				return fmt.Sprintf("[INTERRUPT] User message received while agents work.\n"+
					"User said: %s\n"+
					"Agents still running: %s\n\n"+
					"Handle the user's message, then call spawn_agent(action=wait) to resume waiting.",
					result.UserMessage, strings.Join(result.StillRunning, ", ")), nil
			}
			return "[PAUSED] Interrupt handled by parallel spawn call.", nil
		}

		// All done
		if agentResult, ok := result.Results[newID]; ok {
			return fmt.Sprintf("Agent restarted.\nOld agent: %s\nNew agent: %s\n\n%s",
				args.AgentID, newID, formatAgentCompletion(newID, agentResult)), nil
		}
		return fmt.Sprintf("Agent restarted.\nOld agent: %s\nNew agent: %s\nStatus: completed", args.AgentID, newID), nil

	default:
		return fmt.Sprintf("[ERROR] Unknown action: %s. Valid: spawn, wait, status, list, stop, restart", args.Action), nil
	}
}

// formatAgentCompletion formats completion info for an agent
func formatAgentCompletion(agentID string, info AgentCompletionInfo) string {
	result := fmt.Sprintf("Agent: %s\nSubtask: %s\nStatus: %s", agentID, info.SubtaskID, info.Status)
	if info.Result != "" {
		result += fmt.Sprintf("\nResult: %s", info.Result)
	}
	if info.Error != "" {
		result += fmt.Sprintf("\nError: %s", info.Error)
	}
	return result
}
