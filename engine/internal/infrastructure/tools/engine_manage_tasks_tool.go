package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// EngineTaskManager defines operations for managing engine tasks (consumer-side).
// Unified interface replacing System 1 (work.Manager) + System 2 (old EngineTaskManager).
// Subtasks are EngineTask with ParentTaskID set — no separate entity.
//
// IDs are uuid.UUID throughout the Go API. The only string ids allowed are at the
// external JSON/HTTP boundary — parsed here in the tool layer and in HTTP handlers.
type EngineTaskManager interface {
	// CRUD
	CreateTask(ctx context.Context, params CreateEngineTaskParams) (uuid.UUID, error)
	UpdateTask(ctx context.Context, id uuid.UUID, title, description string) error
	GetTask(ctx context.Context, id uuid.UUID) (*domain.EngineTask, error)
	ListTasks(ctx context.Context, sessionID string) ([]EngineTaskSummary, error)

	// Subtasks (EngineTask with ParentTaskID)
	CreateSubTask(ctx context.Context, parentID uuid.UUID, params CreateEngineTaskParams) (uuid.UUID, error)
	ListSubtasks(ctx context.Context, parentID uuid.UUID) ([]EngineTaskSummary, error)
	ListReadySubtasks(ctx context.Context, parentID uuid.UUID) ([]EngineTaskSummary, error)

	// State machine
	SetTaskStatus(ctx context.Context, id uuid.UUID, status string, result string) error
	ApproveTask(ctx context.Context, id uuid.UUID) error
	StartTask(ctx context.Context, id uuid.UUID) error
	CompleteTask(ctx context.Context, id uuid.UUID, result string) error
	FailTask(ctx context.Context, id uuid.UUID, reason string) error
	CancelTask(ctx context.Context, id uuid.UUID, reason string) error

	// Priority queue
	SetTaskPriority(ctx context.Context, id uuid.UUID, priority int) error
	GetNextTask(ctx context.Context, sessionID string) (*domain.EngineTask, error)

	// Agent assignment
	AssignTaskToAgent(ctx context.Context, id uuid.UUID, agentID string) error
	GetTaskByAgentID(ctx context.Context, agentID string) (*domain.EngineTask, error)
}

// CreateEngineTaskParams holds parameters for creating an engine task.
// BlockedBy is a list of pre-parsed task UUIDs (the tool layer converts the
// agent-supplied JSON strings to UUIDs before calling the manager).
// CreateEngineTaskParams holds the input for creating a new task.
// Q.5: AgentName, Source, SourceID dropped from DB — not persisted.
type CreateEngineTaskParams struct {
	Title              string
	Description        string
	AcceptanceCriteria []string
	SessionID          string
	Priority           int
	BlockedBy          []uuid.UUID
	RequireApproval    bool            // true = starts as draft, false = starts as pending
	Mode               domain.TaskMode // interactive (default) or background
}

// EngineTaskSummary is a lightweight view of an engine task.
// Q.5: AgentName, AssignedAgentID dropped (no longer persisted).
type EngineTaskSummary struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Status   string  `json:"status"`
	ParentID *string `json:"parent_id,omitempty"`
	Priority int     `json:"priority"`
}

type engineManageTasksArgs struct {
	Action             string                   `json:"action"`
	Tasks              []engineManageTaskCreate `json:"tasks,omitempty"`
	TaskID             string                   `json:"task_id,omitempty"`
	ParentTaskID       string                   `json:"parent_task_id,omitempty"`
	Title              string                   `json:"title,omitempty"`
	Description        string                   `json:"description,omitempty"`
	AcceptanceCriteria []string                 `json:"acceptance_criteria,omitempty"`
	Status             string                   `json:"status,omitempty"`
	Result             string                   `json:"result,omitempty"`
	Reason             string                   `json:"reason,omitempty"`
	Priority           *int                     `json:"priority,omitempty"`
	BlockedBy          []string                 `json:"blocked_by,omitempty"`
	AgentID            string                   `json:"agent_id,omitempty"`
	RequireApproval    bool                     `json:"require_approval,omitempty"`
}

type engineManageTaskCreate struct {
	Title              string   `json:"title"`
	Description        string   `json:"description,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	Priority           int      `json:"priority,omitempty"`
}

// EngineManageTasksTool implements engine task management as an Eino tool.
type EngineManageTasksTool struct {
	manager   EngineTaskManager
	sessionID string
}

// NewEngineManageTasksTool creates a new engine_manage_tasks tool.
func NewEngineManageTasksTool(manager EngineTaskManager, sessionID string) tool.InvokableTool {
	return &EngineManageTasksTool{manager: manager, sessionID: sessionID}
}

// parseToolTaskID converts the agent-supplied JSON string id to a UUID. Returns
// a stable error so the tool result formatter can surface it to the agent.
func parseToolTaskID(raw string) (uuid.UUID, error) {
	if raw == "" {
		return uuid.Nil, fmt.Errorf("task id is required")
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid task id %q: %w", raw, err)
	}
	return id, nil
}

// parseToolBlockerIDs converts a []string of task ids from JSON to []uuid.UUID.
func parseToolBlockerIDs(raw []string) ([]uuid.UUID, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		if s == "" {
			return nil, fmt.Errorf("blocked_by contains empty task id")
		}
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("blocked_by contains invalid task id %q: %w", s, err)
		}
		out = append(out, id)
	}
	return out, nil
}

func (t *EngineManageTasksTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "manage_tasks",
		Desc: `Manage tasks (universal work units). Subtasks are tasks with parent_task_id set.

Actions:
- "create": Create one or more tasks. Requires tasks array: [{title, description, acceptance_criteria, priority}]. Optional require_approval=true to start as draft (waits for user approval).
- "create_subtask": Create a sub-task. Requires parent_task_id, title, optional description, acceptance_criteria, priority, blocked_by.
- "update": Update task title/description. Requires task_id.
- "get": Get task details. Requires task_id.
- "list": List all tasks for current session.
- "list_subtasks": List all subtasks of a parent task. Requires parent_task_id.
- "get_ready": List pending subtasks with no unresolved blockers. Requires parent_task_id.
- "get_next": Get highest-priority task ready for work (in_progress first, then by priority DESC).
- "approve": Approve a draft task. Requires task_id.
- "start": Transition task to in_progress. Requires task_id.
- "complete": Mark task completed. Requires task_id, optional result.
- "fail": Mark task failed. Requires task_id, reason.
- "cancel": Cancel task and all non-terminal subtasks. Requires task_id, reason.
- "set_status": Set task status directly. Requires task_id, status, optional result.
- "set_priority": Set task priority (0=normal, 1=high, 2=critical). Requires task_id, priority.
- "assign": Assign task to an agent. Requires task_id, agent_id.`,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"action":              {Type: schema.String, Desc: "Action to perform", Required: true},
			"tasks":               {Type: schema.Array, Desc: "Array of {title, description, acceptance_criteria, priority} (for create)"},
			"task_id":             {Type: schema.String, Desc: "Task ID"},
			"parent_task_id":      {Type: schema.String, Desc: "Parent task ID (for create_subtask/list_subtasks/get_ready)"},
			"title":               {Type: schema.String, Desc: "Task title"},
			"description":         {Type: schema.String, Desc: "Task description"},
			"acceptance_criteria": {Type: schema.Array, Desc: "Acceptance criteria list"},
			"status":              {Type: schema.String, Desc: "Target status (for set_status)"},
			"result":              {Type: schema.String, Desc: "Task result text (for complete/set_status)"},
			"reason":              {Type: schema.String, Desc: "Reason (for fail/cancel)"},
			"priority":            {Type: schema.Integer, Desc: "Priority 0=normal, 1=high, 2=critical"},
			"blocked_by":          {Type: schema.Array, Desc: "Task IDs that block this task"},
			"agent_id":            {Type: schema.String, Desc: "Agent ID (for assign)"},
			"require_approval":    {Type: schema.Boolean, Desc: "Start task as draft requiring user approval (default false)"},
		}),
	}, nil
}

func (t *EngineManageTasksTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var args engineManageTasksArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid JSON: %v", err), nil
	}

	slog.InfoContext(ctx, "[manage_tasks] invoked", "action", args.Action, "session_id", t.sessionID)

	switch args.Action {
	case "create":
		return t.handleCreate(ctx, args)
	case "create_subtask":
		return t.handleCreateSubtask(ctx, args)
	case "update":
		return t.handleUpdate(ctx, args)
	case "get":
		return t.handleGet(ctx, args)
	case "list":
		return t.handleList(ctx)
	case "list_subtasks":
		return t.handleListSubtasks(ctx, args)
	case "get_ready":
		return t.handleGetReady(ctx, args)
	case "get_next":
		return t.handleGetNext(ctx)
	case "approve":
		return t.handleApprove(ctx, args)
	case "start":
		return t.handleStart(ctx, args)
	case "complete":
		return t.handleComplete(ctx, args)
	case "fail":
		return t.handleFail(ctx, args)
	case "cancel":
		return t.handleCancel(ctx, args)
	case "set_status":
		return t.handleSetStatus(ctx, args)
	case "set_priority":
		return t.handleSetPriority(ctx, args)
	case "assign":
		return t.handleAssign(ctx, args)
	default:
		return fmt.Sprintf("[ERROR] Unknown action: %q.", args.Action), nil
	}
}

func (t *EngineManageTasksTool) handleCreate(ctx context.Context, args engineManageTasksArgs) (string, error) {
	if len(args.Tasks) == 0 {
		return "[ERROR] tasks array is required and must not be empty for create action", nil
	}

	var ids []string
	for _, task := range args.Tasks {
		if task.Title == "" {
			return "[ERROR] each task must have a title", nil
		}
		id, err := t.manager.CreateTask(ctx, CreateEngineTaskParams{
			Title:              task.Title,
			Description:        task.Description,
			AcceptanceCriteria: task.AcceptanceCriteria,
			SessionID:          t.sessionID,
			Priority:           task.Priority,
			RequireApproval:    args.RequireApproval,
		})
		if err != nil {
			return fmt.Sprintf("[ERROR] failed to create task %q: %v", task.Title, err), nil
		}
		ids = append(ids, id.String())
	}

	if len(ids) == 1 {
		return fmt.Sprintf("Task created (ID: %s).", ids[0]), nil
	}

	result := fmt.Sprintf("%d tasks created:", len(ids))
	for i, id := range ids {
		result += fmt.Sprintf("\n  [%s] %s", id, args.Tasks[i].Title)
	}
	return result, nil
}

func (t *EngineManageTasksTool) handleCreateSubtask(ctx context.Context, args engineManageTasksArgs) (string, error) {
	if args.ParentTaskID == "" {
		return "[ERROR] parent_task_id is required for create_subtask", nil
	}
	if args.Title == "" {
		return "[ERROR] title is required for create_subtask", nil
	}

	parentID, err := parseToolTaskID(args.ParentTaskID)
	if err != nil {
		return fmt.Sprintf("[ERROR] parent task not found: %s", args.ParentTaskID), nil
	}

	blockers, err := parseToolBlockerIDs(args.BlockedBy)
	if err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}

	priority := 0
	if args.Priority != nil {
		priority = *args.Priority
	}

	id, err := t.manager.CreateSubTask(ctx, parentID, CreateEngineTaskParams{
		Title:              args.Title,
		Description:        args.Description,
		AcceptanceCriteria: args.AcceptanceCriteria,
		SessionID:          t.sessionID,
		Priority:           priority,
		BlockedBy:          blockers,
	})
	if err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	return fmt.Sprintf("Sub-task created (ID: %s, parent: %s).", id, args.ParentTaskID), nil
}

func (t *EngineManageTasksTool) handleUpdate(ctx context.Context, args engineManageTasksArgs) (string, error) {
	if args.TaskID == "" {
		return "[ERROR] task_id is required for update", nil
	}
	if args.Title == "" && args.Description == "" {
		return "[ERROR] at least one of title or description must be provided for update", nil
	}
	id, err := parseToolTaskID(args.TaskID)
	if err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	if err := t.manager.UpdateTask(ctx, id, args.Title, args.Description); err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	return fmt.Sprintf("Task %s updated.", args.TaskID), nil
}

// checkSessionOwnership verifies the task belongs to the current session.
func (t *EngineManageTasksTool) checkSessionOwnership(ctx context.Context, id uuid.UUID) (*domain.EngineTask, error) {
	task, err := t.manager.GetTask(ctx, id)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task %s not found", id)
	}
	if task.SessionID != t.sessionID {
		return nil, fmt.Errorf("task %s not found in current session", id)
	}
	return task, nil
}

func (t *EngineManageTasksTool) handleGet(ctx context.Context, args engineManageTasksArgs) (string, error) {
	if args.TaskID == "" {
		return "[ERROR] task_id is required for get", nil
	}
	id, err := parseToolTaskID(args.TaskID)
	if err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	task, err := t.checkSessionOwnership(ctx, id)
	if err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	return formatTaskDetail(task), nil
}

func (t *EngineManageTasksTool) handleList(ctx context.Context) (string, error) {
	tasks, err := t.manager.ListTasks(ctx, t.sessionID)
	if err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	return formatTaskList(tasks), nil
}

func (t *EngineManageTasksTool) handleListSubtasks(ctx context.Context, args engineManageTasksArgs) (string, error) {
	if args.ParentTaskID == "" {
		return "[ERROR] parent_task_id is required for list_subtasks", nil
	}
	parentID, err := parseToolTaskID(args.ParentTaskID)
	if err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	subs, err := t.manager.ListSubtasks(ctx, parentID)
	if err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	if len(subs) == 0 {
		return fmt.Sprintf("No subtasks for parent %s.", args.ParentTaskID), nil
	}
	return formatTaskList(subs), nil
}

func (t *EngineManageTasksTool) handleGetReady(ctx context.Context, args engineManageTasksArgs) (string, error) {
	if args.ParentTaskID == "" {
		return "[ERROR] parent_task_id is required for get_ready", nil
	}
	parentID, err := parseToolTaskID(args.ParentTaskID)
	if err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	ready, err := t.manager.ListReadySubtasks(ctx, parentID)
	if err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	if len(ready) == 0 {
		return fmt.Sprintf("No ready subtasks for parent %s.", args.ParentTaskID), nil
	}
	return formatTaskList(ready), nil
}

func (t *EngineManageTasksTool) handleGetNext(ctx context.Context) (string, error) {
	task, err := t.manager.GetNextTask(ctx, t.sessionID)
	if err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	if task == nil {
		return "No tasks ready for work.", nil
	}
	return formatTaskDetail(task), nil
}

func (t *EngineManageTasksTool) handleApprove(ctx context.Context, args engineManageTasksArgs) (string, error) {
	if args.TaskID == "" {
		return "[ERROR] task_id is required for approve", nil
	}
	id, err := parseToolTaskID(args.TaskID)
	if err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	if _, err := t.checkSessionOwnership(ctx, id); err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	if err := t.manager.ApproveTask(ctx, id); err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	return fmt.Sprintf("Task %s approved.", args.TaskID), nil
}

func (t *EngineManageTasksTool) handleStart(ctx context.Context, args engineManageTasksArgs) (string, error) {
	if args.TaskID == "" {
		return "[ERROR] task_id is required for start", nil
	}
	id, err := parseToolTaskID(args.TaskID)
	if err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	if _, err := t.checkSessionOwnership(ctx, id); err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	if err := t.manager.StartTask(ctx, id); err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	return fmt.Sprintf("Task %s started.", args.TaskID), nil
}

func (t *EngineManageTasksTool) handleComplete(ctx context.Context, args engineManageTasksArgs) (string, error) {
	if args.TaskID == "" {
		return "[ERROR] task_id is required for complete", nil
	}
	id, err := parseToolTaskID(args.TaskID)
	if err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	if _, err := t.checkSessionOwnership(ctx, id); err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	if err := t.manager.CompleteTask(ctx, id, args.Result); err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	return fmt.Sprintf("Task %s completed.", args.TaskID), nil
}

func (t *EngineManageTasksTool) handleFail(ctx context.Context, args engineManageTasksArgs) (string, error) {
	if args.TaskID == "" {
		return "[ERROR] task_id is required for fail", nil
	}
	if args.Reason == "" {
		return "[ERROR] reason is required for fail", nil
	}
	id, err := parseToolTaskID(args.TaskID)
	if err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	if _, err := t.checkSessionOwnership(ctx, id); err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	if err := t.manager.FailTask(ctx, id, args.Reason); err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	return fmt.Sprintf("Task %s failed: %s", args.TaskID, args.Reason), nil
}

func (t *EngineManageTasksTool) handleCancel(ctx context.Context, args engineManageTasksArgs) (string, error) {
	if args.TaskID == "" {
		return "[ERROR] task_id is required for cancel", nil
	}
	id, err := parseToolTaskID(args.TaskID)
	if err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	if _, err := t.checkSessionOwnership(ctx, id); err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	if err := t.manager.CancelTask(ctx, id, args.Reason); err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	return fmt.Sprintf("Task %s cancelled.", args.TaskID), nil
}

func (t *EngineManageTasksTool) handleSetStatus(ctx context.Context, args engineManageTasksArgs) (string, error) {
	if args.TaskID == "" {
		return "[ERROR] task_id is required for set_status", nil
	}
	if args.Status == "" {
		return "[ERROR] status is required for set_status", nil
	}
	id, err := parseToolTaskID(args.TaskID)
	if err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	if err := t.manager.SetTaskStatus(ctx, id, args.Status, args.Result); err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	return fmt.Sprintf("Task %s status set to %q.", args.TaskID, args.Status), nil
}

func (t *EngineManageTasksTool) handleSetPriority(ctx context.Context, args engineManageTasksArgs) (string, error) {
	if args.TaskID == "" {
		return "[ERROR] task_id is required for set_priority", nil
	}
	if args.Priority == nil {
		return "[ERROR] priority is required for set_priority (0=normal, 1=high, 2=critical)", nil
	}
	id, err := parseToolTaskID(args.TaskID)
	if err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	if _, err := t.checkSessionOwnership(ctx, id); err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	if err := t.manager.SetTaskPriority(ctx, id, *args.Priority); err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	return fmt.Sprintf("Task %s priority set to %d.", args.TaskID, *args.Priority), nil
}

func (t *EngineManageTasksTool) handleAssign(ctx context.Context, args engineManageTasksArgs) (string, error) {
	if args.TaskID == "" {
		return "[ERROR] task_id is required for assign", nil
	}
	if args.AgentID == "" {
		return "[ERROR] agent_id is required for assign", nil
	}
	id, err := parseToolTaskID(args.TaskID)
	if err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	if _, err := t.checkSessionOwnership(ctx, id); err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	if err := t.manager.AssignTaskToAgent(ctx, id, args.AgentID); err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}
	return fmt.Sprintf("Task %s assigned to agent %s.", args.TaskID, args.AgentID), nil
}

// --- formatting helpers ---

func formatTaskList(tasks []EngineTaskSummary) string {
	if len(tasks) == 0 {
		return "No tasks found."
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Tasks (%d):\n", len(tasks)))
	for _, tk := range tasks {
		line := fmt.Sprintf("  [%s] %q — %s (priority=%d)", tk.ID, tk.Title, tk.Status, tk.Priority)
		if tk.ParentID != nil {
			line += fmt.Sprintf(" (parent: %s)", *tk.ParentID)
		}
		sb.WriteString(line + "\n")
	}
	return sb.String()
}

func formatTaskDetail(t *domain.EngineTask) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Task [%s]\n", t.ID))
	sb.WriteString(fmt.Sprintf("  Title: %s\n", t.Title))
	sb.WriteString(fmt.Sprintf("  Status: %s\n", t.Status))
	sb.WriteString(fmt.Sprintf("  Priority: %d\n", t.Priority))
	if t.Description != "" {
		sb.WriteString(fmt.Sprintf("  Description: %s\n", t.Description))
	}
	if len(t.AcceptanceCriteria) > 0 {
		sb.WriteString("  Acceptance Criteria:\n")
		for _, c := range t.AcceptanceCriteria {
			sb.WriteString(fmt.Sprintf("    - %s\n", c))
		}
	}
	if t.ParentTaskID != nil {
		sb.WriteString(fmt.Sprintf("  Parent: %s\n", t.ParentTaskID.String()))
	}
	if len(t.BlockedBy) > 0 {
		blockerStrs := make([]string, 0, len(t.BlockedBy))
		for _, id := range t.BlockedBy {
			blockerStrs = append(blockerStrs, id.String())
		}
		sb.WriteString(fmt.Sprintf("  Blocked By: %s\n", strings.Join(blockerStrs, ", ")))
	}
	if t.Result != "" {
		sb.WriteString(fmt.Sprintf("  Result: %s\n", t.Result))
	}
	if t.Error != "" {
		sb.WriteString(fmt.Sprintf("  Error: %s\n", t.Error))
	}
	return sb.String()
}
