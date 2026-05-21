package lifecycle

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// AgentRunner executes an agent with input and returns output.
type AgentRunner interface {
	RunAgent(ctx context.Context, agentName, input, sessionID string, eventStream domain.AgentEventStream) (string, error)
}

// ContextCompactor compacts agent context when it overflows.
type ContextCompactor interface {
	Compact(ctx context.Context, agentName, sessionID string) error
}

// AgentUUIDResolver resolves an agent name to its stable UUID.
// When set, the Manager uses UUID as the instance key instead of name,
// so renaming an agent does not reset its persistent context.
// Context is required so multi-tenant registries can dispatch to the
// caller's tenant_id (CE single-tenant registries ignore ctx).
type AgentUUIDResolver interface {
	ResolveAgentUUID(ctx context.Context, agentName string) string
}

// Manager tracks agent instances and their lifecycle (spawn vs persistent).
type Manager struct {
	mu           sync.RWMutex
	instances    map[string]*domain.AgentInstance // key: "agentUUID:sessionID" (or name if UUID unavailable)
	contexts     map[string][]string              // key: same as instances
	runner       AgentRunner
	compactor    ContextCompactor  // optional, nil-safe
	uuidResolver AgentUUIDResolver // optional: resolves name → UUID for stable keys
}

// NewManager creates a new lifecycle Manager.
func NewManager(runner AgentRunner) *Manager {
	return &Manager{
		instances: make(map[string]*domain.AgentInstance),
		contexts:  make(map[string][]string),
		runner:    runner,
	}
}

// SetCompactor sets the context compactor (optional).
func (m *Manager) SetCompactor(c ContextCompactor) {
	m.compactor = c
}

// SetUUIDResolver configures name→UUID resolution so persistent context
// survives agent renames.
func (m *Manager) SetUUIDResolver(r AgentUUIDResolver) {
	m.uuidResolver = r
}

// ExecuteTask executes a task on an agent, handling spawn vs persistent lifecycle.
func (m *Manager) ExecuteTask(ctx context.Context, agentName, sessionID, input string,
	mode domain.LifecycleMode, maxContext int, eventStream domain.AgentEventStream) (string, error) {

	key := m.resolvedKey(ctx, agentName, sessionID)

	instance := m.getOrCreateInstance(key, agentName, mode, maxContext)

	// For spawn agents, always reset context
	if mode == domain.LifecycleModeSpawn {
		m.mu.Lock()
		instance.ResetContext()
		m.contexts[key] = nil
		m.mu.Unlock()
	}

	// Transition: initializing → ready → running
	if instance.State() == domain.LifecycleInitializing {
		if err := instance.MarkReady(); err != nil {
			return "", fmt.Errorf("mark ready: %w", err)
		}
	}
	if instance.State() == domain.LifecycleReady {
		if err := instance.MarkRunning(); err != nil {
			return "", fmt.Errorf("mark running: %w", err)
		}
	}

	// Check if persistent agent needs compaction
	if instance.IsPersistent() && instance.NeedsCompaction() {
		slog.InfoContext(ctx, "lifecycle: auto-compacting context", "agent", agentName, "tokens", instance.ContextTokens)
		if m.compactor != nil {
			if err := m.compactor.Compact(ctx, agentName, sessionID); err != nil {
				slog.ErrorContext(ctx, "lifecycle: compaction failed", "error", err, "agent", agentName)
			}
		}
	}

	// Build full input for persistent agents (include previous context)
	fullInput := input
	if instance.IsPersistent() {
		m.mu.RLock()
		prevContext := m.contexts[key]
		m.mu.RUnlock()
		if len(prevContext) > 0 {
			fullInput = buildContextualInput(prevContext, input)
		}
	}

	// Execute the agent
	output, err := m.runner.RunAgent(ctx, agentName, fullInput, sessionID, eventStream)
	if err != nil {
		_ = instance.MarkBlocked()
		return "", fmt.Errorf("agent %q execution failed: %w", agentName, err)
	}

	// Store context for persistent agents
	if instance.IsPersistent() {
		m.mu.Lock()
		m.contexts[key] = append(m.contexts[key], "User: "+input, "Agent: "+output)
		instance.ContextTokens += estimateTokens(input) + estimateTokens(output)
		m.mu.Unlock()
	}

	// Finish task: spawn → finished, persistent → ready
	if err := instance.FinishTask(); err != nil {
		return output, fmt.Errorf("finish task: %w", err)
	}

	// For spawn agents, clean up instance
	if mode == domain.LifecycleModeSpawn {
		m.mu.Lock()
		delete(m.instances, key)
		delete(m.contexts, key)
		m.mu.Unlock()
	}

	return output, nil
}

// GetInstance returns the current agent instance state, if it exists.
func (m *Manager) GetInstance(ctx context.Context, agentName, sessionID string) (*domain.AgentInstance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.instances[m.resolvedKey(ctx, agentName, sessionID)]
	return inst, ok
}

// ResetAgent resets a persistent agent's context explicitly.
func (m *Manager) ResetAgent(ctx context.Context, agentName, sessionID string) {
	key := m.resolvedKey(ctx, agentName, sessionID)
	m.mu.Lock()
	defer m.mu.Unlock()

	if inst, ok := m.instances[key]; ok {
		inst.ResetContext()
	}
	m.contexts[key] = nil
}

// ContextSize returns the number of context entries for a persistent agent.
func (m *Manager) ContextSize(ctx context.Context, agentName, sessionID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.contexts[m.resolvedKey(ctx, agentName, sessionID)])
}

func (m *Manager) getOrCreateInstance(key, agentName string, mode domain.LifecycleMode, maxContext int) *domain.AgentInstance {
	m.mu.Lock()
	defer m.mu.Unlock()

	if inst, ok := m.instances[key]; ok {
		// For spawn agents, recreate instance for fresh state
		if mode == domain.LifecycleModeSpawn {
			inst = domain.NewAgentInstance(agentName, mode, maxContext)
			m.instances[key] = inst
		}
		return inst
	}

	inst := domain.NewAgentInstance(agentName, mode, maxContext)
	m.instances[key] = inst
	return inst
}

// resolvedKey builds the instance map key using UUID when available, falling
// back to the agent name. UUID-based keys survive agent renames.
func (m *Manager) resolvedKey(ctx context.Context, agentName, sessionID string) string {
	if m.uuidResolver != nil {
		if uuid := m.uuidResolver.ResolveAgentUUID(ctx, agentName); uuid != "" {
			return uuid + ":" + sessionID
		}
	}
	return agentName + ":" + sessionID
}

func instanceKey(agentName, sessionID string) string {
	return agentName + ":" + sessionID
}

func buildContextualInput(prevContext []string, newInput string) string {
	result := "Previous context:\n"
	for _, msg := range prevContext {
		result += msg + "\n"
	}
	result += "\nNew task:\n" + newInput
	return result
}

// estimateTokens provides a rough token count estimate (1 token ≈ 4 chars).
func estimateTokens(text string) int {
	return len(text) / 4
}
