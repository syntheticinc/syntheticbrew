package app

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/repository"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/taskrunner"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools"
	agentservice "github.com/syntheticinc/syntheticbrew/internal/service/agent"
	"github.com/syntheticinc/syntheticbrew/internal/service/engine"
	"github.com/syntheticinc/syntheticbrew/internal/service/task"
	"github.com/syntheticinc/syntheticbrew/internal/service/turnexecutor"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
	einotool "github.com/cloudwego/eino/components/tool"
	"gorm.io/gorm"
)

// storageComponents holds all storage-related components created during initialization.
type storageComponents struct {
	TaskManager      *taskrunner.EngineTaskManagerAdapter // unified task manager (EngineTask-based)
	TaskRepo         *configrepo.GORMTaskRepository
	AgentRunStorage  agentservice.AgentRunStorage
	ContextReminders []turnexecutor.ContextReminderProvider
}

// createWorkStorage creates task manager, agent pool, session storage from pgDB.
func createWorkStorage(db *gorm.DB) *storageComponents {
	if db == nil {
		slog.ErrorContext(context.Background(), "no database connection, multi-agent features disabled")
		return &storageComponents{}
	}
	return initWorkComponents(db)
}

// initWorkComponents initializes all work-related components from a GORM DB.
func initWorkComponents(db *gorm.DB) *storageComponents {
	ctx := context.Background()
	result := &storageComponents{}

	taskRepo := configrepo.NewGORMTaskRepository(db)
	agentRunStorage := persistence.NewAgentRunStorage(db)
	result.AgentRunStorage = agentRunStorage
	result.TaskRepo = taskRepo

	// Startup cleanup: orphaned agent runs from previous crash
	cleaned, cleanErr := agentRunStorage.CleanupOrphanedRuns(ctx)
	if cleanErr != nil {
		slog.ErrorContext(ctx, "failed to cleanup orphaned agent runs", "error", cleanErr)
	} else if cleaned > 0 {
		slog.InfoContext(ctx, "cleaned up orphaned agent runs from previous crash", "count", cleaned)
	}

	// Unified task manager (replaces old work.Manager, uses EngineTask).
	result.TaskManager = taskrunner.NewEngineTaskManagerAdapter(taskRepo)
	slog.InfoContext(ctx, "task manager initialized (EngineTask-based)")

	// Context reminder for agent — shows active EngineTasks every turn (survives context compression).
	taskReminder := task.NewTaskReminderProviderContext(result.TaskManager)
	result.ContextReminders = append(result.ContextReminders, taskReminder)

	return result
}

// engineComponents holds Engine and its associated dependencies.
type engineComponents struct {
	Engine            *engine.Engine
	FlowManager       *agentservice.FlowManager
	AgentToolResolver *tools.AgentToolResolver
	ToolDepsProvider  *tools.DefaultToolDepsProvider
	BuiltinStore      *tools.BuiltinToolStore
}

// createEngine creates Engine, FlowManager, ToolResolver and ToolDepsProvider.
// Uses the shared PostgreSQL database for message and context snapshot storage.
func createEngine(
	cfg config.Config,
	db *gorm.DB,
	taskManager *taskrunner.EngineTaskManagerAdapter,
	agentPoolAdapter *agentservice.AgentPoolAdapter,
) (*engineComponents, error) {
	if db == nil {
		return nil, fmt.Errorf("database connection required for engine")
	}

	snapshotRepo := repository.NewAgentContextRepository(db)
	messageRepo := repository.NewMessageRepositoryImpl(db)
	agentEngine := engine.New(snapshotRepo, messageRepo)
	agentEngine.SetPromptPrefixProvider(newPolicyPromptPrefixProvider(configrepo.NewGORMTenantPolicyRepository(db)))
	slog.InfoContext(context.Background(), "engine initialized (PostgreSQL)")

	// Load flows.yaml (optional — not required in bootstrap/Docker mode)
	flowsPath := filepath.Join(cfg.ConfigDir, "flows.yaml")
	flowsCfg, err := config.LoadFlowsConfig(flowsPath)
	if err != nil {
		slog.InfoContext(context.Background(), "No flows.yaml found — using empty flows config (configure agents via Admin Dashboard)", "path", flowsPath)
		flowsCfg = &config.FlowsConfig{}
	}

	flowManager, err := agentservice.NewFlowManager(flowsCfg, cfg.Agent.Prompts)
	if err != nil {
		return nil, fmt.Errorf("create flow manager: %w", err)
	}
	slog.InfoContext(context.Background(), "flow manager initialized", "flows_path", flowsPath)

	// Create ToolDepsProvider with unified task manager.
	toolDepsProvider := tools.NewDefaultToolDepsProvider(
		nil, // proxy -- set dynamically per-session
		agentPoolAdapter,
	)
	if taskManager != nil {
		toolDepsProvider.SetEngineTaskManager(taskManager)
	}

	// Create AgentToolResolver (factory-based tool resolution)
	builtinStore := tools.NewBuiltinToolStore()
	tools.RegisterAllBuiltins(builtinStore)

	// Register spawn_agent separately (requires AgentPool, wired later via wireEngineToPool)
	if agentPoolAdapter != nil {
		builtinStore.Register("spawn_agent", func(deps tools.ToolDependencies) einotool.InvokableTool {
			return tools.NewSpawnAgentTool(deps.AgentPool, deps.SessionID, deps.ProjectKey)
		})
	}

	agentToolResolver := tools.NewAgentToolResolver(builtinStore)
	slog.InfoContext(context.Background(), "agent tool resolver initialized", "builtin_tools", len(builtinStore.Names()))

	return &engineComponents{
		Engine:            agentEngine,
		FlowManager:       flowManager,
		AgentToolResolver: agentToolResolver,
		ToolDepsProvider:  toolDepsProvider,
		BuiltinStore:      builtinStore,
	}, nil
}

// wireEngineToPool connects Engine to AgentPool and configures max concurrency.
func wireEngineToPool(
	agentPool *agentservice.AgentPool,
	ec *engineComponents,
) {
	if agentPool == nil || ec == nil {
		return
	}

	agentPool.SetEngine(ec.Engine, ec.FlowManager, ec.AgentToolResolver, ec.ToolDepsProvider, nil, nil)
	slog.InfoContext(context.Background(), "engine wired to agent pool")
}
