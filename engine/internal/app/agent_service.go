package app

import (
	"context"
	"log/slog"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agents"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/taskrunner"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools"
	agentservice "github.com/syntheticinc/syntheticbrew/internal/service/agent"
	"github.com/syntheticinc/syntheticbrew/internal/service/engine"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
	"github.com/syntheticinc/syntheticbrew/pkg/errors"
	pluginpkg "github.com/syntheticinc/syntheticbrew/pkg/plugin"
	"gorm.io/gorm"
)

// InfraComponents holds all infrastructure components created during initialization
type InfraComponents struct {
	AgentService     *agentservice.Service
	TaskManager      *taskrunner.EngineTaskManagerAdapter
	TaskRepo         *configrepo.GORMTaskRepository
	AgentPool        *agentservice.AgentPool
	AgentPoolAdapter *agentservice.AgentPoolAdapter
	ModelSelector    *llm.ModelSelector
	// Engine components
	Engine            *engine.Engine
	FlowManager       *agentservice.FlowManager
	AgentToolResolver *tools.AgentToolResolver
	ToolDepsProvider  *tools.DefaultToolDepsProvider
	// Additional dependencies for TurnExecutorFactory
	ModelName   string
	ModelCache  *llm.ModelCache
	AgentConfig *config.AgentConfig // effective config with defaults applied
}

// InfraComponentsConfig holds optional parameters for NewInfraComponents.
type InfraComponentsConfig struct {
	Config config.Config
	DB     *gorm.DB // PostgreSQL GORM DB for runtime storage
	// ModelDebugDir, when non-empty, makes wrapWithDebugModel attach a
	// request/response logger to every chat model. Sourced from the
	// bootstrap config (Debug.ModelDebugDir, env SYNTHETICBREW_DEBUG_MODEL).
	ModelDebugDir string
	// Plugin is the runtime extension point. nil defaults to pluginpkg.Noop{}.
	// CE uses Noop so PrepareModelSelector is a no-op.
	Plugin pluginpkg.Plugin
}

// NewInfraComponents creates all infrastructure components including WorkManager and AgentPool.
func NewInfraComponents(icc InfraComponentsConfig) (*InfraComponents, error) {
	cfg := icc.Config

	// 1. Resolve the boot default chat model. The DB is the source of truth:
	// env (cfg.LLM) is only a fallback when the DB has no default set. This
	// keeps DB-only deployments chat-capable across non-deploy restarts
	// (eviction/drain/OOM/crash) without a manual re-apply.
	chatModel, modelName, err := resolveBootChatModel(cfg, icc.DB)
	if err != nil {
		return nil, err
	}

	chatModel = wrapWithDebugModel(chatModel, icc.ModelDebugDir)
	plug := icc.Plugin
	if plug == nil {
		plug = pluginpkg.Noop{}
	}
	modelSelector := createModelSelector(plug, chatModel, modelName)

	// 2. Create model cache (for dynamic model resolution from DB)
	var modelCache *llm.ModelCache
	if icc.DB != nil {
		modelCache = llm.NewModelCache(icc.DB)
	}

	// 3. Create work storage, agent pool, session storage
	storageCmp := createWorkStorage(icc.DB)

	var agentPool *agentservice.AgentPool
	var agentPoolAdapter *agentservice.AgentPoolAdapter
	if storageCmp.TaskManager != nil {
		agentPool = agentservice.NewAgentPool(agentservice.AgentPoolConfig{
			ModelSelector:   modelSelector,
			SubtaskManager:  storageCmp.TaskManager,
			AgentRunStorage: storageCmp.AgentRunStorage,
			AgentConfig:     &cfg.Agent,
		})
		agentPoolAdapter = agentservice.NewAgentPoolAdapter(agentPool)
		slog.InfoContext(context.Background(), "agent pool initialized")
	}

	// 4. Fill empty AgentConfig fields with defaults
	agentConfig := applyAgentConfigDefaults(&cfg.Agent)

	// 6. Create Engine and wire to AgentPool
	ec, err := createEngine(cfg, icc.DB, storageCmp.TaskManager, agentPoolAdapter)
	if err != nil {
		return nil, errors.Wrap(err, errors.CodeInternal, "failed to initialize engine")
	}

	wireEngineToPool(agentPool, ec)

	// 7. Add security reminder (highest priority -- last in context for max recency bias)
	contextReminders := storageCmp.ContextReminders
	contextReminders = append(contextReminders, agents.NewSecurityReminderProvider())

	// 8. Create AgentService (optional — nil when no LLM configured in Docker/bootstrap mode)
	var agentService *agentservice.Service
	if chatModel != nil {
		var svcErr error
		agentService, svcErr = agentservice.New(agentservice.Config{
			ChatModel:        chatModel,
			AgentPool:        agentPool,
			ContextReminders: contextReminders,
			MaxSteps:         cfg.Agent.MaxSteps,
			AgentConfig:      agentConfig,
			ModelName:        modelName,
			Streaming:        cfg.LLM.Streaming,
		})
		if svcErr != nil {
			return nil, errors.Wrap(svcErr, errors.CodeInternal, "failed to create agent service")
		}
		slog.InfoContext(context.Background(), "agent service created with multi-agent support",
			"model", modelName,
			"task_manager", storageCmp.TaskManager != nil,
			"agent_pool", agentPool != nil,
			"engine", ec.Engine != nil)
	} else {
		slog.InfoContext(context.Background(), "agent service skipped — no LLM model configured. Configure models via Admin Dashboard to enable chat.")
	}

	return &InfraComponents{
		AgentService:      agentService,
		TaskManager:       storageCmp.TaskManager,
		TaskRepo:          storageCmp.TaskRepo,
		AgentPool:         agentPool,
		AgentPoolAdapter:  agentPoolAdapter,
		ModelSelector:     modelSelector,
		Engine:            ec.Engine,
		FlowManager:       ec.FlowManager,
		AgentToolResolver: ec.AgentToolResolver,
		ToolDepsProvider:  ec.ToolDepsProvider,
		ModelName:         modelName,
		ModelCache:        modelCache,
		AgentConfig:       agentConfig,
	}, nil
}

// applyAgentConfigDefaults fills empty AgentConfig fields with defaults.
func applyAgentConfigDefaults(agentConfig *config.AgentConfig) *config.AgentConfig {
	defaultConfig := config.DefaultAgentConfig()

	if agentConfig.ContextLogPath == "" {
		agentConfig.ContextLogPath = defaultConfig.ContextLogPath
	}
	if agentConfig.MaxSteps == 0 {
		agentConfig.MaxSteps = defaultConfig.MaxSteps
	}
	if agentConfig.MaxContextSize == 0 {
		agentConfig.MaxContextSize = defaultConfig.MaxContextSize
	}
	if agentConfig.ToolReturnDirectly == nil {
		agentConfig.ToolReturnDirectly = defaultConfig.ToolReturnDirectly
	}

	return agentConfig
}
