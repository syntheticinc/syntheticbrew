package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/google/uuid"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agents/react"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools"
	"github.com/syntheticinc/syntheticbrew/internal/service/engine"
	"github.com/syntheticinc/syntheticbrew/internal/service/orchestrator"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
)

// WaitResult describes the result of waiting for session agents
type WaitResult struct {
	AllDone              bool                           // true if all agents completed
	Interrupted          bool                           // true if interrupted by user message
	IsInterruptResponder bool                           // true = this call should return full INTERRUPT
	UserMessage          string                         // user message that caused interrupt
	StillRunning         []string                       // agent IDs still running
	Results              map[string]AgentCompletionInfo // completed agents
}

// AgentCompletionInfo holds completion info for an agent
type AgentCompletionInfo struct {
	AgentID   string
	SubtaskID string
	Status    string
	Result    string
	Error     string
}

// AgentSnapshot is a safe, immutable copy of agent state.
// Returned by GetStatus/GetAllAgents — safe to read without holding the pool mutex.
type AgentSnapshot struct {
	ID            string
	SubtaskID     string
	SubtaskTitle  string
	SessionID     string
	ProjectKey    string
	Status        string // "running" | "completed" | "failed" | "stopped"
	Result        string
	Error         string
	StartedAt     time.Time
	BlockingSpawn bool
}

// RunningAgent represents a Code Agent goroutine (internal, mutable — never expose outside pool)
type RunningAgent struct {
	ID            string
	SubtaskID     string
	SubtaskTitle  string // title of the subtask (for UI events)
	SessionID     string
	ProjectKey    string
	Status        string // "running" | "completed" | "failed" | "stopped"
	Result        string
	Error         string
	StartedAt     time.Time
	Cancel        context.CancelFunc
	completionCh  chan struct{} // closed when agent reaches terminal state
	closeOnce     sync.Once
	blockingSpawn bool   // true = supervisor blocks on this agent
	agentType     string // agent type name (for restart without subtask)
	description   string // task description for researcher/reviewer agents
}

// snapshot returns an immutable copy of the agent state.
func (a *RunningAgent) snapshot() AgentSnapshot {
	return AgentSnapshot{
		ID:            a.ID,
		SubtaskID:     a.SubtaskID,
		SubtaskTitle:  a.SubtaskTitle,
		SessionID:     a.SessionID,
		ProjectKey:    a.ProjectKey,
		Status:        a.Status,
		Result:        a.Result,
		Error:         a.Error,
		StartedAt:     a.StartedAt,
		BlockingSpawn: a.blockingSpawn,
	}
}

// toCompletionInfo converts an AgentSnapshot to AgentCompletionInfo.
func (s AgentSnapshot) toCompletionInfo() AgentCompletionInfo {
	return AgentCompletionInfo{
		AgentID:   s.ID,
		SubtaskID: s.SubtaskID,
		Status:    s.Status,
		Result:    s.Result,
		Error:     s.Error,
	}
}

// signalCompletion signals that the agent has reached a terminal state.
// Safe to call multiple times (uses sync.Once). Nil-safe if completionCh not initialized.
func (a *RunningAgent) signalCompletion() {
	if a.completionCh == nil {
		return
	}
	a.closeOnce.Do(func() {
		close(a.completionCh)
	})
}

// SubtaskManager defines operations needed by AgentPool for task management (consumer-side).
// Operates on EngineTask (subtasks are EngineTask with ParentTaskID set).
//
// Task IDs are uuid.UUID — the Spawn/Restart paths parse the agent-supplied string
// at the JSON boundary and propagate uuid.UUID through the rest of the pool.
type SubtaskManager interface {
	AssignTaskToAgent(ctx context.Context, taskID uuid.UUID, agentID string) error
	CompleteTask(ctx context.Context, taskID uuid.UUID, result string) error
	FailTask(ctx context.Context, taskID uuid.UUID, reason string) error
	GetTask(ctx context.Context, taskID uuid.UUID) (*domain.EngineTask, error)
}

// AgentEngine executes agents with persistence (consumer-side interface for Engine integration)
type AgentEngine interface {
	Execute(ctx context.Context, cfg engine.ExecutionConfig) (*engine.ExecutionResult, error)
}

// FlowProvider provides flow configurations (consumer-side)
type FlowProvider interface {
	GetFlow(ctx context.Context, agentName string) (*domain.Flow, error)
}

// ToolResolver resolves tool names to tool instances (consumer-side)
type ToolResolver interface {
	Resolve(ctx context.Context, toolNames []string, deps tools.ToolDependencies) ([]einotool.InvokableTool, error)
}

// ToolDepsProvider creates tool dependencies (consumer-side)
type ToolDepsProvider interface {
	GetDependencies(sessionID, projectKey string) tools.ToolDependencies
}

// AgentRunStorage defines operations needed by AgentPool for agent run persistence (consumer-side)
type AgentRunStorage interface {
	Save(ctx context.Context, run *domain.AgentRun) error
	Update(ctx context.Context, run *domain.AgentRun) error
	GetByID(ctx context.Context, id string) (*domain.AgentRun, error)
	GetRunningBySession(ctx context.Context, sessionID string) ([]*domain.AgentRun, error)
	CountRunningBySession(ctx context.Context, sessionID string) (int, error)
	CleanupOrphanedRuns(ctx context.Context) (int64, error)
}

// AgentModelSelector selects a ChatModel based on agent name (consumer-side).
// Allows different agents to use different LLM models.
type AgentModelSelector interface {
	Select(agentName string) model.ToolCallingChatModel
	ModelName(agentName string) string
}

// AgentModelIDResolver resolves the model ID configured for a named agent (consumer-side).
// Returns nil when no per-agent model is configured.
// Context is required so multi-tenant registries can dispatch per tenant.
type AgentModelIDResolver interface {
	ResolveModelID(ctx context.Context, agentName string) *string
}

// AgentModelCacheProvider fetches a cached LLM client by model ID (consumer-side).
type AgentModelCacheProvider interface {
	Get(ctx context.Context, modelID string) (model.ToolCallingChatModel, string, error)
}

// AgentPoolConfig holds configuration for creating an AgentPool
type AgentPoolConfig struct {
	ModelSelector   AgentModelSelector
	ModelIDResolver AgentModelIDResolver    // optional: per-agent model resolution from DB
	ModelCache      AgentModelCacheProvider // optional: paired with ModelIDResolver
	SubtaskManager  SubtaskManager
	AgentRunStorage AgentRunStorage // optional: nil for backward compatibility
	AgentConfig     *config.AgentConfig
	SessionDirName  string // shared session dir from Supervisor for log co-location
	MaxConcurrent   int    // 0 = no limit (backward compatibility)
}

// AgentPool manages Code Agent goroutines
type AgentPool struct {
	agents                map[string]*RunningAgent
	mu                    sync.RWMutex
	modelSelector         AgentModelSelector
	modelIDResolver       AgentModelIDResolver
	modelCache            AgentModelCacheProvider
	sessionProxies        map[string]ClientOperationsProxy
	subtaskManager        SubtaskManager
	agentRunStorage       AgentRunStorage // optional: nil for backward compatibility
	agentConfig           *config.AgentConfig
	sessionEventCallbacks map[string]func(event *domain.AgentEvent) error
	eventBus              *orchestrator.SessionEventBus // for publishing events to Orchestrator
	sessionDirName        string                        // shared session dir from Supervisor for log co-location
	contextReminders      []react.ContextReminderProvider
	// Engine support (required for code agent execution)
	engine       AgentEngine      // Engine for executing code agents
	flowProvider FlowProvider     // for getting coder flow
	toolResolver ToolResolver     // for resolving tool names
	toolDeps     ToolDepsProvider // for creating tool dependencies
	// Max concurrent agents (0 = no limit)
	maxConcurrent int
	// Interrupt mechanism for blocking spawns (delegated to InterruptManager)
	interrupt *InterruptManager
	// Session-scoped contexts: spawned agents derive from these (not from supervisor's turn context).
	// This decouples agent lifecycle from supervisor restarts — agents survive turn cancellation.
	sessionContexts map[string]context.Context
	sessionCancels  map[string]context.CancelFunc
}

// NewAgentPool creates a new AgentPool
func NewAgentPool(cfg AgentPoolConfig) *AgentPool {
	return &AgentPool{
		agents:                make(map[string]*RunningAgent),
		modelSelector:         cfg.ModelSelector,
		modelIDResolver:       cfg.ModelIDResolver,
		modelCache:            cfg.ModelCache,
		sessionProxies:        make(map[string]ClientOperationsProxy),
		subtaskManager:        cfg.SubtaskManager,
		agentRunStorage:       cfg.AgentRunStorage,
		agentConfig:           cfg.AgentConfig,
		sessionEventCallbacks: make(map[string]func(event *domain.AgentEvent) error),
		sessionDirName:        cfg.SessionDirName,
		maxConcurrent:         cfg.MaxConcurrent,
		interrupt:             NewInterruptManager(),
		sessionContexts:       make(map[string]context.Context),
		sessionCancels:        make(map[string]context.CancelFunc),
	}
}

// getOrCreateSessionCtx returns a session-scoped context for spawning agents.
// Agents derive from this context instead of the supervisor's tool call context,
// so they survive supervisor turn cancellation.
// When sourceCtx is provided, context values (e.g. RequestContext with forwarded
// headers) are preserved via context.WithoutCancel so sub-agents can forward
// authorization headers to MCP servers.
func (p *AgentPool) getOrCreateSessionCtx(sessionID string, sourceCtx ...context.Context) context.Context {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ctx, ok := p.sessionContexts[sessionID]; ok {
		return ctx
	}
	var base context.Context = context.Background()
	if len(sourceCtx) > 0 && sourceCtx[0] != nil {
		base = context.WithoutCancel(sourceCtx[0])
	}
	ctx, cancel := context.WithCancel(base)
	p.sessionContexts[sessionID] = ctx
	p.sessionCancels[sessionID] = cancel
	return ctx
}

// SetSessionDirName sets the session directory name (from Supervisor's logger)
func (p *AgentPool) SetSessionDirName(dirName string) {
	p.mu.Lock()
	p.sessionDirName = dirName
	p.mu.Unlock()
}

// SetMaxConcurrent sets the maximum number of concurrent agents (0 = no limit)
func (p *AgentPool) SetMaxConcurrent(n int) {
	p.mu.Lock()
	p.maxConcurrent = n
	p.mu.Unlock()
}

// SetContextReminders sets context reminders to be passed to Code Agents.
// Called when environment context becomes available (e.g., after client connects).
func (p *AgentPool) SetContextReminders(reminders []react.ContextReminderProvider) {
	p.mu.Lock()
	p.contextReminders = reminders
	p.mu.Unlock()
}

// SetEngine sets the Engine and related dependencies for new execution path
func (p *AgentPool) SetEngine(engine AgentEngine, flowProvider FlowProvider, toolResolver ToolResolver, toolDeps ToolDepsProvider, modelCache AgentModelCacheProvider, modelIDResolver AgentModelIDResolver) {
	p.mu.Lock()
	p.engine = engine
	p.flowProvider = flowProvider
	p.toolResolver = toolResolver
	p.toolDeps = toolDeps
	if modelCache != nil {
		p.modelCache = modelCache
	}
	if modelIDResolver != nil {
		p.modelIDResolver = modelIDResolver
	}
	p.mu.Unlock()
}

// SetModelResolver wires per-agent DB model resolution into the pool.
// When set, spawned agents will use their configured model_id from the DB
// instead of falling back to the static ModelSelector default.
func (p *AgentPool) SetModelResolver(resolver AgentModelIDResolver, cache AgentModelCacheProvider) {
	p.mu.Lock()
	p.modelIDResolver = resolver
	p.modelCache = cache
	p.mu.Unlock()
}

// SetEventBus connects the pool to the Orchestrator's event bus.
// Events (AgentCompleted, AgentFailed) will be published to wake up the Supervisor.
func (p *AgentPool) SetEventBus(bus *orchestrator.SessionEventBus) {
	p.mu.Lock()
	p.eventBus = bus
	p.mu.Unlock()
}

// Spawn starts a Code Agent in a goroutine for the given subtask.
// Returns agentID immediately (async).
// blocking: if true, supervisor will block on WaitForAllSessionAgents until this agent completes
//
// subtaskID is received from the agent (JSON string); parsed into uuid.UUID at this
// boundary and propagated through the subtask manager.
func (p *AgentPool) Spawn(ctx context.Context, sessionID, projectKey, subtaskID string, blocking bool) (string, error) {
	agentID := "code-agent-" + uuid.New().String()[:8]

	subtaskUUID, err := uuid.Parse(subtaskID)
	if err != nil {
		return "", fmt.Errorf("invalid subtask id %q: %w", subtaskID, err)
	}

	// Get task details (subtask is EngineTask with ParentTaskID set)
	subtask, err := p.subtaskManager.GetTask(ctx, subtaskUUID)
	if err != nil {
		return "", fmt.Errorf("get task: %w", err)
	}
	if subtask == nil {
		return "", fmt.Errorf("task not found: %s", subtaskID)
	}

	// Assign task to agent (also transitions to in_progress)
	if err := p.subtaskManager.AssignTaskToAgent(ctx, subtaskUUID, agentID); err != nil {
		return "", fmt.Errorf("assign task: %w", err)
	}

	// Create agent context from session-scoped context (NOT from supervisor's turn context).
	// This ensures agents survive supervisor turn cancellation.
	// Pass ctx so RequestContext (forwarded headers) propagates to spawned agents.
	sessionCtx := p.getOrCreateSessionCtx(sessionID, ctx)
	agentCtx, cancel := context.WithCancel(sessionCtx)
	agentCtx = domain.WithAgentID(agentCtx, agentID)

	running := &RunningAgent{
		ID:            agentID,
		SubtaskID:     subtaskID,
		SubtaskTitle:  subtask.Title,
		SessionID:     sessionID,
		ProjectKey:    projectKey,
		Status:        "running",
		StartedAt:     time.Now(),
		Cancel:        cancel,
		completionCh:  make(chan struct{}),
		blockingSpawn: blocking,
		agentType:     "coder",
	}

	// Register agent under lock (isolated critical section with defer)
	if err := p.registerAgent(ctx, sessionID, agentID, running); err != nil {
		return "", err
	}

	// Emit spawned event with full task input (same as what code agent receives).
	// Client uses first line for UI labels, full content for [Task] message.
	p.emitEventForSession(sessionID, &domain.AgentEvent{
		Type:      domain.EventTypeAgentSpawned,
		Timestamp: time.Now(),
		AgentID:   agentID,
		Content:   buildCodeAgentInput(subtask),
		Metadata: map[string]interface{}{
			"subtask_id":    subtaskID,
			"subtask_title": subtask.Title,
		},
	})

	// Launch goroutine
	go func() {
		defer cancel() // Release context resources on goroutine exit
		defer func() {
			if r := recover(); r != nil {
				slog.ErrorContext(context.Background(), "[AgentPool] Code Agent panicked",
					"agent_id", agentID,
					"subtask_id", subtaskID,
					"panic", r)
				p.markFailed(agentID, subtaskID, fmt.Sprintf("panic: %v", r))
			}
		}()

		slog.InfoContext(agentCtx, "[AgentPool] Code Agent starting",
			"agent_id", agentID,
			"subtask_id", subtaskID)

		// Execute code agent via Engine (always)
		result, err := p.runCodeAgentWithEngine(agentCtx, sessionID, projectKey, agentID, subtask)

		if err != nil {
			slog.ErrorContext(agentCtx, "[AgentPool] Code Agent failed",
				"agent_id", agentID,
				"subtask_id", subtaskID,
				"error", err)
			p.markFailed(agentID, subtaskID, err.Error())
			return
		}

		slog.InfoContext(agentCtx, "[AgentPool] Code Agent completed",
			"agent_id", agentID,
			"subtask_id", subtaskID)
		p.markCompleted(agentID, subtaskID, result)
	}()

	slog.InfoContext(ctx, "[AgentPool] Code Agent spawned",
		"agent_id", agentID,
		"subtask_id", subtaskID)

	return agentID, nil
}

// registerAgent adds the agent to the pool under mutex.
// Isolated critical section: uses defer p.mu.Unlock() so unlock can't be missed.
func (p *AgentPool) registerAgent(ctx context.Context, sessionID, agentID string, running *RunningAgent) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check max concurrent limit
	if p.maxConcurrent > 0 && p.agentRunStorage != nil {
		count, err := p.agentRunStorage.CountRunningBySession(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("count running agents: %w", err)
		}
		if count >= p.maxConcurrent {
			return fmt.Errorf("max concurrent agents reached (%d/%d): wait for running agents to complete before spawning new ones", count, p.maxConcurrent)
		}
	}

	p.agents[agentID] = running

	// Save agent run to DB (if storage available)
	if p.agentRunStorage != nil {
		agentRun, err := domain.NewAgentRun(agentID, running.agentType, sessionID)
		if err != nil {
			return fmt.Errorf("create agent run: %w", err)
		}
		if err := p.agentRunStorage.Save(ctx, agentRun); err != nil {
			slog.ErrorContext(ctx, "[AgentPool] failed to save agent run", "agent_id", agentID, "error", err)
			// Non-fatal: continue without DB persistence
		}
	}

	return nil
}

// GetStatus returns an immutable snapshot of agent state (safe to read without
// lock). sessionID scopes the lookup: an agent spawned in another session (and
// therefore, in Cloud, potentially another tenant) is reported as not-found so a
// caller can never inspect agents outside its own session.
func (p *AgentPool) GetStatus(sessionID, agentID string) (AgentSnapshot, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	agent, ok := p.agents[agentID]
	if !ok || agent.SessionID != sessionID {
		return AgentSnapshot{}, false
	}
	return agent.snapshot(), true
}

// GetSessionAgents returns immutable snapshots of the agents spawned in the given
// session only. The pool is process-global across tenants, so listing must be
// session-scoped — otherwise a delegating agent could enumerate other tenants'
// running agents (cloud-first isolation).
func (p *AgentPool) GetSessionAgents(sessionID string) []AgentSnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]AgentSnapshot, 0, len(p.agents))
	for _, agent := range p.agents {
		if agent.SessionID != sessionID {
			continue
		}
		result = append(result, agent.snapshot())
	}
	return result
}

// StopAgent stops a running agent by cancelling its context. sessionID scopes the
// operation: an agent belonging to another session (potentially another tenant in
// Cloud) is reported as not-found and left untouched, so a delegating agent can
// only stop the agents it spawned in its own session.
func (p *AgentPool) StopAgent(sessionID, agentID string) error {
	p.mu.Lock()
	agent, ok := p.agents[agentID]
	if !ok || agent.SessionID != sessionID {
		p.mu.Unlock()
		return fmt.Errorf("agent not found: %s", agentID)
	}
	if agent.Status != "running" {
		status := agent.Status // capture before unlock
		p.mu.Unlock()
		return fmt.Errorf("agent is not running: %s (status: %s)", agentID, status)
	}
	agent.Status = "stopped"
	agent.signalCompletion()
	agent.Cancel()
	agentRunStorage := p.agentRunStorage
	p.mu.Unlock()

	slog.InfoContext(context.Background(), "[AgentPool] agent stopped", "agent_id", agentID)

	// Update agent run in DB (if storage available)
	if agentRunStorage != nil {
		go func() {
			ctx := context.Background()
			run, err := agentRunStorage.GetByID(ctx, agentID)
			if err != nil {
				slog.ErrorContext(context.Background(), "[AgentPool] failed to get agent run for stop", "agent_id", agentID, "error", err)
				return
			}
			if run != nil {
				run.Stop()
				if err := agentRunStorage.Update(ctx, run); err != nil {
					slog.ErrorContext(context.Background(), "[AgentPool] failed to update stopped agent run", "agent_id", agentID, "error", err)
				}
			}
		}()
	}

	return nil
}

// RestartAgent restarts a failed/stopped agent on the same subtask or description
func (p *AgentPool) RestartAgent(ctx context.Context, agentID string, blocking bool) (string, error) {
	p.mu.RLock()
	agent, ok := p.agents[agentID]
	if !ok {
		p.mu.RUnlock()
		return "", fmt.Errorf("agent not found: %s", agentID)
	}
	if agent.Status == "running" {
		p.mu.RUnlock()
		return "", fmt.Errorf("agent is still running: %s", agentID)
	}
	subtaskID := agent.SubtaskID
	sessionID := agent.SessionID
	agentType := agent.agentType
	description := agent.description
	p.mu.RUnlock()

	// researcher/reviewer: restart via SpawnWithDescription
	if subtaskID == "" {
		return p.SpawnWithDescription(ctx, sessionID, "", agentType, description, blocking)
	}

	// coder: restart via Spawn (existing logic)
	subtaskUUID, err := uuid.Parse(subtaskID)
	if err != nil {
		return "", fmt.Errorf("invalid subtask id %q: %w", subtaskID, err)
	}
	subtask, err := p.subtaskManager.GetTask(ctx, subtaskUUID)
	if err != nil {
		return "", fmt.Errorf("get task for restart: %w", err)
	}
	if subtask == nil {
		return "", fmt.Errorf("task not found for restart: %s", subtaskID)
	}

	// Use project key from running agent record (EngineTask has no Context map).
	projectKey := ""
	p.mu.RLock()
	if a, ok := p.agents[agentID]; ok {
		projectKey = a.ProjectKey
	}
	p.mu.RUnlock()

	return p.Spawn(ctx, sessionID, projectKey, subtaskID, blocking)
}

// SpawnWithDescription starts an agent (researcher/reviewer) with a text description instead of subtask.
// Returns agentID immediately (async).
func (p *AgentPool) SpawnWithDescription(ctx context.Context, sessionID, projectKey string, agentType string, description string, blocking bool) (string, error) {
	prefix := agentType
	agentID := prefix + "-" + uuid.New().String()[:8]

	// Create agent context from session-scoped context (NOT from supervisor's turn context).
	// Pass ctx so RequestContext (forwarded headers) propagates to spawned agents.
	sessionCtx := p.getOrCreateSessionCtx(sessionID, ctx)
	agentCtx, cancel := context.WithCancel(sessionCtx)
	agentCtx = domain.WithAgentID(agentCtx, agentID)

	title := firstLine(description, 50)

	running := &RunningAgent{
		ID:            agentID,
		SubtaskID:     "",
		SubtaskTitle:  title,
		SessionID:     sessionID,
		ProjectKey:    projectKey,
		Status:        "running",
		StartedAt:     time.Now(),
		Cancel:        cancel,
		completionCh:  make(chan struct{}),
		blockingSpawn: blocking,
		agentType:     agentType,
		description:   description,
	}

	if err := p.registerAgent(ctx, sessionID, agentID, running); err != nil {
		cancel()
		return "", err
	}

	p.emitEventForSession(sessionID, &domain.AgentEvent{
		Type:      domain.EventTypeAgentSpawned,
		Timestamp: time.Now(),
		AgentID:   agentID,
		Content:   description,
	})

	go func() {
		defer cancel() // Release context resources on goroutine exit
		defer func() {
			if r := recover(); r != nil {
				slog.ErrorContext(context.Background(), "[AgentPool] Agent panicked",
					"agent_id", agentID,
					"agent_type", agentType,
					"panic", r)
				p.markFailed(agentID, "", fmt.Sprintf("panic: %v", r))
			}
		}()

		slog.InfoContext(agentCtx, "[AgentPool] Agent starting",
			"agent_id", agentID,
			"agent_type", agentType)

		result, err := p.runAgentWithEngine(agentCtx, sessionID, projectKey, agentID, agentType, "", description)
		if err != nil {
			slog.ErrorContext(agentCtx, "[AgentPool] Agent failed",
				"agent_id", agentID,
				"agent_type", agentType,
				"error", err)
			p.markFailed(agentID, "", err.Error())
			return
		}

		slog.InfoContext(agentCtx, "[AgentPool] Agent completed",
			"agent_id", agentID,
			"agent_type", agentType)
		p.markCompleted(agentID, "", result)
	}()

	slog.InfoContext(ctx, "[AgentPool] Agent spawned",
		"agent_id", agentID,
		"agent_type", agentType)

	return agentID, nil
}

// firstLine returns the first line of text, truncated to maxLen runes
func firstLine(text string, maxLen int) string {
	line := text
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		line = text[:idx]
	}
	runes := []rune(line)
	if len(runes) > maxLen {
		return string(runes[:maxLen]) + "..."
	}
	return line
}

// SetProxyForSession sets the proxy for a specific session.
// Accepts interface{} to allow passing proxy from different packages.
func (p *AgentPool) SetProxyForSession(sessionID string, proxy interface{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cp, ok := proxy.(ClientOperationsProxy); ok {
		p.sessionProxies[sessionID] = cp
	}
}

// SetEventCallbackForSession sets the event callback for a specific session
func (p *AgentPool) SetEventCallbackForSession(sessionID string, cb func(event *domain.AgentEvent) error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sessionEventCallbacks[sessionID] = cb
}

// RemoveSession removes proxy, event callback, and finished agents for a session (cleanup).
// Also cancels the session-scoped context, which stops all running agents.
func (p *AgentPool) RemoveSession(sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.sessionProxies, sessionID)
	delete(p.sessionEventCallbacks, sessionID)

	// Cancel session-scoped context (stops all running agents)
	if cancel, ok := p.sessionCancels[sessionID]; ok {
		cancel()
		delete(p.sessionContexts, sessionID)
		delete(p.sessionCancels, sessionID)
	}

	// Clean up agents belonging to this session
	for agentID, agent := range p.agents {
		if agent.SessionID == sessionID {
			if agent.Status == "running" {
				agent.signalCompletion()
				agent.Cancel()
			}
			delete(p.agents, agentID)
		}
	}
}

// CancelRunningAgents cancels all running agents for a session (e.g. user pressed Esc).
// Unlike RemoveSession, this does NOT remove the session context or cleanup maps —
// the session stays alive for the next user message.
func (p *AgentPool) CancelRunningAgents(sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, agent := range p.agents {
		if agent.SessionID == sessionID && agent.Status == "running" {
			agent.Status = "stopped"
			agent.signalCompletion()
			agent.Cancel()
			slog.InfoContext(context.Background(), "[AgentPool] cancelled agent (user cancel)", "agent_id", agent.ID)
		}
	}
}

// NotifyUserMessage broadcasts interrupt to ALL blocking spawns for session.
// Delegates to InterruptManager.
func (p *AgentPool) NotifyUserMessage(sessionID, message string) {
	p.interrupt.NotifyUserMessage(sessionID, message)
}

// HasBlockingWait returns true if any blocking spawn is active for session.
// Delegates to InterruptManager.
func (p *AgentPool) HasBlockingWait(sessionID string) bool {
	return p.interrupt.HasBlockingWait(sessionID)
}

// WaitForAllSessionAgents waits for ALL running agents in session to complete.
// Returns immediately if no running agents.
// If interrupted by user message, IsInterruptResponder indicates if this call should handle the interrupt.
//
// Uses session-scoped context (not the passed ctx) for cancellation detection.
// This means the wait survives supervisor turn cancellation — it only ends on
// agent completion, user interrupt, or session removal.
func (p *AgentPool) WaitForAllSessionAgents(ctx context.Context, sessionID string) (WaitResult, error) {
	// 1. Get/create interrupt context for session
	interruptCtx := p.interrupt.GetOrCreateInterruptCtx(sessionID)
	defer p.cleanupInterruptCtx(sessionID)

	// 2. Get session-scoped context (survives turn cancel, dies on RemoveSession)
	sessionCtx := p.getOrCreateSessionCtx(sessionID)

	// 3. Collect completionCh for ALL running agents in session
	p.mu.RLock()
	var channels []<-chan struct{}
	for _, agent := range p.agents {
		if agent.SessionID == sessionID && agent.Status == "running" {
			channels = append(channels, agent.completionCh)
		}
	}
	p.mu.RUnlock()

	if len(channels) == 0 {
		return WaitResult{AllDone: true, Results: p.buildResults(sessionID)}, nil
	}

	// 4. Goroutine: wait ALL channels -> close allDone
	allDone := make(chan struct{})
	go func() {
		for _, ch := range channels {
			<-ch
		}
		close(allDone)
	}()

	// 5. select: allDone | interrupt | caller context cancel | session context cancel
	select {
	case <-allDone:
		return WaitResult{
			AllDone: true,
			Results: p.buildResults(sessionID),
		}, nil

	case <-interruptCtx.Done():
		msg := ExtractUserMessage(context.Cause(interruptCtx))
		isResponder := p.interrupt.ClaimInterruptResponder(sessionID)
		return WaitResult{
			Interrupted:          true,
			IsInterruptResponder: isResponder,
			UserMessage:          msg,
			StillRunning:         p.getRunningAgentIDs(sessionID),
			Results:              p.buildResults(sessionID), // partial: already completed
		}, nil

	case <-ctx.Done():
		return WaitResult{}, ctx.Err()

	case <-sessionCtx.Done():
		return WaitResult{}, sessionCtx.Err()
	}
}

// cleanupInterruptCtx removes interrupt context when no more running agents for session
func (p *AgentPool) cleanupInterruptCtx(sessionID string) {
	p.mu.RLock()
	hasRunning := false
	for _, a := range p.agents {
		if a.SessionID == sessionID && a.Status == "running" {
			hasRunning = true
			break
		}
	}
	p.mu.RUnlock()

	p.interrupt.CleanupIfNoRunning(sessionID, hasRunning)
}

// buildResults builds completion info for all non-running agents in session
func (p *AgentPool) buildResults(sessionID string) map[string]AgentCompletionInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()
	results := make(map[string]AgentCompletionInfo)
	for _, a := range p.agents {
		if a.SessionID == sessionID && a.Status != "running" {
			results[a.ID] = AgentCompletionInfo{
				AgentID:   a.ID,
				SubtaskID: a.SubtaskID,
				Status:    a.Status,
				Result:    a.Result,
				Error:     a.Error,
			}
		}
	}
	return results
}

// WaitForAgent waits for a specific agent to complete.
// Returns immediately if agent not found (already completed or unknown).
func (p *AgentPool) WaitForAgent(ctx context.Context, sessionID, agentID string) (AgentCompletionInfo, error) {
	p.mu.RLock()
	agent, ok := p.agents[agentID]
	p.mu.RUnlock()

	if !ok {
		// Agent not found — could have completed and been cleaned up
		results := p.buildResults(sessionID)
		if info, found := results[agentID]; found {
			return info, nil
		}
		return AgentCompletionInfo{AgentID: agentID, Status: "not_found"}, nil
	}

	ch := agent.completionCh
	select {
	case <-ch:
		p.mu.RLock()
		a, ok := p.agents[agentID]
		p.mu.RUnlock()
		if !ok {
			return AgentCompletionInfo{AgentID: agentID, Status: "not_found"}, nil
		}
		return a.snapshot().toCompletionInfo(), nil
	case <-ctx.Done():
		return AgentCompletionInfo{}, ctx.Err()
	}
}

// getRunningAgentIDs returns IDs of running agents in session
func (p *AgentPool) getRunningAgentIDs(sessionID string) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var ids []string
	for _, a := range p.agents {
		if a.SessionID == sessionID && a.Status == "running" {
			ids = append(ids, a.ID)
		}
	}
	return ids
}
