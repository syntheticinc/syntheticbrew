package configrepo

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"gorm.io/gorm"
)

// TaskFilter holds optional criteria for listing tasks.
type TaskFilter struct {
	Status       *domain.EngineTaskStatus
	UserSub      *string
	SessionID    *string
	ParentTaskID *uuid.UUID
	Limit        int
	Offset       int
}

// GORMTaskRepository implements task persistence using GORM.
type GORMTaskRepository struct {
	db *gorm.DB
}

// NewGORMTaskRepository creates a new GORMTaskRepository.
func NewGORMTaskRepository(db *gorm.DB) *GORMTaskRepository {
	return &GORMTaskRepository{db: db}
}

// Create inserts a new task and populates the ID back into the domain entity.
// Stamps tenant from context.
func (r *GORMTaskRepository) Create(ctx context.Context, task *domain.EngineTask) error {
	m := toTaskModel(task)
	m.TenantID = tenantIDFromCtx(ctx)
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		return fmt.Errorf("create task: %w", err)
	}
	task.ID = m.ID
	return nil
}

// GetByID returns a single task by its primary key (tenant-scoped).
func (r *GORMTaskRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.EngineTask, error) {
	var m models.TaskModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		First(&m, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, domain.ErrEngineTaskNotFound
		}
		return nil, fmt.Errorf("get task %s: %w", id, err)
	}
	return toEngineTask(&m), nil
}

// List returns tasks matching the provided filter (tenant-scoped).
func (r *GORMTaskRepository) List(ctx context.Context, filter TaskFilter) ([]domain.EngineTask, error) {
	q := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Model(&models.TaskModel{})
	q = applyTaskFilter(q, filter)
	q = q.Order("created_at DESC")

	if filter.Limit > 0 {
		q = q.Limit(filter.Limit)
	}
	if filter.Offset > 0 {
		q = q.Offset(filter.Offset)
	}

	var rows []models.TaskModel
	if err := q.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}

	tasks := make([]domain.EngineTask, 0, len(rows))
	for i := range rows {
		tasks = append(tasks, *toEngineTask(&rows[i]))
	}
	return tasks, nil
}

// Count returns the total number of tasks matching the filter (ignoring Limit/Offset, tenant-scoped).
func (r *GORMTaskRepository) Count(ctx context.Context, filter TaskFilter) (int64, error) {
	q := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Model(&models.TaskModel{})
	q = applyTaskFilter(q, filter)
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count tasks: %w", err)
	}
	return count, nil
}

// UpdateStatus transitions a task to a new status and optionally sets a result string (tenant-scoped).
func (r *GORMTaskRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.EngineTaskStatus, result string) error {
	var m models.TaskModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		First(&m, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return domain.ErrEngineTaskNotFound
		}
		return fmt.Errorf("get task %s for status update: %w", id, err)
	}

	task := toEngineTask(&m)
	if err := task.Transition(status); err != nil {
		return err
	}
	task.Result = result

	updated := toTaskModel(task)
	updated.TenantID = m.TenantID // preserve tenant on Save
	if err := r.db.WithContext(ctx).Save(&updated).Error; err != nil {
		return fmt.Errorf("update task %s status: %w", id, err)
	}
	return nil
}

// Update saves all fields of the task (tenant-scoped by preserving existing tenant).
func (r *GORMTaskRepository) Update(ctx context.Context, task *domain.EngineTask) error {
	// Verify task belongs to current tenant before updating.
	var existing models.TaskModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		First(&existing, "id = ?", task.ID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return domain.ErrEngineTaskNotFound
		}
		return fmt.Errorf("find task %s for update: %w", task.ID, err)
	}

	m := toTaskModel(task)
	m.TenantID = existing.TenantID
	if err := r.db.WithContext(ctx).Save(&m).Error; err != nil {
		return fmt.Errorf("update task %s: %w", task.ID, err)
	}
	return nil
}

// GetSubTasks returns all direct children of the given parent task (tenant-scoped).
func (r *GORMTaskRepository) GetSubTasks(ctx context.Context, parentID uuid.UUID) ([]domain.EngineTask, error) {
	var rows []models.TaskModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("parent_task_id = ?", parentID).
		Order("created_at ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("get subtasks for %s: %w", parentID, err)
	}

	tasks := make([]domain.EngineTask, 0, len(rows))
	for i := range rows {
		tasks = append(tasks, *toEngineTask(&rows[i]))
	}
	return tasks, nil
}

// GetPendingBySession returns all pending tasks for the given session (tenant-scoped).
func (r *GORMTaskRepository) GetPendingBySession(ctx context.Context, sessionID string) ([]domain.EngineTask, error) {
	var rows []models.TaskModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("session_id = ? AND status = ?", sessionID, string(domain.EngineTaskStatusPending)).
		Order("created_at ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("get pending tasks for session %s: %w", sessionID, err)
	}

	tasks := make([]domain.EngineTask, 0, len(rows))
	for i := range rows {
		tasks = append(tasks, *toEngineTask(&rows[i]))
	}
	return tasks, nil
}

// MaxCancelDepth is a safety guard against runaway recursion when a cycle
// exists in parent_task_id (should be impossible via API, but may happen
// if the database was modified directly).
const MaxCancelDepth = 64

// Cancel cancels a task and all its non-terminal subtasks (cascading, tenant-scoped).
// The optional reason is stored in the result column for the root task.
func (r *GORMTaskRepository) Cancel(ctx context.Context, id uuid.UUID, reason string) error {
	tenantID := tenantIDFromCtx(ctx)
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		visited := make(map[uuid.UUID]bool)
		return cancelRecursive(tx, tenantID, id, reason, 0, visited)
	})
}

// cancelRecursive cancels a task and recursively cancels all non-terminal subtasks (tenant-scoped).
// - depth: guard against cycles (bails out at MaxCancelDepth)
// - visited: idempotency guard in case the graph is corrupt
// - reason: stored on the first task only; children get empty result
func cancelRecursive(tx *gorm.DB, tenantID string, id uuid.UUID, reason string, depth int, visited map[uuid.UUID]bool) error {
	if depth > MaxCancelDepth {
		return fmt.Errorf("cancel recursion depth exceeded (%d) for task %s — possible cycle in parent_task_id", MaxCancelDepth, id)
	}
	if visited[id] {
		return nil
	}
	visited[id] = true

	var m models.TaskModel
	if err := tx.
		Where("id = ? AND tenant_id = ?", id, tenantID).
		First(&m).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return domain.ErrEngineTaskNotFound
		}
		return fmt.Errorf("get task %s for cancel: %w", id, err)
	}

	task := toEngineTask(&m)
	if task.IsTerminal() {
		return nil
	}

	if err := task.Transition(domain.EngineTaskStatusCancelled); err != nil {
		return fmt.Errorf("transition task %s to cancelled: %w", id, err)
	}

	// Only the root of the cancel call gets the reason string; children are cancelled with no stored reason.
	if depth == 0 {
		task.Result = reason
	}

	updated := toTaskModel(task)
	updated.TenantID = tenantID
	if err := tx.Save(&updated).Error; err != nil {
		return fmt.Errorf("save cancelled task %s: %w", id, err)
	}

	// Cancel subtasks (tenant-scoped).
	var subtasks []models.TaskModel
	if err := tx.
		Where("parent_task_id = ? AND tenant_id = ?", id, tenantID).
		Find(&subtasks).Error; err != nil {
		return fmt.Errorf("get subtasks for cancel %s: %w", id, err)
	}

	for _, sub := range subtasks {
		if err := cancelRecursive(tx, tenantID, sub.ID, "", depth+1, visited); err != nil {
			return err
		}
	}

	return nil
}

// applyTaskFilter adds WHERE clauses based on non-nil filter fields.
func applyTaskFilter(q *gorm.DB, f TaskFilter) *gorm.DB {
	if f.Status != nil {
		q = q.Where("status = ?", string(*f.Status))
	}
	if f.UserSub != nil {
		q = q.Where("session_id IN (?)", q.Session(&gorm.Session{NewDB: true}).Table("sessions").Select("id").Where("user_sub = ?", *f.UserSub))
	}
	if f.SessionID != nil {
		q = q.Where("session_id = ?", *f.SessionID)
	}
	if f.ParentTaskID != nil {
		q = q.Where("parent_task_id = ?", *f.ParentTaskID)
	}
	return q
}

// GetBySession returns all tasks for the given session (used by TaskReminderProvider, tenant-scoped).
func (r *GORMTaskRepository) GetBySession(ctx context.Context, sessionID string) ([]domain.EngineTask, error) {
	var rows []models.TaskModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("session_id = ?", sessionID).
		Order("priority DESC, created_at ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("get tasks for session %s: %w", sessionID, err)
	}

	tasks := make([]domain.EngineTask, 0, len(rows))
	for i := range rows {
		tasks = append(tasks, *toEngineTask(&rows[i]))
	}
	return tasks, nil
}

// GetByStatus returns tasks for the given session and status (tenant-scoped).
func (r *GORMTaskRepository) GetByStatus(ctx context.Context, sessionID string, status domain.EngineTaskStatus) ([]domain.EngineTask, error) {
	var rows []models.TaskModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("session_id = ? AND status = ?", sessionID, string(status)).
		Order("priority DESC, created_at ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("get tasks by status %s: %w", status, err)
	}

	tasks := make([]domain.EngineTask, 0, len(rows))
	for i := range rows {
		tasks = append(tasks, *toEngineTask(&rows[i]))
	}
	return tasks, nil
}

// GetByAgentID returns the active in_progress task assigned to the given agent (tenant-scoped).
func (r *GORMTaskRepository) GetByAgentID(ctx context.Context, agentID string) (*domain.EngineTask, error) {
	var m models.TaskModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("assigned_agent_id = ? AND status = ?", agentID, string(domain.EngineTaskStatusInProgress)).
		First(&m).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("get task by agent %s: %w", agentID, err)
	}
	return toEngineTask(&m), nil
}

// GetReadySubtasks returns pending subtasks of parentID whose blockers
// (declared in BlockedBy) have all reached terminal state (completed/failed/cancelled).
// A task with no blockers is always ready. A task with at least one non-terminal blocker is NOT ready.
// Tenant-scoped.
func (r *GORMTaskRepository) GetReadySubtasks(ctx context.Context, parentID uuid.UUID) ([]domain.EngineTask, error) {
	var rows []models.TaskModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("parent_task_id = ? AND status = ?", parentID, string(domain.EngineTaskStatusPending)).
		Order("priority DESC, created_at ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("get ready subtasks for %s: %w", parentID, err)
	}
	if len(rows) == 0 {
		return []domain.EngineTask{}, nil
	}

	// Collect all distinct blocker IDs declared across pending subtasks.
	blockerSet := make(map[uuid.UUID]struct{})
	tasks := make([]domain.EngineTask, 0, len(rows))
	for i := range rows {
		t := toEngineTask(&rows[i])
		tasks = append(tasks, *t)
		for _, b := range t.BlockedBy {
			if b == uuid.Nil {
				continue
			}
			blockerSet[b] = struct{}{}
		}
	}

	// If no blockers declared — all pending subtasks are ready.
	if len(blockerSet) == 0 {
		return tasks, nil
	}

	// Fetch terminal status for all blockers in one query (tenant-scoped).
	blockerIDs := make([]uuid.UUID, 0, len(blockerSet))
	for id := range blockerSet {
		blockerIDs = append(blockerIDs, id)
	}
	terminalStatuses := []string{
		string(domain.EngineTaskStatusCompleted),
		string(domain.EngineTaskStatusFailed),
		string(domain.EngineTaskStatusCancelled),
	}
	var terminalBlockers []models.TaskModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Select("id").
		Where("id IN ? AND status IN ?", blockerIDs, terminalStatuses).
		Find(&terminalBlockers).Error; err != nil {
		return nil, fmt.Errorf("check blocker statuses: %w", err)
	}
	terminalSet := make(map[uuid.UUID]struct{}, len(terminalBlockers))
	for _, b := range terminalBlockers {
		terminalSet[b.ID] = struct{}{}
	}

	// A subtask is ready iff every blocker it declares is in terminalSet.
	ready := make([]domain.EngineTask, 0, len(tasks))
	for _, t := range tasks {
		allResolved := true
		for _, blockerID := range t.BlockedBy {
			if blockerID == uuid.Nil {
				continue
			}
			if _, ok := terminalSet[blockerID]; !ok {
				allResolved = false
				break
			}
		}
		if allResolved {
			ready = append(ready, t)
		}
	}
	return ready, nil
}

// toTaskModel maps a domain EngineTask to a GORM TaskModel.
// Q.5: agent_name, source, source_id, assigned_agent_id, depth dropped.
// Tenant is applied by the caller (Create stamps from ctx; Update/UpdateStatus
// preserve the existing row's tenant).
func toTaskModel(t *domain.EngineTask) models.TaskModel {
	return models.TaskModel{
		ID:                 t.ID,
		Title:              t.Title,
		Description:        t.Description,
		AcceptanceCriteria: marshalStringSlice(t.AcceptanceCriteria),
		SessionID:          t.SessionID,
		ParentTaskID:       t.ParentTaskID,
		Status:             string(t.Status),
		Mode:               string(t.Mode),
		Priority:           t.Priority,
		BlockedBy:          marshalUUIDSlice(t.BlockedBy),
		Result:             t.Result,
		Error:              t.Error,
		CreatedAt:          t.CreatedAt,
		UpdatedAt:          t.UpdatedAt,
		ApprovedAt:         t.ApprovedAt,
		StartedAt:          t.StartedAt,
		CompletedAt:        t.CompletedAt,
	}
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func marshalStringSlice(s []string) string {
	if len(s) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(s)
	return string(b)
}

func unmarshalStringSlice(s string) []string {
	if s == "" || s == "[]" {
		return nil
	}
	var result []string
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		slog.WarnContext(context.Background(), "unmarshal string slice failed", "error", err, "raw", s)
		return nil
	}
	return result
}

// marshalUUIDSlice serializes a slice of UUIDs into the JSON array string stored in DB.
// uuid.UUID implements MarshalJSON (RFC 4122 string form) so encoding/json handles it.
func marshalUUIDSlice(s []uuid.UUID) string {
	if len(s) == 0 {
		return "[]"
	}
	b, err := json.Marshal(s)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// unmarshalUUIDSlice parses the stored JSON array string into a UUID slice.
// Invalid or empty input returns nil — callers treat nil the same as empty.
func unmarshalUUIDSlice(s string) []uuid.UUID {
	if s == "" || s == "[]" {
		return nil
	}
	var result []uuid.UUID
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		slog.WarnContext(context.Background(), "unmarshal UUID slice failed", "error", err, "raw", s)
		return nil
	}
	return result
}

// toEngineTask maps a GORM TaskModel to a domain EngineTask.
// Q.5: agent_name, source, source_id, assigned_agent_id, depth dropped.
func toEngineTask(m *models.TaskModel) *domain.EngineTask {
	return &domain.EngineTask{
		ID:                 m.ID,
		Title:              m.Title,
		Description:        m.Description,
		AcceptanceCriteria: unmarshalStringSlice(m.AcceptanceCriteria),
		SessionID:          m.SessionID,
		ParentTaskID:       m.ParentTaskID,
		Status:             domain.EngineTaskStatus(m.Status),
		Mode:               domain.TaskMode(m.Mode),
		Priority:           m.Priority,
		BlockedBy:          unmarshalUUIDSlice(m.BlockedBy),
		Result:             m.Result,
		Error:              m.Error,
		CreatedAt:          m.CreatedAt,
		UpdatedAt:          m.UpdatedAt,
		ApprovedAt:         m.ApprovedAt,
		StartedAt:          m.StartedAt,
		CompletedAt:        m.CompletedAt,
	}
}
