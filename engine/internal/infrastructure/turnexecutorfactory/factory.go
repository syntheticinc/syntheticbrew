package turnexecutorfactory

import (
	"context"
	"log/slog"

	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/llm"
	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/tools"
	agentservice "github.com/syntheticinc/bytebrew/engine/internal/service/agent"
	"github.com/syntheticinc/bytebrew/engine/internal/service/engine"
	"github.com/syntheticinc/bytebrew/engine/internal/service/orchestrator"
	"github.com/syntheticinc/bytebrew/engine/internal/service/turnexecutor"
	"github.com/syntheticinc/bytebrew/engine/pkg/config"
)

// AgentModelResolver looks up the ModelID associated with a named agent.
// Returns nil when the agent has no per-agent model configured.
// Context is required so multi-tenant registries can resolve against the
// caller's tenant_id (CE registries ignore ctx — see AgentRegistry).
type AgentModelResolver interface {
	ResolveModelID(ctx context.Context, agentName string) *string
}

// AgentSchemaResolver resolves the primary schema ID (UUID) for an agent.
// Used to propagate SchemaID into tool dependencies for memory/knowledge tools.
type AgentSchemaResolver interface {
	ResolveSchemaID(ctx context.Context, agentName string) (string, error)
}

// AgentUUIDResolver resolves the UUID for an agent by name.
// Used to pass the correct uuid FK into engine.ExecutionConfig.AgentID.
// Context is required so multi-tenant registries can resolve against the
// caller's tenant_id (CE registries ignore ctx — see AgentRegistry).
type AgentUUIDResolver interface {
	ResolveAgentUUID(ctx context.Context, agentName string) string
}

// Factory creates EngineAdapter-based TurnExecutors for Supervisor mode.
// Implements grpc.TurnExecutorFactory interface (consumer-side).
type Factory struct {
	engine        *engine.Engine
	flowManager   turnexecutor.FlowProvider
	toolResolver  *tools.AgentToolResolver
	modelSelector *llm.ModelSelector
	modelCache    *llm.ModelCache
	agentResolver AgentModelResolver
	agentConfig   *config.AgentConfig
	// Raw deps for creating per-session ToolDepsProvider
	agentPool tools.AgentPoolForTool
	// Getter for context reminders (from AgentService)
	contextRemindersGetter func() []turnexecutor.ContextReminderProvider
	// Memory capability deps (injected via SetMemory — nil = disabled)
	memoryRecaller  tools.MemoryRecaller
	memoryStorer    tools.MemoryStorer
	memoryMaxEntries int
	// Engine task manager (injected via SetEngineTaskManager — nil = old task system fallback)
	engineTaskManager tools.EngineTaskManager
	// Schema resolver for memory/knowledge tools (BUG-007)
	schemaResolver AgentSchemaResolver
	// Per-agent capability config reader (memory max_entries, etc.)
	capConfigReader tools.CapabilityConfigReader
	// Agent UUID resolver: name → uuid FK (for engine_adapter ExecutionConfig.AgentID)
	agentUUIDResolver AgentUUIDResolver
}

// New creates a new factory for Engine-based TurnExecutors.
func New(
	engine *engine.Engine,
	flowManager turnexecutor.FlowProvider,
	toolResolver *tools.AgentToolResolver,
	modelSelector *llm.ModelSelector,
	agentConfig *config.AgentConfig,
	agentPool tools.AgentPoolForTool,
	contextRemindersGetter func() []turnexecutor.ContextReminderProvider,
	modelCache *llm.ModelCache,
	agentResolver AgentModelResolver,
) *Factory {
	return &Factory{
		engine:                 engine,
		flowManager:            flowManager,
		toolResolver:           toolResolver,
		modelSelector:          modelSelector,
		modelCache:             modelCache,
		agentResolver:          agentResolver,
		agentConfig:            agentConfig,
		agentPool:              agentPool,
		contextRemindersGetter: contextRemindersGetter,
	}
}

// SetMemory configures the memory storage for memory_recall/memory_store tools.
// Call after factory creation to enable memory capability tools.
func (f *Factory) SetMemory(recaller tools.MemoryRecaller, storer tools.MemoryStorer, maxEntries int) {
	f.memoryRecaller = recaller
	f.memoryStorer = storer
	f.memoryMaxEntries = maxEntries
}

// SetEngineTaskManager configures the DB-backed task manager so agents use EngineTask.
func (f *Factory) SetEngineTaskManager(mgr tools.EngineTaskManager) {
	f.engineTaskManager = mgr
}

// SetSchemaResolver configures schema lookup for propagating SchemaID to tool deps.
func (f *Factory) SetSchemaResolver(resolver AgentSchemaResolver) {
	f.schemaResolver = resolver
}

// SetCapabilityConfigReader configures per-agent capability config resolution.
func (f *Factory) SetCapabilityConfigReader(reader tools.CapabilityConfigReader) {
	f.capConfigReader = reader
}

// SetAgentUUIDResolver configures name→UUID resolution for engine execution context.
func (f *Factory) SetAgentUUIDResolver(resolver AgentUUIDResolver) {
	f.agentUUIDResolver = resolver
}

// userMemoryDepsProvider wraps DefaultToolDepsProvider and injects userID + memory refs per session.
type userMemoryDepsProvider struct {
	base             *tools.DefaultToolDepsProvider
	userID           string
	memoryRecaller   tools.MemoryRecaller
	memoryStorer     tools.MemoryStorer
	memoryMaxEntries int
}

func (p *userMemoryDepsProvider) GetDependencies(sessionID, projectKey string) tools.ToolDependencies {
	deps := p.base.GetDependencies(sessionID, projectKey)
	deps.UserID = p.userID
	deps.MemoryRecaller = p.memoryRecaller
	deps.MemoryStorer = p.memoryStorer
	deps.MemoryMaxEntries = p.memoryMaxEntries
	return deps
}

// CreateForSession creates a TurnExecutor for the given session.
// Implements grpc.TurnExecutorFactory interface.
//
// `ctx` carries per-request values (notably BYOK credentials extracted by
// the BYOK middleware via llm.BYOKCredentialsFrom) so the factory can
// build an ad-hoc per-end-user ChatModel instead of using the
// tenant-configured one. Pass context.Background() in code paths that
// have no per-request context (e.g. CLI runs).
func (f *Factory) CreateForSession(
	ctx context.Context,
	proxy tools.ClientOperationsProxy,
	sessionID, projectKey string,
	projectRoot, platform, agentName, userID string,
) orchestrator.TurnExecutor {
	// Create per-session ToolDepsProvider with proxy for this session
	baseDeps := tools.NewDefaultToolDepsProvider(
		proxy,
		f.agentPool,
	)
	if f.engineTaskManager != nil {
		baseDeps.SetEngineTaskManager(f.engineTaskManager)
	}
	// Resolve per-agent memory max_entries from capability config
	memMaxEntries := f.memoryMaxEntries
	if f.capConfigReader != nil {
		if cfg, err := f.capConfigReader.ReadConfig(ctx, agentName, "memory"); err == nil && cfg != nil {
			unlimitedEntries, _ := cfg["unlimited_entries"].(bool)
			if !unlimitedEntries {
				if me, ok := cfg["max_entries"].(float64); ok && int(me) > 0 {
					memMaxEntries = int(me)
				}
			}
		}
	}

	// Wrap with per-user memory deps
	toolDeps := &userMemoryDepsProvider{
		base:             baseDeps,
		userID:           userID,
		memoryRecaller:   f.memoryRecaller,
		memoryStorer:     f.memoryStorer,
		memoryMaxEntries: memMaxEntries,
	}

	// Get context reminders from getter (if provided)
	var contextReminders []turnexecutor.ContextReminderProvider
	if f.contextRemindersGetter != nil {
		contextReminders = f.contextRemindersGetter()
	}

	// Create per-request EnvironmentContextReminder (replaces any global one from getter)
	if projectRoot != "" || platform != "" {
		envReminder := agentservice.NewEnvironmentContextReminder(projectRoot, platform)
		var filtered []turnexecutor.ContextReminderProvider
		for _, r := range contextReminders {
			if _, ok := r.(*agentservice.EnvironmentContextReminder); !ok {
				filtered = append(filtered, r)
			}
		}
		contextReminders = append(filtered, envReminder)
	}

	// Append capability prompt hints so the agent knows about its capabilities.
	if f.capConfigReader != nil {
		var hints []string
		for _, cap := range []struct{ name, hint string }{
			{"memory", "You have Memory capability. Use memory_recall at the start of conversations to check for prior context about this user. Use memory_store to save important facts for future conversations."},
			{"knowledge", "You have Knowledge capability. Use knowledge_search to find relevant information from your knowledge base before answering questions."},
		} {
			if cfg, err := f.capConfigReader.ReadConfig(ctx, agentName, cap.name); err == nil && cfg != nil {
				hints = append(hints, cap.hint)
			}
		}
		if len(hints) > 0 {
			contextReminders = append(contextReminders, &capabilityHintReminder{hints: hints})
		}
	}

	// Resolve model: BYOK (per-request, per-end-user) overrides the
	// tenant-configured model when X-BYOK-* headers are present; otherwise
	// the per-agent DB model is used, falling back to the static
	// ModelSelector. See V2 §5.8.
	resolved := f.resolveModel(ctx, agentName)
	if resolved == nil || resolved.Client == nil {
		slog.ErrorContext(context.Background(), "no model available for agent — add a model via Admin Dashboard",
			"agent", agentName)
		return nil
	}

	// BUG-007: Resolve agent's schema for memory/knowledge tool deps.
	var schemaID string
	if f.schemaResolver != nil {
		sid, err := f.schemaResolver.ResolveSchemaID(ctx, agentName)
		if err != nil {
			slog.WarnContext(context.Background(), "failed to resolve schema for agent, memory tools may be disabled",
				"agent", agentName, "error", err)
		} else {
			schemaID = sid
		}
	}

	// Resolve agent UUID for engine execution context (agent_context_snapshots.agent_id = uuid FK).
	var agentUUID string
	if f.agentUUIDResolver != nil {
		agentUUID = f.agentUUIDResolver.ResolveAgentUUID(ctx, agentName)
	}

	adapter, err := turnexecutor.NewEngineAdapter(turnexecutor.Config{
		Engine:           f.engine,
		FlowProvider:     f.flowManager,
		ToolResolver:     f.toolResolver,
		ToolDeps:         toolDeps,
		ChatModel:        resolved.Client,
		AgentConfig:      f.agentConfig,
		ModelName:        resolved.Name,
		ProviderType:     resolved.ProviderType,
		ProviderBaseURL:  resolved.BaseURL,
		AgentName:        agentName,
		AgentUUID:        agentUUID,
		SchemaID:         schemaID,
		ContextReminders: contextReminders,
	})

	if err != nil {
		// Shouldn't happen if factory was created successfully.
		// If this occurs, Orchestrator will fail gracefully with nil TurnExecutor.
		return nil
	}

	return adapter
}

// resolveModel tries to resolve a model from the request context (BYOK),
// the DB cache via the agent's ModelID, then the static ModelSelector.
//
// Order of precedence:
//  1. BYOK credentials in ctx (V2 §5.8) — per-request override
//  2. Per-agent ModelID via ModelCache (tenant-configured)
//  3. Static ModelSelector (legacy config / single-tenant fallback)
//
// API key redaction: BYOK paths log only the provider + model + a
// fingerprinted key (llm.RedactAPIKey) — never the raw secret.
func (f *Factory) resolveModel(ctx context.Context, agentName string) *llm.ResolvedModel {
	if creds := llm.BYOKCredentialsFrom(ctx); creds != nil {
		client, err := llm.BuildBYOKChatModel(ctx, *creds)
		if err != nil {
			slog.ErrorContext(ctx, "byok: build chat model failed",
				"agent", agentName,
				"provider", creds.Provider,
				"model", creds.Model,
				"api_key", llm.RedactAPIKey(creds.APIKey),
				"error", err)
		} else {
			modelName := creds.Model
			if modelName == "" {
				modelName = creds.Provider
			}
			slog.InfoContext(ctx, "byok: using user-supplied model",
				"agent", agentName,
				"provider", creds.Provider,
				"model", modelName,
				"api_key", llm.RedactAPIKey(creds.APIKey))
			return &llm.ResolvedModel{
				Client:       client,
				Name:         modelName,
				ProviderType: creds.Provider,
				BaseURL:      creds.BaseURL,
			}
		}
	}

	if f.modelCache != nil && f.agentResolver != nil {
		modelID := f.agentResolver.ResolveModelID(ctx, agentName)
		if modelID != nil {
			resolved, err := f.modelCache.Resolve(ctx, *modelID)
			if err != nil {
				slog.ErrorContext(context.Background(), "failed to resolve model from cache, falling back to selector",
					"agent", agentName, "model_id", *modelID, "error", err)
			} else {
				return resolved
			}
		}
	}

	// Static ModelSelector fallback — provider type / base URL unknown.
	return &llm.ResolvedModel{
		Client: f.modelSelector.Select(agentName),
		Name:   f.modelSelector.ModelName(agentName),
	}
}

// capabilityHintReminder injects capability usage hints into the agent's context.
type capabilityHintReminder struct {
	hints []string
}

func (r *capabilityHintReminder) GetContextReminder(_ context.Context, _ string) (string, int, bool) {
	if len(r.hints) == 0 {
		return "", 0, false
	}
	content := "## Capabilities\n"
	for _, h := range r.hints {
		content += "- " + h + "\n"
	}
	return content, 5, true // priority 5: after env, before tasks
}
