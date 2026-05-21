package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// isClientErr returns true for errors that represent invalid user input
// (bad UUID, unknown parent/blocker id, title missing, depth exceeded, terminal parent, etc.)
// so the handler can map them to 400 Bad Request instead of 500.
func isClientErr(err error) bool {
	if err == nil {
		return false
	}

	// Check sentinel errors first.
	if errors.Is(err, domain.ErrInvalidTransition) ||
		errors.Is(err, domain.ErrTaskTerminal) ||
		errors.Is(err, domain.ErrMaxDepthExceeded) ||
		errors.Is(err, domain.ErrCyclicDependency) {
		return true
	}

	// Fallback to string matching for non-sentinel errors.
	msg := err.Error()
	switch {
	case strings.Contains(msg, "SQLSTATE 22P02"),
		strings.Contains(msg, "invalid input syntax for type uuid"),
		strings.Contains(msg, "title is required"),
		strings.Contains(msg, "blocked_by references unknown task"),
		strings.Contains(msg, "empty task id"),
		strings.Contains(msg, "parent task not found"),
		strings.Contains(msg, "cannot add subtask to terminal task"),
		strings.Contains(msg, "exceeds maximum"),
		strings.Contains(msg, "invalid priority"):
		return true
	}
	return false
}

// parseTaskIDParam extracts the path "id" parameter and parses it as a UUID.
// Returns a 400-mappable error so malformed ids never leak into the service layer.
func parseTaskIDParam(r *http.Request) (uuid.UUID, error) {
	raw := chi.URLParam(r, "id")
	if raw == "" {
		return uuid.Nil, fmt.Errorf("id parameter is required")
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid task id %q: %w", raw, err)
	}
	return id, nil
}

// parseStringIDParam extracts and validates a non-empty string "id" URL parameter.
// Used by handlers whose ids are opaque strings (triggers, widgets, schemas), not UUIDs.
func parseStringIDParam(r *http.Request) (string, error) {
	raw := chi.URLParam(r, "id")
	if raw == "" {
		return "", fmt.Errorf("id parameter is required")
	}
	return raw, nil
}

// parseStringParam extracts and validates a non-empty string URL parameter by name.
func parseStringParam(r *http.Request, param string) (string, error) {
	s := chi.URLParam(r, param)
	if s == "" {
		return "", fmt.Errorf("%s is required", param)
	}
	return s, nil
}

// parseUUIDStringParam extracts a URL parameter by name and validates it is a
// well-formed UUID. Returns a 400-mappable error when the value is missing or
// not a valid UUID so malformed ids never reach the service/repo layer.
func parseUUIDStringParam(r *http.Request, param string) (string, error) {
	s := chi.URLParam(r, param)
	if s == "" {
		return "", fmt.Errorf("%s is required", param)
	}
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("invalid %s: must be a valid UUID", param)
	}
	return s, nil
}

// Input validation limits.
const (
	MaxTaskTitleLen            = 256
	MaxTaskDescriptionLen     = 8192
	MaxAcceptanceCriteria     = 64
	MaxAcceptanceCriterionLen = 512
	MaxBlockers               = 32
	MaxRequestBodySize        = 64 << 10 // 64KB
	DefaultTaskListLimit      = 100
)

// CreateTaskRequest is the body for POST /api/v1/tasks.
type CreateTaskRequest struct {
	Title              string   `json:"title"`
	Description        string   `json:"description,omitempty"`
	AgentName          string   `json:"agent_name"`
	Mode               string   `json:"mode,omitempty"` // "interactive" | "background"
	Priority           int      `json:"priority,omitempty"`            // 0=normal, 1=high, 2=critical
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"` // checklist items
	BlockedBy          []string `json:"blocked_by,omitempty"`          // task IDs that must complete first
	ParentTaskID       string   `json:"parent_task_id,omitempty"`      // if set, created as a sub-task
	RequireApproval    bool     `json:"require_approval,omitempty"`    // if true, starts as draft (awaiting Approve)
}

// TaskListFilter contains query parameters for listing tasks.
type TaskListFilter struct {
	Source       string
	AgentName    string
	Status       string
	ParentTaskID string // empty = no filter; "NONE" = top-level tasks only
	Limit        int
	Offset       int
}

// TaskResponse is a summary of a task for list responses.
// IDs are serialized as string — JSON API contract keeps uuid = canonical string form.
type TaskResponse struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	AgentName    string  `json:"agent_name"`
	Status       string  `json:"status"`
	Source       string  `json:"source"`
	Priority     int     `json:"priority"`
	ParentTaskID *string `json:"parent_task_id,omitempty"`
	CreatedAt    string  `json:"created_at"`
}

// TaskDetailResponse is the full task representation.
type TaskDetailResponse struct {
	TaskResponse
	Description        string   `json:"description,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	BlockedBy          []string `json:"blocked_by,omitempty"`
	AssignedAgentID    string   `json:"assigned_agent_id,omitempty"`
	Mode               string   `json:"mode"`
	Result             string   `json:"result,omitempty"`
	Error              string   `json:"error,omitempty"`
	StartedAt          string   `json:"started_at,omitempty"`
	ApprovedAt         string   `json:"approved_at,omitempty"`
	CompletedAt        string   `json:"completed_at,omitempty"`
}

// PaginatedTaskResponse wraps a page of tasks with pagination metadata.
type PaginatedTaskResponse struct {
	Data       []TaskResponse `json:"data"`
	Total      int64          `json:"total"`
	Page       int            `json:"page"`
	PerPage    int            `json:"per_page"`
	TotalPages int            `json:"total_pages"`
}

// CompleteTaskRequest is the body for POST /api/v1/tasks/{id}/complete.
type CompleteTaskRequest struct {
	Result string `json:"result,omitempty"`
}

// FailTaskRequest is the body for POST /api/v1/tasks/{id}/fail.
type FailTaskRequest struct {
	Reason string `json:"reason"`
}

// SetPriorityRequest is the body for POST /api/v1/tasks/{id}/priority.
type SetPriorityRequest struct {
	Priority int `json:"priority"`
}

// CancelTaskRequest is the optional body for DELETE /api/v1/tasks/{id}.
type CancelTaskRequest struct {
	Reason string `json:"reason,omitempty"`
}

// ActorInfo carries the authenticated caller's identity extracted from the HTTP context.
type ActorInfo struct {
	ID      string // subject (JWT) or token name (API key)
	Type    string // "admin" or "api_token"
	IsAdmin bool
}

// extractActor reads the auth context set by AuthMiddleware.
func extractActor(r *http.Request) ActorInfo {
	actorID, _ := r.Context().Value(ContextKeyActorID).(string)
	actorType, _ := r.Context().Value(ContextKeyActorType).(string)
	return ActorInfo{
		ID:      actorID,
		Type:    actorType,
		IsAdmin: actorType == "admin",
	}
}

// TaskService provides task CRUD + state transitions.
// Task IDs are uuid.UUID — string ids only appear at the HTTP boundary.
// Autonomous tasks (cron/webhook/API) that request user input are auto-failed
// by the executor with a clear reason; interactive tasks can only be cancelled
// if they reach needs_input.
type TaskService interface {
	CreateTask(ctx context.Context, params CreateTaskRequest, actor ActorInfo) (uuid.UUID, error)
	ListTasks(ctx context.Context, filter TaskListFilter, actor ActorInfo) ([]TaskResponse, error)
	CountTasks(ctx context.Context, filter TaskListFilter, actor ActorInfo) (int64, error)
	GetTask(ctx context.Context, id uuid.UUID, actor ActorInfo) (*TaskDetailResponse, error)
	ListSubtasks(ctx context.Context, parentID uuid.UUID, actor ActorInfo) ([]TaskResponse, error)
	CancelTask(ctx context.Context, id uuid.UUID, reason string, actor ActorInfo) error
	ApproveTask(ctx context.Context, id uuid.UUID, actor ActorInfo) error
	StartTask(ctx context.Context, id uuid.UUID, actor ActorInfo) error
	CompleteTask(ctx context.Context, id uuid.UUID, result string, actor ActorInfo) error
	FailTask(ctx context.Context, id uuid.UUID, reason string, actor ActorInfo) error
	SetTaskPriority(ctx context.Context, id uuid.UUID, priority int, actor ActorInfo) error
}

// TaskHandler serves /api/v1/tasks endpoints.
type TaskHandler struct {
	service TaskService
}

// NewTaskHandler creates a TaskHandler.
func NewTaskHandler(service TaskService) *TaskHandler {
	return &TaskHandler{service: service}
}

// Create handles POST /api/v1/tasks.
func (h *TaskHandler) Create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBodySize)
	var req CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}
	if req.Title == "" {
		writeJSONError(w, http.StatusBadRequest, "title is required")
		return
	}
	if len(req.Title) > MaxTaskTitleLen {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("title exceeds maximum length of %d", MaxTaskTitleLen))
		return
	}
	if len(req.Description) > MaxTaskDescriptionLen {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("description exceeds maximum length of %d", MaxTaskDescriptionLen))
		return
	}
	if len(req.AcceptanceCriteria) > MaxAcceptanceCriteria {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("acceptance_criteria exceeds maximum of %d items", MaxAcceptanceCriteria))
		return
	}
	for _, ac := range req.AcceptanceCriteria {
		if len(ac) > MaxAcceptanceCriterionLen {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("acceptance criterion exceeds maximum length of %d", MaxAcceptanceCriterionLen))
			return
		}
	}
	if len(req.BlockedBy) > MaxBlockers {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("blocked_by exceeds maximum of %d items", MaxBlockers))
		return
	}
	if req.AgentName == "" {
		writeJSONError(w, http.StatusBadRequest, "agent_name is required")
		return
	}

	actor := extractActor(r)
	taskID, err := h.service.CreateTask(r.Context(), req, actor)
	if err != nil {
		writeJSONError(w, mapTaskError(err), err.Error())
		return
	}

	initialStatus := "pending"
	if req.RequireApproval {
		initialStatus = "draft"
	}
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"task_id": taskID.String(),
		"status":  initialStatus,
	})
}

// List handles GET /api/v1/tasks.
// Supports pagination via ?page=N&per_page=M query parameters.
// Without pagination params, applies a default limit.
func (h *TaskHandler) List(w http.ResponseWriter, r *http.Request) {
	filter := TaskListFilter{
		Source:    r.URL.Query().Get("source"),
		AgentName: r.URL.Query().Get("agent_name"),
		Status:    r.URL.Query().Get("status"),
	}
	actor := extractActor(r)

	pageStr := r.URL.Query().Get("page")
	perPageStr := r.URL.Query().Get("per_page")

	page := 1
	perPage := DefaultTaskListLimit
	if pageStr != "" {
		if v, err := strconv.Atoi(pageStr); err == nil && v > 0 {
			page = v
		}
	}
	if perPageStr != "" {
		if v, err := strconv.Atoi(perPageStr); err == nil && v > 0 {
			if v > 100 {
				v = 100
			}
			perPage = v
		}
	}

	filter.Limit = perPage
	filter.Offset = (page - 1) * perPage

	tasks, err := h.service.ListTasks(r.Context(), filter, actor)
	if err != nil {
		writeJSONError(w, mapTaskError(err), err.Error())
		return
	}

	total, err := h.service.CountTasks(r.Context(), filter, actor)
	if err != nil {
		writeJSONError(w, mapTaskError(err), err.Error())
		return
	}

	totalPages := int(total) / perPage
	if int(total)%perPage != 0 {
		totalPages++
	}

	writeJSON(w, http.StatusOK, PaginatedTaskResponse{
		Data:       tasks,
		Total:      total,
		Page:       page,
		PerPage:    perPage,
		TotalPages: totalPages,
	})
}

// mapTaskError maps a service error to the appropriate HTTP status code.
func mapTaskError(err error) int {
	if errors.Is(err, domain.ErrEngineTaskNotFound) {
		return http.StatusNotFound
	}
	if isClientErr(err) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// Get handles GET /api/v1/tasks/{id}.
func (h *TaskHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := parseTaskIDParam(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	actor := extractActor(r)
	task, err := h.service.GetTask(r.Context(), id, actor)
	if err != nil {
		writeJSONError(w, mapTaskError(err), err.Error())
		return
	}
	if task == nil {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("task not found: %s", id))
		return
	}

	writeJSON(w, http.StatusOK, task)
}

// Cancel handles DELETE /api/v1/tasks/{id}.
// Optional body: {"reason": "..."} is stored on the cancelled task.
func (h *TaskHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	id, err := parseTaskIDParam(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req CancelTaskRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
			return
		}
	}

	actor := extractActor(r)
	if err := h.service.CancelTask(r.Context(), id, req.Reason, actor); err != nil {
		writeJSONError(w, mapTaskError(err), err.Error())
		return
	}

	w.WriteHeader(http.StatusOK)
}

// ListSubtasks handles GET /api/v1/tasks/{id}/subtasks.
func (h *TaskHandler) ListSubtasks(w http.ResponseWriter, r *http.Request) {
	id, err := parseTaskIDParam(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	actor := extractActor(r)
	subs, err := h.service.ListSubtasks(r.Context(), id, actor)
	if err != nil {
		writeJSONError(w, mapTaskError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, subs)
}

// Approve handles POST /api/v1/tasks/{id}/approve (draft -> approved).
func (h *TaskHandler) Approve(w http.ResponseWriter, r *http.Request) {
	id, err := parseTaskIDParam(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	actor := extractActor(r)
	if err := h.service.ApproveTask(r.Context(), id, actor); err != nil {
		writeJSONError(w, mapTaskError(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

// Start handles POST /api/v1/tasks/{id}/start (approved/pending -> in_progress).
func (h *TaskHandler) Start(w http.ResponseWriter, r *http.Request) {
	id, err := parseTaskIDParam(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	actor := extractActor(r)
	if err := h.service.StartTask(r.Context(), id, actor); err != nil {
		writeJSONError(w, mapTaskError(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

// Complete handles POST /api/v1/tasks/{id}/complete (in_progress -> completed).
func (h *TaskHandler) Complete(w http.ResponseWriter, r *http.Request) {
	id, err := parseTaskIDParam(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	// body is optional — allow empty body for completion without result
	var req CompleteTaskRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
			return
		}
	}
	actor := extractActor(r)
	if err := h.service.CompleteTask(r.Context(), id, req.Result, actor); err != nil {
		writeJSONError(w, mapTaskError(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

// Fail handles POST /api/v1/tasks/{id}/fail (in_progress -> failed).
func (h *TaskHandler) Fail(w http.ResponseWriter, r *http.Request) {
	id, err := parseTaskIDParam(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req FailTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}
	if req.Reason == "" {
		writeJSONError(w, http.StatusBadRequest, "reason is required")
		return
	}
	actor := extractActor(r)
	if err := h.service.FailTask(r.Context(), id, req.Reason, actor); err != nil {
		writeJSONError(w, mapTaskError(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

// SetPriority handles POST /api/v1/tasks/{id}/priority.
func (h *TaskHandler) SetPriority(w http.ResponseWriter, r *http.Request) {
	id, err := parseTaskIDParam(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req SetPriorityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}
	if req.Priority < 0 || req.Priority > 2 {
		writeJSONError(w, http.StatusBadRequest, "priority must be 0, 1, or 2")
		return
	}
	actor := extractActor(r)
	if err := h.service.SetTaskPriority(r.Context(), id, req.Priority, actor); err != nil {
		writeJSONError(w, mapTaskError(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}
