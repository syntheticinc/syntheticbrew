package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools"
	"github.com/syntheticinc/syntheticbrew/internal/service/lifecycle"
	"github.com/syntheticinc/syntheticbrew/internal/service/orchestrator"
)

// chatAgentFactory is the consumer-side subset of TurnExecutorFactory needed by poolBasedRunner.
type chatAgentFactory interface {
	CreateForSession(ctx context.Context, proxy tools.ClientOperationsProxy,
		sessionID, projectKey, projectRoot, platform, agentName, userID string) orchestrator.TurnExecutor
}

// AgentLifecycleReader reads the lifecycle mode for an agent by name.
type AgentLifecycleReader interface {
	GetLifecycleMode(ctx context.Context, agentName string) domain.LifecycleMode
	GetMaxContextSize(ctx context.Context, agentName string) int
}

// CompositeAgentSpawner routes spawn requests based on the target agent's lifecycle mode.
// For "spawn" agents it delegates to the existing AgentPoolAdapter (unchanged execution path).
// For "persistent" agents it uses lifecycle.Manager to handle context accumulation.
type CompositeAgentSpawner struct {
	pool    tools.GenericAgentSpawner
	manager *lifecycle.Manager
	agents  AgentLifecycleReader

	// completed holds results of chat agents that ran synchronously through
	// lifecycle.Manager, keyed by session then synthetic spawn ID. The spawn
	// tools use an async contract (SpawnAgent → id, WaitForAgent(id) → result);
	// the manager path is synchronous, so its output is stashed here for
	// WaitForAgent to return instead of being handed back as if it were an
	// agent ID. Session-scoped (a session belongs to one tenant) and entries
	// are deleted on read; stale entries (spawn without a matching wait, e.g.
	// ctx cancellation between the two) are swept lazily on the next stash.
	mu        sync.Mutex
	completed map[string]map[string]stashedCompletion
}

// stashedCompletion is a completed sync-spawn result awaiting WaitForAgent.
type stashedCompletion struct {
	info    tools.AgentCompletionInfo
	stashed time.Time
}

// stashedCompletionTTL bounds how long an unclaimed sync-spawn result may
// live before the lazy sweep drops it.
const stashedCompletionTTL = 10 * time.Minute

// NewCompositeAgentSpawner creates a new CompositeAgentSpawner.
func NewCompositeAgentSpawner(
	pool tools.GenericAgentSpawner,
	manager *lifecycle.Manager,
	agents AgentLifecycleReader,
) *CompositeAgentSpawner {
	return &CompositeAgentSpawner{
		pool:      pool,
		manager:   manager,
		agents:    agents,
		completed: make(map[string]map[string]stashedCompletion),
	}
}

// SpawnAgent implements tools.GenericAgentSpawner by routing based on lifecycle mode
// and agent type.
//
// Chat agents (any agent that is not a code-agent like coder/researcher/reviewer)
// always run through lifecycle.Manager regardless of lifecycle mode, because
// AgentPool.SpawnWithDescription is a code-agent-only path that requires a gRPC
// session proxy and a registered code-agent flow — neither of which chat agents have.
//
// Code agents in spawn mode continue to use the original gRPC pool path.
//
// Spawn cycles (A → B → A) are detected via the ancestor chain on ctx: if the
// target appears among ancestors the spawn fails fast with [ERROR] cycle, which
// the spawn tool surfaces as a tool_result instead of letting the recursion
// hang the SSE stream.
func (c *CompositeAgentSpawner) SpawnAgent(ctx context.Context, params tools.SpawnParams) (string, error) {
	ancestors := domain.SpawnAncestorsFromContext(ctx)
	for _, a := range ancestors {
		if a == params.AgentName {
			chain := strings.Join(append(ancestors, params.AgentName), " → ")
			return "", fmt.Errorf("spawn cycle detected: %s", chain)
		}
	}
	ctx = domain.WithSpawnAncestor(ctx, params.AgentName)

	mode := c.agents.GetLifecycleMode(ctx, params.AgentName)

	if mode != domain.LifecycleModePersistent && !isChatAgent(params.AgentName) {
		return c.pool.SpawnAgent(ctx, params)
	}

	slog.InfoContext(ctx, "lifecycle: routing to manager",
		"agent", params.AgentName,
		"mode", mode,
		"session", params.SessionID,
		"ancestor_depth", len(ancestors),
	)

	maxContext := c.agents.GetMaxContextSize(ctx, params.AgentName)

	result, err := c.manager.ExecuteTask(
		ctx,
		params.AgentName,
		params.SessionID,
		params.Description,
		mode,
		maxContext,
		nil,
	)
	if err != nil {
		return "", fmt.Errorf("lifecycle execute task: %w", err)
	}

	// The manager ran the chat agent synchronously and already holds its output.
	// Honor the async spawn contract the tools rely on: return a retrieval ID and
	// stash the result for WaitForAgent, rather than returning the answer text as
	// if it were an agent ID (which WaitForAgent could never resolve, so the
	// delegator would report "completed (no output)").
	agentID := "chat-" + uuid.New().String()
	bucketKey := stashBucketKey(ctx, params.SessionID)
	c.mu.Lock()
	c.sweepStaleLocked()
	if c.completed[bucketKey] == nil {
		c.completed[bucketKey] = make(map[string]stashedCompletion)
	}
	c.completed[bucketKey][agentID] = stashedCompletion{
		info: tools.AgentCompletionInfo{
			AgentID: agentID,
			Status:  "completed",
			Result:  result,
		},
		stashed: time.Now(),
	}
	c.mu.Unlock()

	return agentID, nil
}

// stashBucketKey scopes stash buckets by tenant so a client-supplied session id
// can never address another tenant's bucket. Self-hosted runs carry the
// sentinel tenant, so the key stays stable there.
func stashBucketKey(ctx context.Context, sessionID string) string {
	return domain.TenantIDFromContext(ctx) + "/" + sessionID
}

// sweepStaleLocked drops unclaimed results older than stashedCompletionTTL.
// Caller must hold c.mu.
func (c *CompositeAgentSpawner) sweepStaleLocked() {
	cutoff := time.Now().Add(-stashedCompletionTTL)
	for key, bucket := range c.completed {
		for id, entry := range bucket {
			if entry.stashed.Before(cutoff) {
				delete(bucket, id)
			}
		}
		if len(bucket) == 0 {
			delete(c.completed, key)
		}
	}
}

// takeStashed removes and returns the stashed synchronous result for agentID in
// the session's bucket, reporting whether one was present. It manages its own
// lock; a flat early-return shape keeps WaitForAgent free of nested lookups.
func (c *CompositeAgentSpawner) takeStashed(bucketKey, agentID string) (tools.AgentCompletionInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	bucket, ok := c.completed[bucketKey]
	if !ok {
		return tools.AgentCompletionInfo{}, false
	}
	entry, ok := bucket[agentID]
	if !ok {
		return tools.AgentCompletionInfo{}, false
	}
	delete(bucket, agentID)
	if len(bucket) == 0 {
		delete(c.completed, bucketKey)
	}
	return entry.info, true
}

// WaitForAgent returns a synchronously-run chat agent's stashed result, or falls
// back to the pool for asynchronously-spawned code agents.
func (c *CompositeAgentSpawner) WaitForAgent(ctx context.Context, sessionID, agentID string) (tools.AgentCompletionInfo, error) {
	if info, ok := c.takeStashed(stashBucketKey(ctx, sessionID), agentID); ok {
		return info, nil
	}
	return c.pool.WaitForAgent(ctx, sessionID, agentID)
}

// WaitForAllSessionAgents delegates to the underlying pool.
func (c *CompositeAgentSpawner) WaitForAllSessionAgents(ctx context.Context, sessionID string) (tools.WaitResult, error) {
	return c.pool.WaitForAllSessionAgents(ctx, sessionID)
}

// HasBlockingWait delegates to the underlying pool.
func (c *CompositeAgentSpawner) HasBlockingWait(sessionID string) bool {
	return c.pool.HasBlockingWait(sessionID)
}

// NotifyUserMessage delegates to the underlying pool.
func (c *CompositeAgentSpawner) NotifyUserMessage(sessionID, message string) {
	c.pool.NotifyUserMessage(sessionID, message)
}

// StopAgent delegates to the underlying pool, scoped to the caller's session so
// an agent can only stop the agents it spawned in its own session (cloud-first
// isolation — the pool is process-global across tenants).
func (c *CompositeAgentSpawner) StopAgent(sessionID, agentID string) error {
	return c.pool.StopAgent(sessionID, agentID)
}

// agentSpawnerWaiter is the minimal interface poolBasedRunner needs:
// spawn an agent and wait for its completion by agentID.
type agentSpawnerWaiter interface {
	SpawnAgent(ctx context.Context, params tools.SpawnParams) (string, error)
	WaitForAgent(ctx context.Context, sessionID, agentID string) (tools.AgentCompletionInfo, error)
}

// poolBasedRunner wraps an agentSpawnerWaiter to implement lifecycle.AgentRunner.
// RunAgent spawns the agent, then blocks until it completes, returning its actual output.
type poolBasedRunner struct {
	pool        agentSpawnerWaiter
	chatFactory chatAgentFactory
}

// SetChatFactory wires the TurnExecutorFactory so chat agents can be executed
// via the SSE path rather than the code-agent pool path (which requires a gRPC proxy).
func (r *poolBasedRunner) SetChatFactory(f chatAgentFactory) {
	r.chatFactory = f
}

// isChatAgent returns true for schema-bound agents (delegated via can_spawn).
// Code agents ("coder", "researcher", "reviewer") use the pool path.
func isChatAgent(name string) bool {
	switch name {
	case "coder", "researcher", "reviewer":
		return false
	}
	return true
}

// RunAgent implements lifecycle.AgentRunner.
// For chat agents (schema agents) it uses the TurnExecutorFactory SSE path.
// For code agents it uses the pool path (spawn + wait).
func (r *poolBasedRunner) RunAgent(ctx context.Context, agentName, input, sessionID string, eventStream domain.AgentEventStream) (string, error) {
	if r.chatFactory != nil && isChatAgent(agentName) {
		return r.runChatAgent(ctx, agentName, input, sessionID, eventStream)
	}
	return r.runCodeAgent(ctx, agentName, input, sessionID)
}

func (r *poolBasedRunner) runCodeAgent(ctx context.Context, agentName, input, sessionID string) (string, error) {
	agentID, err := r.pool.SpawnAgent(ctx, tools.SpawnParams{
		SessionID:   sessionID,
		AgentName:   agentName,
		Description: input,
		Blocking:    false,
	})
	if err != nil {
		return "", fmt.Errorf("spawn agent: %w", err)
	}

	info, err := r.pool.WaitForAgent(ctx, sessionID, agentID)
	if err != nil {
		return "", fmt.Errorf("wait for agent %s: %w", agentID, err)
	}
	if info.Status == "failed" || info.Error != "" {
		reason := info.Error
		if reason == "" {
			reason = "agent failed without diagnostic"
		}
		return "", fmt.Errorf("agent %s failed: %s", agentID, reason)
	}
	return info.Result, nil
}

func (r *poolBasedRunner) runChatAgent(ctx context.Context, agentName, input, sessionID string, eventStream domain.AgentEventStream) (string, error) {
	proxy := tools.NewInProcessProxy()
	defer proxy.Dispose()

	executor := r.chatFactory.CreateForSession(ctx, proxy, sessionID, "", "", "", agentName, "")
	if executor == nil {
		return "", fmt.Errorf("no executor available for chat agent %q — check model configuration", agentName)
	}

	slog.InfoContext(ctx, "lifecycle: running chat agent via TurnExecutor",
		"agent", agentName,
		"session", sessionID,
	)

	var answer strings.Builder
	chunkCb := func(chunk string) error {
		answer.WriteString(chunk)
		return nil
	}
	eventCb := func(event *domain.AgentEvent) error {
		if eventStream != nil {
			return eventStream.Send(event)
		}
		return nil
	}

	if err := executor.ExecuteTurn(ctx, sessionID, "", input, chunkCb, eventCb); err != nil {
		return "", fmt.Errorf("chat agent %q execution: %w", agentName, err)
	}
	return answer.String(), nil
}
