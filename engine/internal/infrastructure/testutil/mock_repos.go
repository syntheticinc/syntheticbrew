package testutil

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
)

// NoopAgentService implements AgentService interface for FlowHandler
type NoopAgentService struct{}

func (n *NoopAgentService) SetEnvironmentContext(projectRoot, platform string) {}
func (n *NoopAgentService) SetTestingStrategy(yamlContent string)              {}

// MockSnapshotRepo implements SnapshotRepository interface
type MockSnapshotRepo struct {
	Snapshots map[string]*domain.AgentContextSnapshot
}

func NewMockSnapshotRepo() *MockSnapshotRepo {
	return &MockSnapshotRepo{
		Snapshots: make(map[string]*domain.AgentContextSnapshot),
	}
}

func (m *MockSnapshotRepo) Save(ctx context.Context, snapshot *domain.AgentContextSnapshot) error {
	m.Snapshots[snapshot.AgentID] = snapshot
	return nil
}

func (m *MockSnapshotRepo) Load(ctx context.Context, sessionID, agentID string) (*domain.AgentContextSnapshot, error) {
	snap, exists := m.Snapshots[agentID]
	if !exists {
		return nil, nil
	}
	return snap, nil
}

func (m *MockSnapshotRepo) Delete(ctx context.Context, sessionID, agentID string) error {
	delete(m.Snapshots, agentID)
	return nil
}

func (m *MockSnapshotRepo) FindActive(ctx context.Context) ([]*domain.AgentContextSnapshot, error) {
	return nil, nil
}

// MockHistoryRepo implements MessageRepository interface
type MockHistoryRepo struct {
	Messages []*domain.Message
}

func NewMockHistoryRepo() *MockHistoryRepo {
	return &MockHistoryRepo{
		Messages: make([]*domain.Message, 0),
	}
}

func (m *MockHistoryRepo) Create(ctx context.Context, message *domain.Message) error {
	m.Messages = append(m.Messages, message)
	return nil
}

// MockEngineTaskManager implements tools.EngineTaskManager + agent.SubtaskManager for testing.
// In-memory, no persistence. Subtasks = EngineTask with ParentTaskID set.
//
// Task IDs are uuid.UUID so the mock matches the production adapter's behaviour.
type MockEngineTaskManager struct {
	Tasks map[uuid.UUID]*domain.EngineTask
	mu    sync.Mutex
}

func NewMockEngineTaskManager() *MockEngineTaskManager {
	return &MockEngineTaskManager{Tasks: make(map[uuid.UUID]*domain.EngineTask)}
}

// --- tools.EngineTaskManager ---

func (m *MockEngineTaskManager) CreateTask(ctx context.Context, params tools.CreateEngineTaskParams) (uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := uuid.New()
	status := domain.EngineTaskStatusPending
	if params.RequireApproval {
		status = domain.EngineTaskStatusDraft
	}
	task := &domain.EngineTask{
		ID:                 id,
		Title:              params.Title,
		Description:        params.Description,
		AcceptanceCriteria: params.AcceptanceCriteria,
		SessionID:          params.SessionID,
		Priority:           params.Priority,
		BlockedBy:          params.BlockedBy,
		Status:             status,
		Mode:               domain.TaskModeInteractive,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}
	m.Tasks[id] = task
	return id, nil
}

func (m *MockEngineTaskManager) UpdateTask(ctx context.Context, id uuid.UUID, title, description string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.Tasks[id]
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}
	if title != "" {
		task.Title = title
	}
	if description != "" {
		task.Description = description
	}
	task.UpdatedAt = time.Now()
	return nil
}

func (m *MockEngineTaskManager) GetTask(ctx context.Context, id uuid.UUID) (*domain.EngineTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Tasks[id], nil
}

func (m *MockEngineTaskManager) SetTaskStatus(ctx context.Context, id uuid.UUID, status string, result string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.Tasks[id]
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}
	if err := task.Transition(domain.EngineTaskStatus(status)); err != nil {
		return err
	}
	task.Result = result
	return nil
}

func (m *MockEngineTaskManager) ListTasks(ctx context.Context, sessionID string) ([]tools.EngineTaskSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []tools.EngineTaskSummary
	for _, t := range m.Tasks {
		if t.SessionID != sessionID {
			continue
		}
		var parentID *string
		if t.ParentTaskID != nil {
			s := t.ParentTaskID.String()
			parentID = &s
		}
		result = append(result, tools.EngineTaskSummary{
			ID:       t.ID.String(),
			Title:    t.Title,
			Status:   string(t.Status),
			ParentID: parentID,
			Priority: t.Priority,
		})
	}
	return result, nil
}

func (m *MockEngineTaskManager) CreateSubTask(ctx context.Context, parentID uuid.UUID, params tools.CreateEngineTaskParams) (uuid.UUID, error) {
	if parentID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("parent task not found: %s", parentID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id := uuid.New()
	status := domain.EngineTaskStatusPending
	if params.RequireApproval {
		status = domain.EngineTaskStatusDraft
	}
	task := &domain.EngineTask{
		ID:                 id,
		Title:              params.Title,
		Description:        params.Description,
		AcceptanceCriteria: params.AcceptanceCriteria,
		SessionID:          params.SessionID,
		ParentTaskID:       &parentID,
		Priority:           params.Priority,
		BlockedBy:          params.BlockedBy,
		Status:             status,
		Mode:               domain.TaskModeInteractive,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}
	m.Tasks[id] = task
	return id, nil
}

func (m *MockEngineTaskManager) ListSubtasks(ctx context.Context, parentID uuid.UUID) ([]tools.EngineTaskSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []tools.EngineTaskSummary
	for _, t := range m.Tasks {
		if t.ParentTaskID == nil || *t.ParentTaskID != parentID {
			continue
		}
		result = append(result, tools.EngineTaskSummary{ID: t.ID.String(), Title: t.Title, Status: string(t.Status)})
	}
	return result, nil
}

func (m *MockEngineTaskManager) ListReadySubtasks(ctx context.Context, parentID uuid.UUID) ([]tools.EngineTaskSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	isTerminal := func(s domain.EngineTaskStatus) bool {
		return s == domain.EngineTaskStatusCompleted ||
			s == domain.EngineTaskStatusFailed ||
			s == domain.EngineTaskStatusCancelled
	}
	var result []tools.EngineTaskSummary
	for _, t := range m.Tasks {
		if t.ParentTaskID == nil || *t.ParentTaskID != parentID {
			continue
		}
		if t.Status != domain.EngineTaskStatusPending {
			continue
		}
		// Every declared blocker must be in terminal state.
		allResolved := true
		for _, blockerID := range t.BlockedBy {
			if blockerID == uuid.Nil {
				continue
			}
			blocker, ok := m.Tasks[blockerID]
			if !ok || !isTerminal(blocker.Status) {
				allResolved = false
				break
			}
		}
		if !allResolved {
			continue
		}
		result = append(result, tools.EngineTaskSummary{ID: t.ID.String(), Title: t.Title, Status: string(t.Status)})
	}
	return result, nil
}

func (m *MockEngineTaskManager) ApproveTask(ctx context.Context, id uuid.UUID) error {
	return m.SetTaskStatus(ctx, id, string(domain.EngineTaskStatusApproved), "")
}

func (m *MockEngineTaskManager) StartTask(ctx context.Context, id uuid.UUID) error {
	return m.SetTaskStatus(ctx, id, string(domain.EngineTaskStatusInProgress), "")
}

func (m *MockEngineTaskManager) CompleteTask(ctx context.Context, id uuid.UUID, result string) error {
	return m.SetTaskStatus(ctx, id, string(domain.EngineTaskStatusCompleted), result)
}

func (m *MockEngineTaskManager) FailTask(ctx context.Context, id uuid.UUID, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.Tasks[id]
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}
	return task.Fail(reason)
}

func (m *MockEngineTaskManager) CancelTask(ctx context.Context, id uuid.UUID, reason string) error {
	return m.SetTaskStatus(ctx, id, string(domain.EngineTaskStatusCancelled), reason)
}

func (m *MockEngineTaskManager) SetTaskPriority(ctx context.Context, id uuid.UUID, priority int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.Tasks[id]
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}
	return task.SetPriority(priority)
}

func (m *MockEngineTaskManager) GetNextTask(ctx context.Context, sessionID string) (*domain.EngineTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, status := range []domain.EngineTaskStatus{
		domain.EngineTaskStatusInProgress,
		domain.EngineTaskStatusApproved,
		domain.EngineTaskStatusPending,
	} {
		for _, t := range m.Tasks {
			if t.SessionID == sessionID && t.Status == status {
				return t, nil
			}
		}
	}
	return nil, nil
}

func (m *MockEngineTaskManager) AssignTaskToAgent(ctx context.Context, id uuid.UUID, agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.Tasks[id]
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}
	// Q.5: AssignedAgentID is no longer persisted. Start the task if pending/approved.
	if task.Status == domain.EngineTaskStatusApproved || task.Status == domain.EngineTaskStatusPending {
		return task.Start()
	}
	return nil
}

func (m *MockEngineTaskManager) GetTaskByAgentID(ctx context.Context, agentID string) (*domain.EngineTask, error) {
	// Q.5: AssignedAgentID is no longer persisted — always returns nil.
	return nil, nil
}

// MockAgentRunStorage implements AgentRunStorage interface for testing
type MockAgentRunStorage struct {
	Runs map[string]*domain.AgentRun
	mu   sync.Mutex
}

func NewMockAgentRunStorage() *MockAgentRunStorage {
	return &MockAgentRunStorage{Runs: make(map[string]*domain.AgentRun)}
}

func (m *MockAgentRunStorage) Save(ctx context.Context, run *domain.AgentRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Runs[run.ID] = run
	return nil
}

func (m *MockAgentRunStorage) Update(ctx context.Context, run *domain.AgentRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Runs[run.ID] = run
	return nil
}

func (m *MockAgentRunStorage) GetByID(ctx context.Context, id string) (*domain.AgentRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Runs[id], nil
}

func (m *MockAgentRunStorage) GetRunningBySession(ctx context.Context, sessionID string) ([]*domain.AgentRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*domain.AgentRun
	for _, r := range m.Runs {
		if r.SessionID == sessionID && r.Status == domain.AgentRunRunning {
			result = append(result, r)
		}
	}
	return result, nil
}

func (m *MockAgentRunStorage) CountRunningBySession(ctx context.Context, sessionID string) (int, error) {
	runs, _ := m.GetRunningBySession(ctx, sessionID)
	return len(runs), nil
}

func (m *MockAgentRunStorage) CleanupOrphanedRuns(ctx context.Context) (int64, error) {
	return 0, nil
}

// TestFlowConfig returns programmatic FlowsConfig and PromptsConfig for testing
func TestFlowConfig() (*config.FlowsConfig, *config.PromptsConfig) {
	flowsCfg := &config.FlowsConfig{
		Flows: map[string]config.FlowDefinition{
			"supervisor": {
				Name:            "supervisor-flow",
				SystemPromptRef: "supervisor_prompt",
				Tools: []string{
					"manage_tasks",
					"read_file", "write_file", "edit_file",
					"search_code", "get_project_tree", "smart_search", "grep_search", "glob",
					"execute_command", "show_structured_output",
					"spawn_agent",
					"lsp",
				},
				MaxSteps:       10,
				MaxContextSize: 4000,
				Lifecycle: config.LifecycleConfig{
					SuspendOn: []string{},
					ReportTo:  "user",
				},
			},
			"coder": {
				Name:            "coder-flow",
				SystemPromptRef: "code_agent_prompt",
				Tools: []string{
					"read_file", "write_file", "edit_file",
					"search_code", "get_project_tree",
					"execute_command",
				},
				MaxSteps:       10,
				MaxContextSize: 4000,
				Lifecycle: config.LifecycleConfig{
					SuspendOn: []string{},
					ReportTo:  "parent_agent",
				},
			},
		},
	}

	promptsCfg := &config.PromptsConfig{
		SupervisorPrompt: "You are a test supervisor. Follow instructions exactly.",
		SystemPrompt:     "You are a helpful assistant.",
		CodeAgentPrompt:  "You are a code agent. Complete the assigned subtask.",
	}

	return flowsCfg, promptsCfg
}
