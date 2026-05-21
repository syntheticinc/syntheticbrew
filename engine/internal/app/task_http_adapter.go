package app

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	deliveryhttp "github.com/syntheticinc/syntheticbrew/internal/delivery/http"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/taskrunner"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools"
)

// sessionUserReader looks up the owner of a session by JWT sub.
type sessionUserReader interface {
	GetUserSubBySessionID(ctx context.Context, sessionID string) (string, bool, error)
}

// taskServiceHTTPAdapter bridges task infrastructure to the http.TaskService interface.
//
// It holds both the repository (for reads) and the shared EngineTaskManagerAdapter
// (for writes that require validation — depth/cycle/blocker checks) so REST API
// behaviour matches what agents see through the manage_tasks tool.
//
// All mutating operations route through the manager so that completion hooks
// (webhooks) fire consistently regardless of whether the action originated
// from an agent tool or the admin dashboard.
type taskServiceHTTPAdapter struct {
	repo          *configrepo.GORMTaskRepository
	manager       *taskrunner.EngineTaskManagerAdapter
	sessionReader sessionUserReader
}

// toHTTPTaskResponse maps a domain EngineTask to the HTTP response shape.
// UUIDs are serialized as canonical strings — the JSON API contract keeps id as string.
func toHTTPTaskResponse(t domain.EngineTask) deliveryhttp.TaskResponse {
	var parentID *string
	if t.ParentTaskID != nil {
		s := t.ParentTaskID.String()
		parentID = &s
	}
	return deliveryhttp.TaskResponse{
		ID:           t.ID.String(),
		Title:        t.Title,
		Status:       string(t.Status),
		Priority:     t.Priority,
		ParentTaskID: parentID,
		CreatedAt:    t.CreatedAt.Format(time.RFC3339),
	}
}

// blockedByStrings converts a domain []uuid.UUID into the []string used in JSON responses.
// Returns nil for empty input so omitempty works as expected.
func blockedByStrings(ids []uuid.UUID) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}

// parseBlockedByUUIDs converts the HTTP request's []string into []uuid.UUID.
// Returns a descriptive error if any id is malformed so the handler maps it to 400.
func parseBlockedByUUIDs(raw []string) ([]uuid.UUID, error) {
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

// checkOwnership verifies that a non-admin actor owns the given task via its session.
// Admin actors are allowed to operate on any task. Returns ErrEngineTaskNotFound
// if the task belongs to a different user (information hiding — don't leak existence).
func checkOwnership(ctx context.Context, task *domain.EngineTask, actor deliveryhttp.ActorInfo, sr sessionUserReader) error {
	if actor.IsAdmin {
		return nil
	}
	if task.SessionID == "" {
		return domain.ErrEngineTaskNotFound
	}
	ownerSub, ok, err := sr.GetUserSubBySessionID(ctx, task.SessionID)
	if err != nil {
		return err
	}
	if !ok || ownerSub != actor.ID {
		return domain.ErrEngineTaskNotFound
	}
	return nil
}

// getTaskWithOwnershipCheck loads a task and verifies ownership.
func (a *taskServiceHTTPAdapter) getTaskWithOwnershipCheck(ctx context.Context, id uuid.UUID, actor deliveryhttp.ActorInfo) (*domain.EngineTask, error) {
	task, err := a.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := checkOwnership(ctx, task, actor, a.sessionReader); err != nil {
		return nil, err
	}
	return task, nil
}

func (a *taskServiceHTTPAdapter) CreateTask(ctx context.Context, req deliveryhttp.CreateTaskRequest, actor deliveryhttp.ActorInfo) (uuid.UUID, error) {
	if req.Priority < 0 || req.Priority > 2 {
		return uuid.Nil, fmt.Errorf("invalid priority: %d (must be 0-2)", req.Priority)
	}
	mode := domain.TaskModeInteractive
	if req.Mode == "background" {
		mode = domain.TaskModeBackground
	}

	blockers, err := parseBlockedByUUIDs(req.BlockedBy)
	if err != nil {
		return uuid.Nil, err
	}

	params := tools.CreateEngineTaskParams{
		Title:              req.Title,
		Description:        req.Description,
		AcceptanceCriteria: req.AcceptanceCriteria,
		Priority:           req.Priority,
		BlockedBy:          blockers,
		RequireApproval:    req.RequireApproval,
		Mode:               mode,
	}

	if req.ParentTaskID != "" {
		// Goes through full validation: parent exists, not terminal, depth within limit, no cycle.
		parentID, perr := uuid.Parse(req.ParentTaskID)
		if perr != nil {
			return uuid.Nil, fmt.Errorf("parent task not found: %s", req.ParentTaskID)
		}
		return a.manager.CreateSubTask(ctx, parentID, params)
	}
	return a.manager.CreateTask(ctx, params)
}

func (a *taskServiceHTTPAdapter) buildRepoFilter(filter deliveryhttp.TaskListFilter, actor deliveryhttp.ActorInfo) configrepo.TaskFilter {
	repoFilter := configrepo.TaskFilter{
		Limit:  filter.Limit,
		Offset: filter.Offset,
	}
	if filter.Status != "" {
		st := domain.EngineTaskStatus(filter.Status)
		repoFilter.Status = &st
	}
	if filter.ParentTaskID != "" {
		// Malformed parent UUID — silently ignore the filter rather than 500.
		// The handler is list-shaped and a bad filter should just mean "no match".
		if pid, err := uuid.Parse(filter.ParentTaskID); err == nil {
			repoFilter.ParentTaskID = &pid
		}
	}
	// Non-admin actors only see tasks belonging to their sessions.
	// The UserSub filter is resolved via session ownership in the repository layer.
	if !actor.IsAdmin {
		repoFilter.UserSub = &actor.ID
	}
	return repoFilter
}

func (a *taskServiceHTTPAdapter) ListTasks(ctx context.Context, filter deliveryhttp.TaskListFilter, actor deliveryhttp.ActorInfo) ([]deliveryhttp.TaskResponse, error) {
	tasks, err := a.repo.List(ctx, a.buildRepoFilter(filter, actor))
	if err != nil {
		return nil, err
	}
	result := make([]deliveryhttp.TaskResponse, 0, len(tasks))
	for _, t := range tasks {
		result = append(result, toHTTPTaskResponse(t))
	}
	return result, nil
}

func (a *taskServiceHTTPAdapter) CountTasks(ctx context.Context, filter deliveryhttp.TaskListFilter, actor deliveryhttp.ActorInfo) (int64, error) {
	return a.repo.Count(ctx, a.buildRepoFilter(filter, actor))
}

func (a *taskServiceHTTPAdapter) GetTask(ctx context.Context, id uuid.UUID, actor deliveryhttp.ActorInfo) (*deliveryhttp.TaskDetailResponse, error) {
	t, err := a.getTaskWithOwnershipCheck(ctx, id, actor)
	if err != nil {
		return nil, err
	}
	resp := &deliveryhttp.TaskDetailResponse{
		TaskResponse:       toHTTPTaskResponse(*t),
		Description:        t.Description,
		AcceptanceCriteria: t.AcceptanceCriteria,
		BlockedBy:          blockedByStrings(t.BlockedBy),
		Mode:               string(t.Mode),
		Result:             t.Result,
		Error:              t.Error,
	}
	if t.StartedAt != nil {
		resp.StartedAt = t.StartedAt.Format(time.RFC3339)
	}
	if t.ApprovedAt != nil {
		resp.ApprovedAt = t.ApprovedAt.Format(time.RFC3339)
	}
	if t.CompletedAt != nil {
		resp.CompletedAt = t.CompletedAt.Format(time.RFC3339)
	}
	return resp, nil
}

func (a *taskServiceHTTPAdapter) ListSubtasks(ctx context.Context, parentID uuid.UUID, actor deliveryhttp.ActorInfo) ([]deliveryhttp.TaskResponse, error) {
	// Verify parent ownership first.
	if _, err := a.getTaskWithOwnershipCheck(ctx, parentID, actor); err != nil {
		return nil, err
	}
	tasks, err := a.repo.GetSubTasks(ctx, parentID)
	if err != nil {
		return nil, err
	}
	result := make([]deliveryhttp.TaskResponse, 0, len(tasks))
	for _, t := range tasks {
		result = append(result, toHTTPTaskResponse(t))
	}
	return result, nil
}

func (a *taskServiceHTTPAdapter) CancelTask(ctx context.Context, id uuid.UUID, reason string, actor deliveryhttp.ActorInfo) error {
	if _, err := a.getTaskWithOwnershipCheck(ctx, id, actor); err != nil {
		return err
	}
	return a.manager.CancelTask(ctx, id, reason)
}

func (a *taskServiceHTTPAdapter) ApproveTask(ctx context.Context, id uuid.UUID, actor deliveryhttp.ActorInfo) error {
	if _, err := a.getTaskWithOwnershipCheck(ctx, id, actor); err != nil {
		return err
	}
	return a.manager.ApproveTask(ctx, id)
}

func (a *taskServiceHTTPAdapter) StartTask(ctx context.Context, id uuid.UUID, actor deliveryhttp.ActorInfo) error {
	if _, err := a.getTaskWithOwnershipCheck(ctx, id, actor); err != nil {
		return err
	}
	return a.manager.StartTask(ctx, id)
}

func (a *taskServiceHTTPAdapter) CompleteTask(ctx context.Context, id uuid.UUID, result string, actor deliveryhttp.ActorInfo) error {
	if _, err := a.getTaskWithOwnershipCheck(ctx, id, actor); err != nil {
		return err
	}
	return a.manager.CompleteTask(ctx, id, result)
}

func (a *taskServiceHTTPAdapter) FailTask(ctx context.Context, id uuid.UUID, reason string, actor deliveryhttp.ActorInfo) error {
	if _, err := a.getTaskWithOwnershipCheck(ctx, id, actor); err != nil {
		return err
	}
	return a.manager.FailTask(ctx, id, reason)
}

func (a *taskServiceHTTPAdapter) SetTaskPriority(ctx context.Context, id uuid.UUID, priority int, actor deliveryhttp.ActorInfo) error {
	if _, err := a.getTaskWithOwnershipCheck(ctx, id, actor); err != nil {
		return err
	}
	return a.manager.SetTaskPriority(ctx, id, priority)
}
