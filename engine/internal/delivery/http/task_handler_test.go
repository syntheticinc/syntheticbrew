package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/bytebrew/engine/internal/domain"
)

type mockTaskService struct {
	createdID    uuid.UUID
	tasks        []TaskResponse
	taskCount    int64
	taskDetail   *TaskDetailResponse
	cancelledID  uuid.UUID
	cancelReason string
	err          error
}

func (m *mockTaskService) CreateTask(_ context.Context, params CreateTaskRequest, _ ActorInfo) (uuid.UUID, error) {
	if m.err != nil {
		return uuid.Nil, m.err
	}
	return m.createdID, nil
}

func (m *mockTaskService) ListTasks(_ context.Context, _ TaskListFilter, _ ActorInfo) ([]TaskResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.tasks, nil
}

func (m *mockTaskService) CountTasks(_ context.Context, _ TaskListFilter, _ ActorInfo) (int64, error) {
	if m.err != nil {
		return 0, m.err
	}
	return m.taskCount, nil
}

func (m *mockTaskService) GetTask(_ context.Context, id uuid.UUID, _ ActorInfo) (*TaskDetailResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.taskDetail != nil && m.taskDetail.ID == id.String() {
		return m.taskDetail, nil
	}
	return nil, nil
}

func (m *mockTaskService) CancelTask(_ context.Context, id uuid.UUID, reason string, _ ActorInfo) error {
	if m.err != nil {
		return m.err
	}
	m.cancelledID = id
	m.cancelReason = reason
	return nil
}

func (m *mockTaskService) ListSubtasks(_ context.Context, _ uuid.UUID, _ ActorInfo) ([]TaskResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.tasks, nil
}

func (m *mockTaskService) ApproveTask(_ context.Context, _ uuid.UUID, _ ActorInfo) error {
	return m.err
}

func (m *mockTaskService) StartTask(_ context.Context, _ uuid.UUID, _ ActorInfo) error {
	return m.err
}

func (m *mockTaskService) CompleteTask(_ context.Context, _ uuid.UUID, _ string, _ ActorInfo) error {
	return m.err
}

func (m *mockTaskService) FailTask(_ context.Context, _ uuid.UUID, _ string, _ ActorInfo) error {
	return m.err
}

func (m *mockTaskService) SetTaskPriority(_ context.Context, _ uuid.UUID, _ int, _ ActorInfo) error {
	return m.err
}

func newTaskRouter(handler *TaskHandler) http.Handler {
	return newTaskTestRouter(handler)
}

func TestTaskHandler_Create(t *testing.T) {
	createdID := uuid.New()
	mock := &mockTaskService{createdID: createdID}
	handler := NewTaskHandler(mock)
	router := newTaskRouter(handler)

	body, _ := json.Marshal(CreateTaskRequest{
		Title:     "Deploy v2",
		AgentName: "devops",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	var resp map[string]interface{}
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, createdID.String(), resp["task_id"])
	assert.Equal(t, "pending", resp["status"])
}

func TestTaskHandler_Create_MissingTitle(t *testing.T) {
	handler := NewTaskHandler(&mockTaskService{})
	router := newTaskRouter(handler)

	body, _ := json.Marshal(CreateTaskRequest{AgentName: "devops"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTaskHandler_Create_MissingAgent(t *testing.T) {
	handler := NewTaskHandler(&mockTaskService{})
	router := newTaskRouter(handler)

	body, _ := json.Marshal(CreateTaskRequest{Title: "test"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTaskHandler_Create_TitleTooLong(t *testing.T) {
	handler := NewTaskHandler(&mockTaskService{})
	router := newTaskRouter(handler)

	longTitle := make([]byte, MaxTaskTitleLen+1)
	for i := range longTitle {
		longTitle[i] = 'a'
	}
	body, _ := json.Marshal(CreateTaskRequest{Title: string(longTitle), AgentName: "devops"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTaskHandler_Create_TooManyBlockers(t *testing.T) {
	handler := NewTaskHandler(&mockTaskService{})
	router := newTaskRouter(handler)

	blockers := make([]string, MaxBlockers+1)
	for i := range blockers {
		blockers[i] = uuid.New().String()
	}
	body, _ := json.Marshal(CreateTaskRequest{Title: "test", AgentName: "a", BlockedBy: blockers})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTaskHandler_List(t *testing.T) {
	tasks := []TaskResponse{
		{ID: uuid.New().String(), Title: "Task 1", AgentName: "sales", Status: "completed"},
		{ID: uuid.New().String(), Title: "Task 2", AgentName: "devops", Status: "pending"},
	}
	handler := NewTaskHandler(&mockTaskService{tasks: tasks, taskCount: 2})
	router := newTaskRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result PaginatedTaskResponse
	err := json.NewDecoder(rec.Body).Decode(&result)
	require.NoError(t, err)
	assert.Len(t, result.Data, 2)
	assert.Equal(t, int64(2), result.Total)
}

func TestTaskHandler_List_WithFilters(t *testing.T) {
	handler := NewTaskHandler(&mockTaskService{tasks: []TaskResponse{}})
	router := newTaskRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?source=api&agent_name=sales&status=pending", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestTaskHandler_Get(t *testing.T) {
	detailID := uuid.New()
	detail := &TaskDetailResponse{
		TaskResponse: TaskResponse{ID: detailID.String(), Title: "Build feature", AgentName: "coder", Status: "in_progress"},
		Mode:         "interactive",
	}
	handler := NewTaskHandler(&mockTaskService{taskDetail: detail})
	router := newTaskRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+detailID.String(), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result TaskDetailResponse
	err := json.NewDecoder(rec.Body).Decode(&result)
	require.NoError(t, err)
	assert.Equal(t, detailID.String(), result.ID)
	assert.Equal(t, "Build feature", result.Title)
}

func TestTaskHandler_Get_NotFound(t *testing.T) {
	handler := NewTaskHandler(&mockTaskService{})
	router := newTaskRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestTaskHandler_Get_NotFound_ViaError(t *testing.T) {
	handler := NewTaskHandler(&mockTaskService{err: domain.ErrEngineTaskNotFound})
	router := newTaskRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestTaskHandler_Get_InvalidID(t *testing.T) {
	handler := NewTaskHandler(&mockTaskService{})
	router := newTaskRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/abc", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	// Non-UUID path param must be rejected at the handler with 400.
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTaskHandler_Cancel(t *testing.T) {
	mock := &mockTaskService{}
	handler := NewTaskHandler(mock)
	router := newTaskRouter(handler)

	targetID := uuid.New()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+targetID.String(), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, targetID, mock.cancelledID)
}

func TestTaskHandler_Cancel_Error(t *testing.T) {
	handler := NewTaskHandler(&mockTaskService{err: fmt.Errorf("not found")})
	router := newTaskRouter(handler)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestTaskHandler_List_NoPagination_ReturnsPaginated(t *testing.T) {
	tasks := []TaskResponse{
		{ID: uuid.New().String(), Title: "T1", Status: "pending"},
		{ID: uuid.New().String(), Title: "T2", Status: "completed"},
	}
	handler := NewTaskHandler(&mockTaskService{tasks: tasks, taskCount: 2})
	router := newTaskRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result PaginatedTaskResponse
	err := json.NewDecoder(rec.Body).Decode(&result)
	require.NoError(t, err)
	assert.Len(t, result.Data, 2)
	assert.Equal(t, DefaultTaskListLimit, result.PerPage)
}

func TestTaskHandler_List_WithPagination(t *testing.T) {
	tasks := []TaskResponse{
		{ID: uuid.New().String(), Title: "T3", Status: "pending"},
	}
	handler := NewTaskHandler(&mockTaskService{tasks: tasks, taskCount: 25})
	router := newTaskRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?page=2&per_page=10", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result PaginatedTaskResponse
	err := json.NewDecoder(rec.Body).Decode(&result)
	require.NoError(t, err)
	assert.Len(t, result.Data, 1)
	assert.Equal(t, int64(25), result.Total)
	assert.Equal(t, 2, result.Page)
	assert.Equal(t, 10, result.PerPage)
	assert.Equal(t, 3, result.TotalPages)
}

func TestTaskHandler_List_PaginationDefaults(t *testing.T) {
	handler := NewTaskHandler(&mockTaskService{tasks: []TaskResponse{}, taskCount: 0})
	router := newTaskRouter(handler)

	// Only page param triggers pagination, per_page defaults to DefaultTaskListLimit
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?page=1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result PaginatedTaskResponse
	err := json.NewDecoder(rec.Body).Decode(&result)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Page)
	assert.Equal(t, DefaultTaskListLimit, result.PerPage)
}

func TestTaskHandler_List_PerPageCappedAt100(t *testing.T) {
	handler := NewTaskHandler(&mockTaskService{tasks: []TaskResponse{}, taskCount: 0})
	router := newTaskRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?page=1&per_page=500", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result PaginatedTaskResponse
	err := json.NewDecoder(rec.Body).Decode(&result)
	require.NoError(t, err)
	assert.Equal(t, 100, result.PerPage)
}

// lifecycleCapturingMock records which lifecycle call was made.
type lifecycleCapturingMock struct {
	mockTaskService
	lastAction string
	lastID     uuid.UUID
	lastResult string
	lastReason string
	lastPrio   int
	subtasks   []TaskResponse
}

func (m *lifecycleCapturingMock) ApproveTask(_ context.Context, id uuid.UUID, _ ActorInfo) error {
	m.lastAction = "approve"
	m.lastID = id
	return m.err
}
func (m *lifecycleCapturingMock) StartTask(_ context.Context, id uuid.UUID, _ ActorInfo) error {
	m.lastAction = "start"
	m.lastID = id
	return m.err
}
func (m *lifecycleCapturingMock) CompleteTask(_ context.Context, id uuid.UUID, result string, _ ActorInfo) error {
	m.lastAction = "complete"
	m.lastID = id
	m.lastResult = result
	return m.err
}
func (m *lifecycleCapturingMock) FailTask(_ context.Context, id uuid.UUID, reason string, _ ActorInfo) error {
	m.lastAction = "fail"
	m.lastID = id
	m.lastReason = reason
	return m.err
}
func (m *lifecycleCapturingMock) SetTaskPriority(_ context.Context, id uuid.UUID, priority int, _ ActorInfo) error {
	m.lastAction = "priority"
	m.lastID = id
	m.lastPrio = priority
	return m.err
}
func (m *lifecycleCapturingMock) ListSubtasks(_ context.Context, parentID uuid.UUID, _ ActorInfo) ([]TaskResponse, error) {
	m.lastAction = "subtasks"
	m.lastID = parentID
	return m.subtasks, m.err
}

func TestTaskHandler_Approve(t *testing.T) {
	mock := &lifecycleCapturingMock{}
	router := newTaskRouter(NewTaskHandler(mock))

	target := uuid.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+target.String()+"/approve", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "approve", mock.lastAction)
	assert.Equal(t, target, mock.lastID)
}

func TestTaskHandler_Start(t *testing.T) {
	mock := &lifecycleCapturingMock{}
	router := newTaskRouter(NewTaskHandler(mock))

	target := uuid.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+target.String()+"/start", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "start", mock.lastAction)
	assert.Equal(t, target, mock.lastID)
}

func TestTaskHandler_Complete_WithResult(t *testing.T) {
	mock := &lifecycleCapturingMock{}
	router := newTaskRouter(NewTaskHandler(mock))

	target := uuid.New()
	body, _ := json.Marshal(CompleteTaskRequest{Result: "ok"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+target.String()+"/complete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "complete", mock.lastAction)
	assert.Equal(t, target, mock.lastID)
	assert.Equal(t, "ok", mock.lastResult)
}

func TestTaskHandler_Complete_EmptyBody(t *testing.T) {
	mock := &lifecycleCapturingMock{}
	router := newTaskRouter(NewTaskHandler(mock))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+uuid.New().String()+"/complete", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "complete", mock.lastAction)
	assert.Equal(t, "", mock.lastResult)
}

func TestTaskHandler_Fail_RequiresReason(t *testing.T) {
	router := newTaskRouter(NewTaskHandler(&lifecycleCapturingMock{}))

	body, _ := json.Marshal(FailTaskRequest{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+uuid.New().String()+"/fail", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTaskHandler_Fail_WithReason(t *testing.T) {
	mock := &lifecycleCapturingMock{}
	router := newTaskRouter(NewTaskHandler(mock))

	body, _ := json.Marshal(FailTaskRequest{Reason: "timeout"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+uuid.New().String()+"/fail", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "fail", mock.lastAction)
	assert.Equal(t, "timeout", mock.lastReason)
}

func TestTaskHandler_SetPriority_ValidatesRange(t *testing.T) {
	router := newTaskRouter(NewTaskHandler(&lifecycleCapturingMock{}))

	target := uuid.New().String()
	for _, bad := range []int{-1, 3, 99} {
		body, _ := json.Marshal(SetPriorityRequest{Priority: bad})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+target+"/priority", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "priority=%d should be rejected", bad)
	}
}

func TestTaskHandler_SetPriority_AcceptsValid(t *testing.T) {
	for _, good := range []int{0, 1, 2} {
		mock := &lifecycleCapturingMock{}
		router := newTaskRouter(NewTaskHandler(mock))

		body, _ := json.Marshal(SetPriorityRequest{Priority: good})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+uuid.New().String()+"/priority", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code, "priority=%d should be accepted", good)
		assert.Equal(t, good, mock.lastPrio)
	}
}

func TestTaskHandler_ListSubtasks(t *testing.T) {
	mock := &lifecycleCapturingMock{
		subtasks: []TaskResponse{
			{ID: uuid.New().String(), Title: "Sub 1", AgentName: "coder", Status: "pending", Priority: 1},
			{ID: uuid.New().String(), Title: "Sub 2", AgentName: "coder", Status: "completed", Priority: 0},
		},
	}
	router := newTaskRouter(NewTaskHandler(mock))

	parent := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+parent.String()+"/subtasks", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, parent, mock.lastID)

	var result []TaskResponse
	err := json.NewDecoder(rec.Body).Decode(&result)
	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, 1, result[0].Priority)
}

func TestTaskHandler_Create_WithPriorityAndBlockers(t *testing.T) {
	mock := &capturingCreateMock{createdID: uuid.New()}
	router := newTaskRouter(NewTaskHandler(mock))

	body, _ := json.Marshal(CreateTaskRequest{
		Title:              "Deploy",
		AgentName:          "devops",
		Priority:           2,
		AcceptanceCriteria: []string{"CI green", "Docs updated"},
		BlockedBy:          []string{"dep-1", "dep-2"},
		RequireApproval:    true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, 2, mock.capturedParams.Priority)
	assert.Equal(t, []string{"CI green", "Docs updated"}, mock.capturedParams.AcceptanceCriteria)
	assert.Equal(t, []string{"dep-1", "dep-2"}, mock.capturedParams.BlockedBy)
	assert.True(t, mock.capturedParams.RequireApproval)
}

type capturingCreateMock struct {
	lifecycleCapturingMock
	capturedParams CreateTaskRequest
	createdID      uuid.UUID
}

func (m *capturingCreateMock) CreateTask(_ context.Context, p CreateTaskRequest, _ ActorInfo) (uuid.UUID, error) {
	m.capturedParams = p
	return m.createdID, nil
}

// Tests for the isClientErr classifier.
// Errors from the manager that represent invalid user input must be reported
// to the HTTP client as 400 Bad Request, not 500 Internal Server Error.
func TestIsClientErr_classifiesUserInputErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"random server err", errors.New("disk full"), false},
		{"sqlstate 22P02 (bad uuid)", errors.New(`get task xyz: ERROR: invalid input syntax for type uuid: "xyz" (SQLSTATE 22P02)`), true},
		{"missing title", errors.New("title is required"), true},
		{"unknown blocker", errors.New("blocked_by references unknown task: id-123"), true},
		{"empty blocker id", errors.New("empty task id in blocked_by"), true},
		{"missing parent", errors.New("parent task not found: nope"), true},
		{"terminal parent", errors.New("cannot add subtask to terminal task t-1"), true},
		{"depth exceeded", errors.New("subtask depth 10 exceeds maximum 10"), true},
		{"invalid transition (sentinel)", fmt.Errorf("some context: %w", domain.ErrInvalidTransition), true},
		{"terminal task (sentinel)", fmt.Errorf("wrapped: %w", domain.ErrTaskTerminal), true},
		{"max depth (sentinel)", fmt.Errorf("wrapped: %w", domain.ErrMaxDepthExceeded), true},
		{"cyclic dep (sentinel)", fmt.Errorf("wrapped: %w", domain.ErrCyclicDependency), true},
		{"invalid priority", errors.New("invalid priority: 5"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isClientErr(tc.err))
		})
	}
}

func TestMapTaskError_NotFound(t *testing.T) {
	assert.Equal(t, http.StatusNotFound, mapTaskError(domain.ErrEngineTaskNotFound))
	assert.Equal(t, http.StatusNotFound, mapTaskError(fmt.Errorf("wrapped: %w", domain.ErrEngineTaskNotFound)))
}

func TestMapTaskError_ClientErr(t *testing.T) {
	assert.Equal(t, http.StatusBadRequest, mapTaskError(fmt.Errorf("title is required")))
}

func TestMapTaskError_ServerErr(t *testing.T) {
	assert.Equal(t, http.StatusInternalServerError, mapTaskError(fmt.Errorf("database timeout")))
}
