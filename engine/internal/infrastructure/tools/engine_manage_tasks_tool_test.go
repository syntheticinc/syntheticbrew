package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// mockEngineTaskManager implements EngineTaskManager for testing.
// Uses uuid.UUID IDs throughout, matching the production adapter.
type mockEngineTaskManager struct {
	tasks          map[uuid.UUID]CreateEngineTaskParams
	parents        map[uuid.UUID]uuid.UUID // child_id -> parent_id
	statusCalls    []setStatusCall
	updateCalls    []updateCall
	priorityCalls  []priorityCall
	assignCalls    []assignCall
	listResult     []EngineTaskSummary
	subtasksResult []EngineTaskSummary
	readyResult    []EngineTaskSummary
	getTaskResult  *domain.EngineTask
	nextTaskResult *domain.EngineTask
	listErr        error
	createErr      error
	lastCreatedID  uuid.UUID
}

type priorityCall struct {
	ID       uuid.UUID
	Priority int
}

type assignCall struct {
	TaskID  uuid.UUID
	AgentID string
}

type setStatusCall struct {
	ID     uuid.UUID
	Status string
	Result string
}

type updateCall struct {
	ID          uuid.UUID
	Title       string
	Description string
}

func newMockEngineTaskManager() *mockEngineTaskManager {
	return &mockEngineTaskManager{
		tasks:   make(map[uuid.UUID]CreateEngineTaskParams),
		parents: make(map[uuid.UUID]uuid.UUID),
	}
}

func (m *mockEngineTaskManager) CreateTask(_ context.Context, params CreateEngineTaskParams) (uuid.UUID, error) {
	if m.createErr != nil {
		return uuid.Nil, m.createErr
	}
	id := uuid.New()
	m.tasks[id] = params
	m.lastCreatedID = id
	return id, nil
}

func (m *mockEngineTaskManager) UpdateTask(_ context.Context, id uuid.UUID, title, description string) error {
	m.updateCalls = append(m.updateCalls, updateCall{ID: id, Title: title, Description: description})
	return nil
}

func (m *mockEngineTaskManager) SetTaskStatus(_ context.Context, id uuid.UUID, status string, result string) error {
	m.statusCalls = append(m.statusCalls, setStatusCall{ID: id, Status: status, Result: result})
	return nil
}

func (m *mockEngineTaskManager) ListTasks(_ context.Context, _ string) ([]EngineTaskSummary, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.listResult, nil
}

func (m *mockEngineTaskManager) CreateSubTask(_ context.Context, parentID uuid.UUID, params CreateEngineTaskParams) (uuid.UUID, error) {
	if m.createErr != nil {
		return uuid.Nil, m.createErr
	}
	id := uuid.New()
	m.tasks[id] = params
	m.parents[id] = parentID
	m.lastCreatedID = id
	return id, nil
}

// Stubs for the rest of the EngineTaskManager interface.
func (m *mockEngineTaskManager) GetTask(_ context.Context, id uuid.UUID) (*domain.EngineTask, error) {
	if m.getTaskResult != nil {
		return m.getTaskResult, nil
	}
	// Return a stub task with matching session for ownership checks.
	return &domain.EngineTask{
		ID:        id,
		SessionID: "session-1",
		Status:    domain.EngineTaskStatusPending,
	}, nil
}

func (m *mockEngineTaskManager) ListSubtasks(_ context.Context, _ uuid.UUID) ([]EngineTaskSummary, error) {
	return m.subtasksResult, nil
}

func (m *mockEngineTaskManager) ListReadySubtasks(_ context.Context, _ uuid.UUID) ([]EngineTaskSummary, error) {
	return m.readyResult, nil
}

func (m *mockEngineTaskManager) ApproveTask(_ context.Context, id uuid.UUID) error {
	m.statusCalls = append(m.statusCalls, setStatusCall{ID: id, Status: "approved"})
	return nil
}

func (m *mockEngineTaskManager) StartTask(_ context.Context, id uuid.UUID) error {
	m.statusCalls = append(m.statusCalls, setStatusCall{ID: id, Status: "in_progress"})
	return nil
}

func (m *mockEngineTaskManager) CompleteTask(_ context.Context, id uuid.UUID, result string) error {
	m.statusCalls = append(m.statusCalls, setStatusCall{ID: id, Status: "completed", Result: result})
	return nil
}

func (m *mockEngineTaskManager) FailTask(_ context.Context, id uuid.UUID, reason string) error {
	m.statusCalls = append(m.statusCalls, setStatusCall{ID: id, Status: "failed", Result: reason})
	return nil
}

func (m *mockEngineTaskManager) CancelTask(_ context.Context, id uuid.UUID, _ string) error {
	m.statusCalls = append(m.statusCalls, setStatusCall{ID: id, Status: "cancelled"})
	return nil
}

func (m *mockEngineTaskManager) SetTaskPriority(_ context.Context, id uuid.UUID, priority int) error {
	m.priorityCalls = append(m.priorityCalls, priorityCall{ID: id, Priority: priority})
	return nil
}

func (m *mockEngineTaskManager) GetNextTask(_ context.Context, _ string) (*domain.EngineTask, error) {
	return m.nextTaskResult, nil
}

func (m *mockEngineTaskManager) AssignTaskToAgent(_ context.Context, id uuid.UUID, agentID string) error {
	m.assignCalls = append(m.assignCalls, assignCall{TaskID: id, AgentID: agentID})
	return nil
}

func (m *mockEngineTaskManager) GetTaskByAgentID(_ context.Context, _ string) (*domain.EngineTask, error) {
	return nil, nil
}

func TestEngineManageTasksTool_Create_Single(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{
		Action: "create",
		Tasks:  []engineManageTaskCreate{{Title: "Fix bug", Description: "Fix the login bug"}},
	})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.Contains(t, result, "Task created (ID: ")
	assert.Equal(t, 1, len(mgr.tasks))
	created := mgr.tasks[mgr.lastCreatedID]
	assert.Equal(t, "Fix bug", created.Title)
	assert.Equal(t, "session-1", created.SessionID)
}

func TestEngineManageTasksTool_Create_Multiple(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{
		Action: "create",
		Tasks: []engineManageTaskCreate{
			{Title: "Task A"},
			{Title: "Task B", Description: "Desc B"},
		},
	})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.Contains(t, result, "2 tasks created")
	assert.Contains(t, result, "Task A")
	assert.Contains(t, result, "Task B")
	assert.Equal(t, 2, len(mgr.tasks))
}

func TestEngineManageTasksTool_Create_EmptyTasks(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{Action: "create", Tasks: nil})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "tasks array is required")
}

func TestEngineManageTasksTool_Create_MissingTitle(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{
		Action: "create",
		Tasks:  []engineManageTaskCreate{{Title: "", Description: "no title"}},
	})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "must have a title")
}

func TestEngineManageTasksTool_Update(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	taskID := uuid.New()
	args, _ := json.Marshal(engineManageTasksArgs{
		Action:      "update",
		TaskID:      taskID.String(),
		Title:       "New Title",
		Description: "New Desc",
	})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.Contains(t, result, "updated")
	require.Len(t, mgr.updateCalls, 1)
	assert.Equal(t, taskID, mgr.updateCalls[0].ID)
	assert.Equal(t, "New Title", mgr.updateCalls[0].Title)
}

func TestEngineManageTasksTool_Update_NoID(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{Action: "update", Title: "Something"})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "task_id is required")
}

func TestEngineManageTasksTool_Update_NoFields(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{Action: "update", TaskID: uuid.New().String()})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "at least one of title or description")
}

func TestEngineManageTasksTool_SetStatus(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	taskID := uuid.New()
	args, _ := json.Marshal(engineManageTasksArgs{
		Action: "set_status",
		TaskID: taskID.String(),
		Status: "completed",
		Result: "All done",
	})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.Contains(t, result, "status set to")
	require.Len(t, mgr.statusCalls, 1)
	assert.Equal(t, "completed", mgr.statusCalls[0].Status)
	assert.Equal(t, "All done", mgr.statusCalls[0].Result)
}

func TestEngineManageTasksTool_SetStatus_NoStatus(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{Action: "set_status", TaskID: uuid.New().String()})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "status is required")
}

func TestEngineManageTasksTool_List(t *testing.T) {
	parentID := uuid.New().String()
	mgr := newMockEngineTaskManager()
	mgr.listResult = []EngineTaskSummary{
		{ID: uuid.New().String(), Title: "Task 1", Status: "pending"},
		{ID: uuid.New().String(), Title: "Task 2", Status: "completed", ParentID: &parentID},
	}
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{Action: "list"})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.Contains(t, result, "Tasks (2)")
	assert.Contains(t, result, "Task 1")
	assert.Contains(t, result, "Task 2")
	assert.Contains(t, result, "parent: "+parentID)
}

func TestEngineManageTasksTool_List_Empty(t *testing.T) {
	mgr := newMockEngineTaskManager()
	mgr.listResult = nil
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{Action: "list"})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.Contains(t, result, "No tasks found")
}

func TestEngineManageTasksTool_CreateSubtask(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	parentID := uuid.New()
	args, _ := json.Marshal(engineManageTasksArgs{
		Action:       "create_subtask",
		ParentTaskID: parentID.String(),
		Title:        "Sub task",
		Description:  "Sub desc",
	})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.Contains(t, result, "Sub-task created (ID: ")
	assert.Contains(t, result, "parent: "+parentID.String())
}

func TestEngineManageTasksTool_CreateSubtask_NoParent(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{Action: "create_subtask", Title: "Sub"})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "parent_task_id is required")
}

func TestEngineManageTasksTool_CreateSubtask_NoTitle(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{Action: "create_subtask", ParentTaskID: uuid.New().String()})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "title is required")
}

func TestEngineManageTasksTool_UnknownAction(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{Action: "destroy"})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "Unknown action")
}

func TestEngineManageTasksTool_InvalidJSON(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	result, err := tl.InvokableRun(context.Background(), "not json at all")

	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "Invalid JSON")
}

func TestEngineManageTasksTool_CreateError(t *testing.T) {
	mgr := newMockEngineTaskManager()
	mgr.createErr = fmt.Errorf("db connection lost")
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{
		Action: "create",
		Tasks:  []engineManageTaskCreate{{Title: "Fail"}},
	})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "db connection lost")
}

func TestEngineManageTasksTool_ListError(t *testing.T) {
	mgr := newMockEngineTaskManager()
	mgr.listErr = fmt.Errorf("query failed")
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{Action: "list"})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "query failed")
}

// --- Lifecycle action tests ---

func TestEngineManageTasksTool_Approve(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	taskID := uuid.New()
	args, _ := json.Marshal(engineManageTasksArgs{Action: "approve", TaskID: taskID.String()})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.NotContains(t, result, "[ERROR]")
	require.Len(t, mgr.statusCalls, 1)
	assert.Equal(t, taskID, mgr.statusCalls[0].ID)
	assert.Equal(t, "approved", mgr.statusCalls[0].Status)
}

func TestEngineManageTasksTool_Approve_MissingID(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{Action: "approve"})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Empty(t, mgr.statusCalls)
}

func TestEngineManageTasksTool_Start(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{Action: "start", TaskID: uuid.New().String()})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.NotContains(t, result, "[ERROR]")
	require.Len(t, mgr.statusCalls, 1)
	assert.Equal(t, "in_progress", mgr.statusCalls[0].Status)
}

func TestEngineManageTasksTool_Complete_WithResult(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{
		Action: "complete",
		TaskID: uuid.New().String(),
		Result: "deployed",
	})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.NotContains(t, result, "[ERROR]")
	require.Len(t, mgr.statusCalls, 1)
	assert.Equal(t, "completed", mgr.statusCalls[0].Status)
	assert.Equal(t, "deployed", mgr.statusCalls[0].Result)
}

func TestEngineManageTasksTool_Fail_RequiresReason(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{Action: "fail", TaskID: uuid.New().String()})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Empty(t, mgr.statusCalls)
}

func TestEngineManageTasksTool_Fail_WithReason(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{
		Action: "fail",
		TaskID: uuid.New().String(),
		Reason: "timeout",
	})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.NotContains(t, result, "[ERROR]")
	require.Len(t, mgr.statusCalls, 1)
	assert.Equal(t, "failed", mgr.statusCalls[0].Status)
	assert.Equal(t, "timeout", mgr.statusCalls[0].Result)
}

func TestEngineManageTasksTool_SetPriority(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	taskID := uuid.New()
	prio := 2
	args, _ := json.Marshal(engineManageTasksArgs{
		Action:   "set_priority",
		TaskID:   taskID.String(),
		Priority: &prio,
	})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.NotContains(t, result, "[ERROR]")
	require.Len(t, mgr.priorityCalls, 1)
	assert.Equal(t, taskID, mgr.priorityCalls[0].ID)
	assert.Equal(t, 2, mgr.priorityCalls[0].Priority)
}

func TestEngineManageTasksTool_Assign(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	taskID := uuid.New()
	args, _ := json.Marshal(engineManageTasksArgs{
		Action:  "assign",
		TaskID:  taskID.String(),
		AgentID: "agent-abc",
	})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.NotContains(t, result, "[ERROR]")
	require.Len(t, mgr.assignCalls, 1)
	assert.Equal(t, taskID, mgr.assignCalls[0].TaskID)
	assert.Equal(t, "agent-abc", mgr.assignCalls[0].AgentID)
}

func TestEngineManageTasksTool_ListSubtasks(t *testing.T) {
	mgr := newMockEngineTaskManager()
	mgr.subtasksResult = []EngineTaskSummary{
		{ID: "c-1", Title: "Child 1", Status: "pending"},
		{ID: "c-2", Title: "Child 2", Status: "completed"},
	}
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{
		Action:       "list_subtasks",
		ParentTaskID: uuid.New().String(),
	})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.NotContains(t, result, "[ERROR]")
	assert.Contains(t, result, "c-1")
	assert.Contains(t, result, "c-2")
}

func TestEngineManageTasksTool_GetReady_BlocksOnIncompleteBlockers(t *testing.T) {
	mgr := newMockEngineTaskManager()
	// Mock: adapter returned empty list → blockers still open.
	mgr.readyResult = []EngineTaskSummary{}
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{
		Action:       "get_ready",
		ParentTaskID: uuid.New().String(),
	})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.Contains(t, result, "No ready subtasks")
}

func TestEngineManageTasksTool_GetReady_UnlocksOnComplete(t *testing.T) {
	mgr := newMockEngineTaskManager()
	mgr.readyResult = []EngineTaskSummary{
		{ID: "child-2", Title: "Unblocked", Status: "pending"},
	}
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{
		Action:       "get_ready",
		ParentTaskID: uuid.New().String(),
	})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.NotContains(t, result, "[ERROR]")
	assert.Contains(t, result, "child-2")
}

func TestEngineManageTasksTool_CreateSubtask_WithBlockedBy(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	blocker1 := uuid.New()
	blocker2 := uuid.New()
	prio := 1
	args, _ := json.Marshal(engineManageTasksArgs{
		Action:       "create_subtask",
		ParentTaskID: uuid.New().String(),
		Title:        "Dependent subtask",
		Priority:     &prio,
		BlockedBy:    []string{blocker1.String(), blocker2.String()},
	})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.NotContains(t, result, "[ERROR]")
	require.Len(t, mgr.tasks, 1)
	created := mgr.tasks[mgr.lastCreatedID]
	assert.Equal(t, 1, created.Priority)
	assert.Equal(t, []uuid.UUID{blocker1, blocker2}, created.BlockedBy)
}

func TestEngineManageTasksTool_Create_WithRequireApproval(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{
		Action:          "create",
		Tasks:           []engineManageTaskCreate{{Title: "Prod deploy", Priority: 2}},
		RequireApproval: true,
	})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	assert.NotContains(t, result, "[ERROR]")
	require.Len(t, mgr.tasks, 1)
	created := mgr.tasks[mgr.lastCreatedID]
	assert.True(t, created.RequireApproval)
	assert.Equal(t, 2, created.Priority)
}

func TestEngineManageTasksTool_GetNext_Empty(t *testing.T) {
	mgr := newMockEngineTaskManager()
	tl := NewEngineManageTasksTool(mgr, "session-1")

	args, _ := json.Marshal(engineManageTasksArgs{Action: "get_next"})
	result, err := tl.InvokableRun(context.Background(), string(args))

	require.NoError(t, err)
	// No task available → message, not error.
	assert.NotContains(t, result, "[ERROR]")
}
