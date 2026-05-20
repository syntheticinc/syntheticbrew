package tools

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/agentregistry"
	"github.com/syntheticinc/bytebrew/engine/pkg/config"
)

// BuiltinToolFactory creates a tool instance given dependencies.
type BuiltinToolFactory func(deps ToolDependencies) tool.InvokableTool

// BuiltinToolStore stores builtin tool factories by name.
type BuiltinToolStore struct {
	factories map[string]BuiltinToolFactory
}

// NewBuiltinToolStore creates an empty BuiltinToolStore.
func NewBuiltinToolStore() *BuiltinToolStore {
	return &BuiltinToolStore{factories: make(map[string]BuiltinToolFactory)}
}

// Register adds a factory for the given tool name.
func (s *BuiltinToolStore) Register(name string, factory BuiltinToolFactory) {
	s.factories[name] = factory
}

// Get returns the factory for the given name.
func (s *BuiltinToolStore) Get(name string) (BuiltinToolFactory, bool) {
	f, ok := s.factories[name]
	return f, ok
}

// Names returns all registered tool names in alphabetical order.
func (s *BuiltinToolStore) Names() []string {
	names := make([]string, 0, len(s.factories))
	for name := range s.factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// MCPClientProvider provides MCP tools for a given MCP server name.
// Defined on the consumer side (AgentToolResolver).
//
// ctx carries tenant_id (Cloud) so the provider can route to the correct
// per-tenant ClientRegistry. CE implementations may ignore ctx — the
// singleton registry is shared across all callers.
type MCPClientProvider interface {
	// GetMCPTools returns Eino-compatible tools for the named MCP server.
	// Returns nil, nil if the server is not connected.
	GetMCPTools(ctx context.Context, name string) ([]tool.InvokableTool, error)
}

// CapabilityToolInjector returns additional tool names based on agent capabilities.
type CapabilityToolInjector interface {
	InjectedTools(ctx context.Context, agentName string) ([]string, error)
}

// CircuitBreakerRegistry provides circuit breakers for named resources.
type CircuitBreakerRegistry interface {
	Get(name string) CircuitBreakerChecker
}

// KnowledgeEmbedderResolver resolves a per-agent embedder from capability config (WP-4).
// Returns nil,nil if no embedding model is configured for the agent (fallback to global).
type KnowledgeEmbedderResolver interface {
	ResolveEmbedder(ctx context.Context, agentName string) (KnowledgeEmbedder, error)
}

// KnowledgeKBResolver resolves linked KB IDs for an agent (many-to-many).
type KnowledgeKBResolver interface {
	ListKBsByAgentName(ctx context.Context, agentName string) ([]string, error)
}

// CapabilityConfigReader reads capability config from DB for per-agent runtime customization.
type CapabilityConfigReader interface {
	ReadConfig(ctx context.Context, agentName, capType string) (map[string]interface{}, error)
}

// AgentToolResolver composes tools for a specific agent from various sources.
type AgentToolResolver struct {
	builtins                *BuiltinToolStore
	knowledgeSearcher       KnowledgeSearcher
	knowledgeEmbedder       KnowledgeEmbedder       // global fallback (Ollama)
	knowledgeEmbedResolver  KnowledgeEmbedderResolver // per-agent from capability config (WP-4)
	mcpProvider             MCPClientProvider
	spawner                 GenericAgentSpawner
	inspector               GenericAgentInspector
	capInjector             CapabilityToolInjector
	cbRegistry              CircuitBreakerRegistry
	capConfigReader         CapabilityConfigReader
	knowledgeKBResolver     KnowledgeKBResolver // resolves agent → linked KB IDs
	toolTimeoutMs           int64 // 0 = disabled
}

// NewAgentToolResolver creates a new AgentToolResolver.
func NewAgentToolResolver(builtins *BuiltinToolStore) *AgentToolResolver {
	return &AgentToolResolver{builtins: builtins}
}

// BuiltinStore returns the underlying BuiltinToolStore.
func (r *AgentToolResolver) BuiltinStore() *BuiltinToolStore {
	return r.builtins
}

// SetKnowledge configures knowledge search dependencies for auto-injection.
func (r *AgentToolResolver) SetKnowledge(searcher KnowledgeSearcher, embedder KnowledgeEmbedder) {
	r.knowledgeSearcher = searcher
	r.knowledgeEmbedder = embedder
}

// SetKnowledgeEmbedderResolver configures per-agent embedding model resolution (WP-4).
func (r *AgentToolResolver) SetKnowledgeEmbedderResolver(resolver KnowledgeEmbedderResolver) {
	r.knowledgeEmbedResolver = resolver
}

// SetKnowledgeKBResolver configures agent → KB IDs resolution (many-to-many).
func (r *AgentToolResolver) SetKnowledgeKBResolver(resolver KnowledgeKBResolver) {
	r.knowledgeKBResolver = resolver
}

// SetMCPProvider configures the MCP client provider for MCP tool resolution.
func (r *AgentToolResolver) SetMCPProvider(provider MCPClientProvider) {
	r.mcpProvider = provider
}

// SetSpawner configures the spawner and inspector for spawn tool resolution via legacy Resolve path.
func (r *AgentToolResolver) SetSpawner(spawner GenericAgentSpawner, inspector GenericAgentInspector) {
	r.spawner = spawner
	r.inspector = inspector
}

// SetCapabilityInjector configures the capability injector for auto-injecting tools based on agent capabilities.
func (r *AgentToolResolver) SetCapabilityInjector(injector CapabilityToolInjector) {
	r.capInjector = injector
}

// SetCircuitBreakerRegistry configures the circuit breaker registry for MCP tool protection.
func (r *AgentToolResolver) SetCircuitBreakerRegistry(registry CircuitBreakerRegistry) {
	r.cbRegistry = registry
}

// SetToolTimeout configures the per-MCP-tool-call timeout in milliseconds (AC-RESIL-05).
func (r *AgentToolResolver) SetToolTimeout(timeoutMs int64) {
	r.toolTimeoutMs = timeoutMs
}

// SetCapabilityConfigReader configures the reader for per-agent capability configs.
func (r *AgentToolResolver) SetCapabilityConfigReader(reader CapabilityConfigReader) {
	r.capConfigReader = reader
}


// ResolveContext holds per-agent resolution context.
type ResolveContext struct {
	Agent            *agentregistry.RegisteredAgent
	Deps             ToolDependencies
	ConfirmRequester ConfirmationRequester    // nil if no confirmation support
	Spawner          GenericAgentSpawner      // nil if spawn not available
	Inspector        GenericAgentInspector    // nil if inspect not available
	KnowledgeSearcher KnowledgeSearcher       // nil if no knowledge DB
	KnowledgeEmbedder KnowledgeEmbedder       // nil if no embeddings
	// Ctx carries the per-request context so MCP tool resolution can route
	// to the correct per-tenant ClientRegistry. Zero value (nil) is treated
	// as context.Background() — safe in CE single-tenant mode.
	Ctx context.Context
}

// ResolveForAgent returns tools available to a specific agent.
// DerivedTools (pre-computed at registry load time) is the single source of truth
// for which tools the agent has access to. Raw record fields (BuiltinTools, CanSpawn,
// CustomTools) are only consulted for per-tool construction details, not for membership.
func (r *AgentToolResolver) ResolveForAgent(ctx context.Context, rc ResolveContext) ([]tool.InvokableTool, error) {
	var tools []tool.InvokableTool

	// Build a set of derived tool names for O(1) membership checks.
	derivedSet := make(map[string]bool, len(rc.Agent.DerivedTools))
	for _, n := range rc.Agent.DerivedTools {
		derivedSet[n] = true
	}

	// Resolve builtin tools that are in DerivedTools (excludes knowledge_search —
	// handled separately below — and spawn_* / custom tool names which have their
	// own construction paths).
	for _, name := range rc.Agent.DerivedTools {
		// knowledge_search is constructed below with full KB/embedder wiring — skip here.
		if name == "knowledge_search" {
			continue
		}
		// spawn_* handled below (per-target and generic spawn_agent).
		if strings.HasPrefix(name, "spawn_") {
			continue
		}
		factory, ok := r.builtins.Get(name)
		if !ok {
			// Capability-derived tools (memory_recall, memory_store) or custom tool
			// names that aren't registered as builtins → warn and skip gracefully.
			slog.WarnContext(ctx, "tool in DerivedTools not registered as builtin, skipping",
				"agent", rc.Agent.Record.Name, "tool", name)
			continue
		}
		t := factory(rc.Deps)
		if t == nil {
			continue // tool disabled in this dependency context
		}
		tools = append(tools, t)
	}

	// Generate spawn_{name} tools for every per-target spawn_* entry in DerivedTools.
	// spawn_agent (Tier-1 generic) is handled separately below.
	if rc.Spawner != nil {
		for _, name := range rc.Agent.DerivedTools {
			if !strings.HasPrefix(name, "spawn_") || name == "spawn_agent" {
				continue
			}
			targetName := strings.TrimPrefix(name, "spawn_")
			spawnTool := NewSpawnTool(targetName, rc.Deps.SessionID, rc.Spawner, rc.Inspector)
			tools = append(tools, spawnTool)
		}
	}

	// Generic spawn_agent tool: accepts agent_name + input, spawns any reachable agent.
	if derivedSet["spawn_agent"] && rc.Spawner != nil {
		tools = append(tools, NewGenericSpawnTool(rc.Deps.SessionID, rc.Spawner, rc.Inspector, rc.Deps.EngineTaskManager))
	}

	// Custom declarative tools: keep the full CustomToolRecord (Name + Config JSON)
	// for construction, but only include tools whose name is in DerivedTools.
	for _, ct := range rc.Agent.Record.CustomTools {
		if !derivedSet[ct.Name] {
			continue
		}
		cfg := config.CustomToolConfig{Name: ct.Name}
		// ct.Config is JSON — parse if needed. For now, use name-only stub.
		dt := NewDeclarativeTool(cfg)
		tools = append(tools, dt)
	}

	// Phase 2.7: wrap confirm_before tools with ConfirmationWrapper
	if len(rc.Agent.Record.ConfirmBefore) > 0 && rc.ConfirmRequester != nil {
		confirmSet := make(map[string]bool, len(rc.Agent.Record.ConfirmBefore))
		for _, name := range rc.Agent.Record.ConfirmBefore {
			confirmSet[name] = true
		}
		for i, t := range tools {
			info, _ := t.Info(ctx)
			if info != nil && confirmSet[info.Name] {
				tools[i] = NewConfirmationWrapper(t, rc.ConfirmRequester)
			}
		}
	}

	// Knowledge search — auto-inject when agent has Knowledge capability.
	// Priority: 1) ResolveContext deps, 2) per-agent resolver (WP-4), 3) global fallback (Ollama).
	ks := rc.KnowledgeSearcher
	ke := rc.KnowledgeEmbedder
	if ks == nil {
		ks = r.knowledgeSearcher
	}
	if ke == nil && r.knowledgeEmbedResolver != nil {
		if resolved, err := r.knowledgeEmbedResolver.ResolveEmbedder(ctx, rc.Agent.Record.Name); err == nil && resolved != nil {
			ke = resolved
		}
	}
	if ke == nil {
		ke = r.knowledgeEmbedder
	}
	hasKnowledgeCap := derivedSet["knowledge_search"] // WP-3: present in DerivedTools
	if hasKnowledgeCap && ks != nil && ke != nil {
		topK := 5 // domain default
		var simThreshold float64
		if r.capConfigReader != nil {
			if kcfg, err := r.capConfigReader.ReadConfig(ctx, rc.Agent.Record.Name, "knowledge"); err == nil && kcfg != nil {
				if tk, ok := kcfg["top_k"].(float64); ok && int(tk) > 0 {
					topK = int(tk)
				}
				if st, ok := kcfg["similarity_threshold"].(float64); ok && st > 0 {
					simThreshold = st
				}
			}
		}
		// Resolve linked KB IDs for the agent.
		var kbIDs []string
		if r.knowledgeKBResolver != nil {
			if ids, err := r.knowledgeKBResolver.ListKBsByAgentName(ctx, rc.Agent.Record.Name); err == nil {
				kbIDs = ids
			}
		}
		knowledgeTool := NewKnowledgeSearchTool(rc.Agent.Record.Name, kbIDs, ks, ke, topK, simThreshold)
		tools = append(tools, knowledgeTool)
	} else if derivedSet["knowledge_search"] {
		slog.WarnContext(ctx, "agent has knowledge_search in DerivedTools but knowledge not available — skipping",
			"agent", rc.Agent.Record.Name,
			"searcher_available", ks != nil,
			"embedder_available", ke != nil)
	}

	// MCP tools — append tools from connected MCP servers configured for this agent.
	// Circuit breaker (US-006) wrapping happens inside resolveMCPTools.
	if rc.Ctx == nil {
		rc.Ctx = ctx
	}
	mcpTools, err := r.resolveMCPTools(rc)
	if err != nil {
		return nil, fmt.Errorf("resolve mcp tools for agent %q: %w", rc.Agent.Record.Name, err)
	}
	tools = append(tools, mcpTools...)

	return tools, nil
}

// Resolve implements the legacy ToolResolver interface (Resolve by tool names + deps).
// This allows AgentToolResolver to be used as a drop-in replacement for DefaultToolResolver
// in the turn_executor pipeline where RegisteredAgent is not yet available.
func (r *AgentToolResolver) Resolve(ctx context.Context, toolNames []string, deps ToolDependencies) ([]tool.InvokableTool, error) {
	// US-001: Inject capability-derived tool names before resolution
	allToolNames := toolNames
	capInjectedTools := make(map[string]bool) // track which tools came from capabilities
	if r.capInjector != nil && deps.AgentName != "" {
		injected, err := r.capInjector.InjectedTools(ctx, deps.AgentName)
		if err != nil {
			slog.WarnContext(ctx, "capability injection failed, continuing without injected tools",
				"agent", deps.AgentName, "error", err)
		} else if len(injected) > 0 {
			// Deduplicate: only add tools not already in the list
			existing := make(map[string]bool, len(toolNames))
			for _, n := range toolNames {
				existing[n] = true
			}
			for _, n := range injected {
				if !existing[n] {
					allToolNames = append(allToolNames, n)
					existing[n] = true
				}
				capInjectedTools[n] = true
			}
		}
	}

	var resolved []tool.InvokableTool

	for _, name := range allToolNames {
		// knowledge_search is auto-injected below via capability — skip here
		if name == "knowledge_search" {
			continue
		}
		factory, ok := r.builtins.Get(name)
		if !ok {
			// BUG-006: capability-injected tools that aren't registered yet → warn and skip.
			if capInjectedTools[name] {
				slog.WarnContext(ctx, "capability-injected tool not registered, skipping",
					"agent", deps.AgentName, "tool", name)
				continue
			}
			return nil, fmt.Errorf("resolve tool %s: unknown builtin tool", name)
		}
		t := factory(deps)
		if t == nil {
			continue
		}
		// Prompt-injection markers for untrusted tool output are applied at the
		// message_collector layer (LLM-bound only) — keeps raw content in SSE /
		// history / audit. See internal/service/engine/llm_content_wrap.go.
		t = NewCancellableToolWrapper(t)

		// Wrap confirm_before tools with ConfirmationWrapper (deterministic HITL confirmation)
		if deps.ConfirmRequester != nil && hasToolInList(deps.ConfirmBefore, name) {
			t = NewConfirmationWrapper(t, deps.ConfirmRequester)
		}

		resolved = append(resolved, t)
	}

	// Knowledge auto-injection via legacy Resolve path (capability-driven only).
	// Priority: 1) per-agent resolver (WP-4), 2) global fallback (Ollama).
	hasKnowledgeCapLegacy := capInjectedTools["knowledge_search"] // WP-3: capability-injected
	legacyEmbedder := KnowledgeEmbedder(r.knowledgeEmbedder)
	if r.knowledgeEmbedResolver != nil && deps.AgentName != "" {
		if resolved, err := r.knowledgeEmbedResolver.ResolveEmbedder(ctx, deps.AgentName); err == nil && resolved != nil {
			legacyEmbedder = resolved
		}
	}
	if hasKnowledgeCapLegacy && r.knowledgeSearcher != nil && legacyEmbedder != nil {
		legacyTopK := 5 // domain default
		var legacySimThreshold float64
		if r.capConfigReader != nil && deps.AgentName != "" {
			if kcfg, err := r.capConfigReader.ReadConfig(ctx, deps.AgentName, "knowledge"); err == nil && kcfg != nil {
				if tk, ok := kcfg["top_k"].(float64); ok && int(tk) > 0 {
					legacyTopK = int(tk)
				}
				if st, ok := kcfg["similarity_threshold"].(float64); ok && st > 0 {
					legacySimThreshold = st
				}
			}
		}
		// Resolve linked KB IDs for the agent.
		var kbIDs []string
		if r.knowledgeKBResolver != nil && deps.AgentName != "" {
			if ids, err := r.knowledgeKBResolver.ListKBsByAgentName(ctx, deps.AgentName); err == nil {
				kbIDs = ids
			}
		}
		knowledgeTool := NewKnowledgeSearchTool(deps.AgentName, kbIDs, r.knowledgeSearcher, legacyEmbedder, legacyTopK, legacySimThreshold)
		resolved = append(resolved, knowledgeTool)
	} else if hasToolInList(allToolNames, "knowledge_search") {
		slog.WarnContext(ctx, "knowledge_search in tool list but knowledge not available — skipping",
			"agent", deps.AgentName,
			"capability_injected", hasKnowledgeCapLegacy)
	}

	// Spawn tools via legacy Resolve path.
	// Generic spawn_agent is Tier-1: always available when spawner exists.
	if r.spawner != nil {
		resolved = append(resolved, NewGenericSpawnTool(deps.SessionID, r.spawner, r.inspector, deps.EngineTaskManager))
		for _, targetName := range deps.CanSpawn {
			spawnTool := NewSpawnTool(targetName, deps.SessionID, r.spawner, r.inspector)
			resolved = append(resolved, spawnTool)
		}
	}

	// MCP tools via legacy Resolve path. An unreachable MCP server fails the
	// resolve so engine_adapter can surface the error as an `error` SSE event
	// instead of silently dropping the agent's MCP-backed tool surface.
	if r.mcpProvider != nil && len(deps.MCPServers) > 0 {
		for _, serverName := range deps.MCPServers {
			mcpTools, err := r.mcpProvider.GetMCPTools(ctx, serverName)
			if err != nil {
				slog.WarnContext(ctx, "MCP server unreachable, failing tool resolve",
					"server", serverName, "error", err)
				return nil, fmt.Errorf("mcp server %q unreachable: %w", serverName, err)
			}
			// AC-RESIL-05: Timeout is innermost — fires first, feeds timeout error to CB
			// US-006: Circuit breaker wraps timeout
			for i, mt := range mcpTools {
				if r.toolTimeoutMs > 0 {
					mcpTools[i] = NewTimeoutToolWrapper(mt, r.toolTimeoutMs)
					mt = mcpTools[i]
				}
				if r.cbRegistry != nil {
					mcpTools[i] = NewCircuitBreakerToolWrapper(mt, r.cbRegistry.Get(serverName))
				}
			}
			resolved = append(resolved, mcpTools...)
		}
	}

	return resolved, nil
}

// resolveMCPTools returns tools from MCP servers configured for the agent.
// US-006: MCP tools are wrapped with circuit breaker and timeout if configured.
func (r *AgentToolResolver) resolveMCPTools(rc ResolveContext) ([]tool.InvokableTool, error) {
	if r.mcpProvider == nil || len(rc.Agent.Record.MCPServers) == 0 {
		return nil, nil
	}

	mcpCtx := rc.Ctx
	if mcpCtx == nil {
		mcpCtx = context.Background()
	}

	var result []tool.InvokableTool
	for _, serverName := range rc.Agent.Record.MCPServers {
		mcpTools, err := r.mcpProvider.GetMCPTools(mcpCtx, serverName)
		if err != nil {
			slog.WarnContext(mcpCtx, "failed to get MCP tools, skipping server",
				"server", serverName, "agent", rc.Agent.Record.Name, "error", err)
			continue
		}
		// AC-RESIL-05: Timeout is innermost — fires first, feeds timeout error to CB
		// US-006: Circuit breaker wraps timeout
		for i, mt := range mcpTools {
			if r.toolTimeoutMs > 0 {
				mcpTools[i] = NewTimeoutToolWrapper(mt, r.toolTimeoutMs)
				mt = mcpTools[i]
			}
			if r.cbRegistry != nil {
				mcpTools[i] = NewCircuitBreakerToolWrapper(mt, r.cbRegistry.Get(serverName))
			}
		}
		result = append(result, mcpTools...)
	}
	return result, nil
}

// hasToolInList checks if a tool name exists in the given list.
func hasToolInList(tools []string, name string) bool {
	for _, t := range tools {
		if t == name {
			return true
		}
	}
	return false
}
