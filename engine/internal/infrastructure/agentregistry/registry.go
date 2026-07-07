package agentregistry

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
)

// AgentReader is the consumer-side interface for reading agent records.
type AgentReader interface {
	List(ctx context.Context) ([]configrepo.AgentRecord, error)
	GetByName(ctx context.Context, name string) (*configrepo.AgentRecord, error)
	Count(ctx context.Context) (int64, error)
}

// CapabilityReader is the consumer-side interface for bulk-loading capabilities.
// Returns all capabilities for the current tenant grouped by agent name.
type CapabilityReader interface {
	ListAll(ctx context.Context) (map[string][]configrepo.CapabilityRecord, error)
}

// RegisteredAgent holds a domain Flow and its original DB record.
// DerivedTools is the pre-computed, sorted, deduplicated list of tool names
// that the agent's runtime should have access to (base + spawn + capabilities).
// Use DerivedTools everywhere instead of reconstructing from Record.BuiltinTools.
type RegisteredAgent struct {
	Flow         *domain.Flow
	Record       configrepo.AgentRecord
	DerivedTools []string
}

// AgentRegistry loads agents from DB and caches them in memory.
type AgentRegistry struct {
	mu      sync.RWMutex
	agents  map[string]*RegisteredAgent
	repo    AgentReader
	capRepo CapabilityReader // optional; nil disables capability-derived tools
	deriver *Deriver         // optional; nil falls back to legacy DeriveRuntimeTools free function
}

// SetDeriver wires a strategy-based Deriver into the registry. When set,
// subsequent Load calls use Deriver.DeriveRuntimeTools (which dispatches
// capabilities via the capabilities.Registry) instead of the legacy free
// function. Construction-time DI: app.NewServer calls this once after
// constructing the registry. Safe to call before Load.
func (r *AgentRegistry) SetDeriver(d *Deriver) {
	r.mu.Lock()
	r.deriver = d
	r.mu.Unlock()
}

// New creates a new AgentRegistry.
func New(repo AgentReader) *AgentRegistry {
	return &AgentRegistry{
		agents: make(map[string]*RegisteredAgent),
		repo:   repo,
	}
}

// NewWithCapabilities creates a new AgentRegistry that also loads capabilities
// to populate DerivedTools on each agent at load time.
func NewWithCapabilities(repo AgentReader, capRepo CapabilityReader) *AgentRegistry {
	return &AgentRegistry{
		agents:  make(map[string]*RegisteredAgent),
		repo:    repo,
		capRepo: capRepo,
	}
}

// Load reads all agents from DB and caches them in memory.
// When a CapabilityReader is configured, capabilities are loaded in one bulk
// query and used to compute DerivedTools for each agent via DeriveRuntimeTools.
func (r *AgentRegistry) Load(ctx context.Context) error {
	records, err := r.repo.List(ctx)
	if err != nil {
		return fmt.Errorf("load agents: %w", err)
	}

	// Bulk-load capabilities once (zero extra queries when capRepo is nil).
	var capsByAgent map[string][]configrepo.CapabilityRecord
	if r.capRepo != nil {
		capsByAgent, err = r.capRepo.ListAll(ctx)
		if err != nil {
			return fmt.Errorf("load capabilities: %w", err)
		}
	}

	agents := make(map[string]*RegisteredAgent, len(records))
	for _, rec := range records {
		caps := capsByAgent[rec.Name] // nil if capRepo unset or agent has no caps
		var derived []string
		if r.deriver != nil {
			derived, err = r.deriver.DeriveRuntimeTools(ctx, rec, caps)
			if err != nil {
				return fmt.Errorf("derive tools for agent %q: %w", rec.Name, err)
			}
		} else {
			derived = DeriveRuntimeTools(rec, caps)
		}
		flow := toFlow(rec)
		agents[rec.Name] = &RegisteredAgent{
			Flow:         flow,
			Record:       rec,
			DerivedTools: derived,
		}
	}

	r.mu.Lock()
	r.agents = agents
	r.mu.Unlock()
	return nil
}

// Reload reloads all agents from DB (hot-reload support).
func (r *AgentRegistry) Reload(ctx context.Context) error {
	return r.Load(ctx)
}

// Get returns a registered agent by name.
func (r *AgentRegistry) Get(name string) (*RegisteredAgent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, ok := r.agents[name]
	if !ok {
		return nil, fmt.Errorf("agent %q not found", name)
	}
	return agent, nil
}

// GetByID returns a registered agent by its UUID.
// Used by chat dispatch to resolve a schema's entry_agent_id (UUID) onto the
// agent record (whose name drives the flow / LLM lookup path).
// ctx is accepted so multi-tenant callers can pass the request context;
// the single-tenant AgentRegistry ignores it because tenant dispatch happens
// at the Manager level (Manager.GetForContext) before this method is called.
func (r *AgentRegistry) GetByID(_ context.Context, id string) (*RegisteredAgent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, agent := range r.agents {
		if agent.Record.ID == id {
			return agent, nil
		}
	}
	return nil, fmt.Errorf("agent with id %q not found", id)
}

// GetFlow implements the FlowProvider interface used by EngineAdapter and AgentPool.
// This allows AgentRegistry to be a drop-in replacement for FlowManager.
func (r *AgentRegistry) GetFlow(_ context.Context, agentName string) (*domain.Flow, error) {
	agent, err := r.Get(agentName)
	if err != nil {
		return nil, err
	}
	return agent.Flow, nil
}

// List returns all registered agent names in alphabetical order.
func (r *AgentRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.agents))
	for name := range r.agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// GetAll returns a copy of all registered agents.
func (r *AgentRegistry) GetAll() map[string]*RegisteredAgent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]*RegisteredAgent, len(r.agents))
	for k, v := range r.agents {
		result[k] = v
	}
	return result
}

// GetDefault returns the first agent alphabetically.
func (r *AgentRegistry) GetDefault() (*RegisteredAgent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.agents) == 0 {
		return nil, fmt.Errorf("no agents configured")
	}

	var firstName string
	for name := range r.agents {
		if firstName == "" || name < firstName {
			firstName = name
		}
	}
	return r.agents[firstName], nil
}

// ResolveAgentUUID returns the UUID for the given agent name, or "" if not found.
// Implements turnexecutorfactory.AgentUUIDResolver and lifecycle.AgentUUIDResolver.
// The context parameter is ignored at this layer because the AgentRegistry is
// already tenant-scoped (single-tenant singleton or per-tenant instance managed
// by Manager.GetForContext); tenant dispatch happens at the Manager level.
func (r *AgentRegistry) ResolveAgentUUID(_ context.Context, agentName string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, ok := r.agents[agentName]
	if !ok {
		return ""
	}
	return agent.Record.ID
}

// ResolveModelID returns the ModelID for the given agent name, or nil if not found.
// Implements turnexecutorfactory.AgentModelResolver interface.
// Context is ignored here for the same reason as ResolveAgentUUID.
func (r *AgentRegistry) ResolveModelID(_ context.Context, agentName string) *string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, ok := r.agents[agentName]
	if !ok {
		return nil
	}
	return agent.Record.ModelID
}

// Count returns the number of registered agents.
func (r *AgentRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.agents)
}

// toFlow converts an AgentRecord into a domain.Flow.
func toFlow(rec configrepo.AgentRecord) *domain.Flow {
	spawn := domain.SpawnPolicy{
		AllowedFlows: make([]string, 0, len(rec.CanSpawn)),
	}
	for _, name := range rec.CanSpawn {
		spawn.AllowedFlows = append(spawn.AllowedFlows, string(name))
	}

	// Collect all tool names (builtin + custom)
	toolNames := make([]string, 0, len(rec.BuiltinTools)+len(rec.CustomTools))
	toolNames = append(toolNames, rec.BuiltinTools...)
	for _, ct := range rec.CustomTools {
		toolNames = append(toolNames, ct.Name)
	}

	lifecycle := domain.LifecyclePolicy{}
	switch rec.Lifecycle {
	case "persistent":
		lifecycle.SuspendOn = []string{"final_answer"}
		lifecycle.ReportTo = "user"
	case "spawn":
		lifecycle.ReportTo = "parent_agent"
	}

	// Append confirm_before instruction to system prompt (mirrors prompt_builder.go logic).
	// This ensures DB-configured confirm_before is applied in the SSE/HTTP path.
	// The confirmation flow is driven by the runtime ConfirmationWrapper —
	// the agent does not need a separate user-prompt tool; the wrapper
	// surfaces the confirmation event when these tools are invoked.
	systemPrompt := rec.SystemPrompt
	if len(rec.ConfirmBefore) > 0 {
		systemPrompt += "\n\n## Confirmation required\nThe runtime will request user confirmation before executing: " +
			strings.Join(rec.ConfirmBefore, ", ")
	}

	return &domain.Flow{
		Type:            string(rec.Name),
		Name:            rec.Name,
		SystemPrompt:    systemPrompt,
		ToolNames:       toolNames,
		MaxSteps:        rec.MaxSteps,
		MaxContextSize:  rec.MaxContextSize,
		MaxTurnDuration: rec.MaxTurnDuration,
		MaxStepDuration: rec.MaxStepDuration,
		ToolExecution:   rec.ToolExecution,
		Lifecycle:       lifecycle,
		Spawn:           spawn,
		MCPServers:      rec.MCPServers,
		ConfirmBefore:   rec.ConfirmBefore,
		Temperature:     rec.Temperature,
		TopP:            rec.TopP,
		MaxTokens:       rec.MaxTokens,
		StopSequences:   rec.StopSequences,
		IsSystem:        rec.IsSystem,
	}
}
