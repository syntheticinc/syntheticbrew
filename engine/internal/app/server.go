// Package app provides the CE server bootstrap used by cmd/ce and the
// integration test harness (cmd/testserver uses a cut-down variant).
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	deliveryhttp "github.com/syntheticinc/syntheticbrew/internal/delivery/http"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/domain/capabilities"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agentregistry"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agents/callbacks"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/audit"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/auth"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/flowregistry"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/indexing"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/kgtools"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/lsp"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/mcp"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/repository"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/platform"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/portfile"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/taskrunner"
	admintools "github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools/admin"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/turnexecutorfactory"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/versioncheck"

	"github.com/syntheticinc/syntheticbrew/internal/service/capability"
	"github.com/syntheticinc/syntheticbrew/internal/service/eventstore"
	svcknowledge "github.com/syntheticinc/syntheticbrew/internal/service/knowledge"
	"github.com/syntheticinc/syntheticbrew/internal/service/lifecycle"
	memorysvc "github.com/syntheticinc/syntheticbrew/internal/service/memory"

	"github.com/syntheticinc/syntheticbrew/internal/service/resilience"
	"github.com/syntheticinc/syntheticbrew/internal/service/sessionprocessor"
	"github.com/syntheticinc/syntheticbrew/internal/service/turnexecutor"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/activeusers"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/kgread"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/usagelimit"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
	"github.com/syntheticinc/syntheticbrew/pkg/logger"
	pluginpkg "github.com/syntheticinc/syntheticbrew/pkg/plugin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// ServerConfig holds parameters for Run.
type ServerConfig struct {
	// ConfigPath is the path to the config file (resolved by the caller).
	ConfigPath string

	// ConfigExplicit is true when --config was explicitly provided on the command line.
	ConfigExplicit bool

	// Port overrides the config port (0 = use config or random).
	Port int

	// Managed enables managed subprocess mode (random port, READY protocol).
	Managed bool

	// Plugin is the runtime extension point. nil defaults to pluginpkg.Noop{}
	// — a silent pass-through that adds nothing to the server.
	Plugin pluginpkg.Plugin

	// RequireTenant enforces presence of a non-empty tenant_id after auth.
	// CE defaults to false (single-tenant). Multi-tenant setups set it true.
	RequireTenant bool

	// Version, Commit, Date are build-time metadata.
	Version string
	Commit  string
	Date    string
}

// Run starts the SyntheticBrew server with the given configuration.
// Called from cmd/ce (CE binary) and pkg/server.Run (integration tests).
func Run(sc ServerConfig) error {
	if sc.Plugin == nil {
		sc.Plugin = pluginpkg.Noop{}
	}

	// Process-global step accumulator: counts real agent steps per in-flight
	// turn, keyed by session id. Driven by the same per-step signal the agent
	// runtime already fires (the step callback below) and drained once by the
	// usage-limit settle when a turn completes. Session-keyed, so it is safe
	// under the multi-tenant invariant (session ids are unique across
	// tenants).
	usageAccumulator := usagelimit.NewStepAccumulator()

	// Wire the agent-step observer hook so plugins are notified after every
	// runtime step. The callbacks package uses a process-global callback
	// because the StepCounter lives deep in the agent infrastructure;
	// plumbing a dependency through four constructor layers for a single
	// observer hook would be disproportionate.
	plugin := sc.Plugin
	callbacks.SetStepCallback(func(ctx context.Context) error {
		usageAccumulator.Inc(domain.SessionIDFromContext(ctx))
		return plugin.OnAgentStep(ctx, domain.TenantIDFromContext(ctx), pluginpkg.StepsLimitFromContext(ctx))
	})

	// Always resolve data dir (needed for port file discovery)
	dataDir, err := UserDataDir()
	if err != nil {
		return fmt.Errorf("resolve user data directory: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}

	configPath := sc.ConfigPath

	// In managed mode, create additional subdirs and override paths
	if sc.Managed {
		if err := ensureManagedDirs(dataDir); err != nil {
			return fmt.Errorf("create managed directories: %w", err)
		}

		// If --config was not explicitly provided, use config from data dir.
		// Managed mode requires an explicit config.yaml — auto-generation has
		// been removed (legacy fallback). Operators must place a valid config
		// at $DATA_DIR/config.yaml or pass --config explicitly.
		if !sc.ConfigExplicit {
			managedConfigPath := filepath.Join(dataDir, "config.yaml")
			if _, err := os.Stat(managedConfigPath); os.IsNotExist(err) {
				return fmt.Errorf("managed mode requires config.yaml at %s — pass --config to override", managedConfigPath)
			}
			configPath = managedConfigPath
		}

		if err := EnsureManagedDefaults(dataDir); err != nil {
			return err
		}
	}

	// Get working directory for config path resolution
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	// Resolve config path relative to working directory
	if !filepath.IsAbs(configPath) {
		configPath = filepath.Join(wd, configPath)
	}

	// Load configuration — if config file doesn't exist, use defaults (env-var mode for Docker)
	var cfg *config.Config
	if _, statErr := os.Stat(configPath); os.IsNotExist(statErr) && !sc.ConfigExplicit {
		slog.InfoContext(context.Background(), "No config file found, using defaults (configure via environment variables or Admin Dashboard)", "path", configPath)
		cfg = config.DefaultConfig()
	} else {
		var loadErr error
		cfg, loadErr = config.Load(configPath)
		if loadErr != nil {
			return fmt.Errorf("load config: %w", loadErr)
		}
		slog.InfoContext(context.Background(), "Config loaded", "default_provider", cfg.LLM.DefaultProvider, "ollama_model", cfg.LLM.Ollama.Model)
	}

	// Check for already running server BEFORE touching log files.
	if err := portfile.AcquireLock(dataDir); err != nil {
		return err
	}

	// Apply managed mode overrides
	if sc.Managed {
		cfg.Logging.FilePath = filepath.Join(dataDir, "logs", "server.log")
		cfg.Server.Port = sc.Port
	}

	// Clear old logs if configured
	if cfg.Logging.ClearOnStartup {
		logsDir := filepath.Dir(cfg.Logging.FilePath)
		if logsDir == "" || logsDir == "." {
			logsDir = "logs"
		}
		removedCount, err := logger.ClearLogsDir(logsDir)
		if err != nil {
			slog.WarnContext(context.Background(), "failed to clear logs directory", "error", err)
		} else if removedCount > 0 {
			slog.InfoContext(context.Background(), "Cleared old log files", "count", removedCount, "dir", logsDir)
		}
	}

	// Initialize logger
	loggerInstance, err := logger.New(cfg.Logging)
	if err != nil {
		return fmt.Errorf("initialize logger: %w", err)
	}

	slog.SetDefault(loggerInstance.Logger)

	ctx := context.Background()
	loggerInstance.InfoContext(ctx, "Starting SyntheticBrew Server",
		"version", sc.Version,
		"commit", sc.Commit,
		"built", sc.Date,
		"config", configPath,
	)

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Try loading bootstrap config for PostgreSQL database connection.
	var agentRegistry *agentregistry.AgentRegistry
	var registryMgr *agentregistry.Manager
	var pgDB *gorm.DB
	var taskRepo *configrepo.GORMTaskRepository
	var apiTokenRepo *configrepo.GORMAPITokenRepository
	var capRegistry *capabilities.Registry
	var kgToolProvider *kgtools.Provider
	bootstrapCfg, bootstrapErr := config.LoadBootstrap(configPath)
	if bootstrapErr != nil {
		slog.InfoContext(context.Background(), "No bootstrap database config, running in legacy mode", "reason", bootstrapErr.Error())
	} else {
		var pgErr error
		pgDB, pgErr = gorm.Open(postgres.Open(bootstrapCfg.Database.URL), &gorm.Config{
			Logger: gormlogger.Default.LogMode(gormlogger.Silent),
		})
		if pgErr != nil {
			return fmt.Errorf("connect to PostgreSQL: %w", pgErr)
		}

		agentRepo := configrepo.NewGORMAgentRepository(pgDB)
		taskRepo = configrepo.NewGORMTaskRepository(pgDB)
		apiTokenRepo = configrepo.NewGORMAPITokenRepository(pgDB)
		capRepoForRegistry := configrepo.NewGORMCapabilityRepository(pgDB)
		registryMgr = agentregistry.NewManagerWithCapabilities(agentRepo, capRepoForRegistry, sc.RequireTenant)
		// Strategy-based capability dispatch (Этап 0 — Capability Strategy Refactor).
		// Single registry shared by AgentRegistry (via Deriver) and capability.Injector.
		// Adding a new capability requires ONE new file in internal/domain/capabilities
		// plus ONE line below. No modifications to switch statements scattered around.

		// Knowledge Graphs (Этап 2 — engine 1.3.0) — per-tenant tool resolver
		// wired into the strategy registry so `list_X` / `get_X` MCP tools
		// generated by KG schemas appear in agent runtime tools automatically.
		kgSchemaRepoForTools := configrepo.NewGORMKGSchemaRepository(pgDB)
		kgToolProvider = kgtools.NewProvider(kgSchemaRepoForTools)

		capRegistry = capabilities.NewRegistry(
			capabilities.MemoryCapability{},
			capabilities.KnowledgeCapability{},
			capabilities.NewKnowledgeGraphsCapability(kgToolProvider),
		)
		registryMgr.SetDeriver(agentregistry.NewDeriver(capRegistry))
		if loadErr := registryMgr.Init(ctx); loadErr != nil {
			return fmt.Errorf("load agents from database: %w", loadErr)
		}
		if !sc.RequireTenant {
			agentRegistry = registryMgr.Single()
			agentCount := agentRegistry.Count()
			if agentCount > 0 {
				slog.InfoContext(ctx, "Loaded agents from database", "count", agentCount, "agents", agentRegistry.List())
			} else {
				slog.InfoContext(ctx, "No agents configured in database")
			}
		} else {
			slog.InfoContext(ctx, "Multi-tenant mode: agent registries loaded per-tenant on first request")
		}

		// Wire the tenant seeder so plugins that handle tenant provisioning can
		// populate newly-created tenants with default data via engine repositories
		// rather than reimplementing schema/agent creation. CE's Noop plugin
		// ignores the seeder, so this is safe to wire unconditionally.
		sc.Plugin.SetTenantSeeder(&engineTenantSeeder{
			schemaRepo: configrepo.NewGORMSchemaRepository(pgDB),
			db:         pgDB,
		})

		// Wire the schema counter so plugins can enforce per-tenant schema
		// quotas without making an internal HTTP sub-request (the old sub-request
		// design hard-coded the loopback port and silently failed open whenever
		// the engine bound a non-default port). CE's Noop plugin ignores the
		// counter — safe to wire unconditionally. The closure body lives in
		// schema_counter.go so the per-tenant scope contract has its own test
		// surface.
		sc.Plugin.SetSchemaCounter(NewSchemaCounter(configrepo.NewGORMSchemaRepository(pgDB)))

		// Wire the usage-limit writer so a provisioning plugin can install a
		// default per-tenant cap through the engine's own tenant-scoped
		// repository (never clobbering an existing limit). CE's Noop plugin
		// ignores it — safe to wire unconditionally.
		sc.Plugin.SetUsageLimitWriter(newEngineUsageLimitWriter(configrepo.NewGORMUsageLimitRepository(pgDB)))

		// Wire the embedding-model writer so a provisioning plugin can install a
		// default embedding model on a freshly-created tenant through the
		// engine's own tenant-scoped model repository (idempotent — never
		// duplicating or clobbering a tenant's own embedding model). CE's Noop
		// plugin ignores it — safe to wire unconditionally.
		sc.Plugin.SetEmbeddingModelWriter(newEngineEmbeddingModelWriter(configrepo.NewGORMLLMProviderRepository(pgDB)))

		// Wire the tenant-policy seam so a plugin can write and read protected
		// per-tenant policy entries through the engine's own tenant-scoped
		// repository. No HTTP route exposes this table — the seam is the only
		// write path. CE's Noop plugin ignores both — safe to wire
		// unconditionally.
		policyRepo := configrepo.NewGORMTenantPolicyRepository(pgDB)
		sc.Plugin.SetTenantPolicyWriter(newEngineTenantPolicyWriter(policyRepo))
		sc.Plugin.SetTenantPolicyReader(newEngineTenantPolicyReader(policyRepo))

		// Wire the knowledge-document counter so a plugin can count a tenant's
		// documents in-process, mirroring the schema counter. CE's Noop plugin
		// ignores it — safe to wire unconditionally.
		sc.Plugin.SetKnowledgeDocumentCounter(NewKnowledgeDocumentCounter(configrepo.NewGORMKnowledgeRepository(pgDB)))
	}

	// Create infrastructure components (AgentService + WorkManager + AgentPool)
	infraCfg := InfraComponentsConfig{
		Config: *cfg,
		DB:     pgDB,
		Plugin: sc.Plugin,
	}
	if bootstrapCfg != nil {
		infraCfg.ModelDebugDir = bootstrapCfg.Debug.ModelDebugDir
	}
	components, err := NewInfraComponents(infraCfg)
	if err != nil {
		return fmt.Errorf("create infrastructure components: %w", err)
	}

	// Knowledge embeddings infrastructure (created before HTTP so endpoints can use it)
	var knowledgeRepo *configrepo.GORMKnowledgeRepository
	var embeddingsClient *indexing.EmbeddingsClient
	if pgDB != nil {
		knowledgeRepo = configrepo.NewGORMKnowledgeRepository(pgDB)
		var embedCfg config.EmbeddingsConfig
		if bootstrapCfg != nil {
			embedCfg = bootstrapCfg.Embeddings
		}
		embeddingsClient = indexing.NewClient(embedCfg)
	}

	// Initialize MCP Manager — owns per-tenant ClientRegistry instances.
	// Single-tenant (perTenant=false): Init eagerly loads the singleton for
	// CETenantID. Multi-tenant (perTenant=true): Init no-ops; per-tenant
	// registries lazy-load on first GetForContext call from a request bearing
	// tenant_id.
	//
	// In legacy mode (pgDB == nil) the manager is built with a nil reader and
	// never has Init called — the singleton is empty and all GetMCPTools
	// return "not registered". This matches pre-1.1.9 behaviour for the
	// ConfigPath-only fallback boot path.
	var mcpManager *mcp.Manager
	if pgDB != nil {
		mcpServerRepoForManager := configrepo.NewGORMMCPServerRepository(pgDB)
		mcpManager = mcp.NewManager(mcpServerRepoForManager, sc.Plugin.TransportPolicy(), sc.RequireTenant)
	} else {
		mcpManager = mcp.NewManager(nil, sc.Plugin.TransportPolicy(), sc.RequireTenant)
	}

	// Forward-headers cache — per-tenant when RequireTenant=true, single slot
	// otherwise. Wired into the Manager so every Init / lazy-load /
	// ReconnectTenant refreshes the affected tenant's entry. ChatHandler /
	// AdminAssistantHandler read the request-scoped slice via
	// GetForContext(r.Context()).
	forwardHeadersStore := mcp.NewForwardHeadersStore(sc.RequireTenant)
	mcpManager.SetForwardHeadersStore(forwardHeadersStore)

	// Per-server TTL refresher — schedules tools/list refresh goroutines for
	// MCP servers with catalog_refresh_interval_seconds set. Bound to the
	// server lifetime ctx so cancellation propagates; StopAll on shutdown
	// guarantees deterministic teardown even if Init bails early.
	mcpRefresher := mcp.NewRefresher(mcpManager, ctx)
	mcpManager.SetRefresher(mcpRefresher)
	defer mcpRefresher.StopAll()

	// Apply LSP installer toggle from bootstrap config (env SYNTHETICBREW_DISABLE_LSP_DOWNLOAD).
	// One-shot at startup; subsequent updates require restart.
	if bootstrapCfg != nil {
		lsp.SetInstallDisabled(bootstrapCfg.LSP.DisableDownload)
	}

	// Seed bootstrap data (builder-assistant, MCP catalog, schema templates, BYOK).
	// Must run BEFORE the MCP connect pass so the seeded syntheticbrew-docs entry is
	// included in the first connect cycle. Helper lives in seed.go.
	//
	// docsMCPURL precedence: env override (SYNTHETICBREW_DOCS_MCP_URL via bootstrap)
	// wins over plugin default. CE's Noop returns "" so the seeder no-ops when
	// nothing is configured.
	var docsMCPURL string
	if bootstrapCfg != nil {
		docsMCPURL = bootstrapCfg.MCP.DocsURL
	}
	if docsMCPURL == "" {
		docsMCPURL = sc.Plugin.DocsMCPEndpoint()
	}
	var bootstrapAdminToken string
	if bootstrapCfg != nil {
		bootstrapAdminToken = bootstrapCfg.Seed.BootstrapAdminToken
	}
	var bootstrapBYOK config.BootstrapBYOK
	if bootstrapCfg != nil {
		bootstrapBYOK = bootstrapCfg.BYOK
	}
	if err := bootstrapSeeds(ctx, pgDB, cfg.BYOK, bootstrapBYOK, docsMCPURL, bootstrapAdminToken); err != nil {
		return fmt.Errorf("bootstrap seed: %w", err)
	}

	if pgDB != nil {
		// Manager.Init: single-tenant eagerly loads CETenantID singleton
		// (preserves pre-1.1.9 boot logs) and refreshes the forward-headers
		// store for the CE sentinel. Multi-tenant is a no-op — per-tenant
		// registries lazy-load on first request and refresh their own
		// forward-headers entry.
		if initErr := mcpManager.Init(ctx); initErr != nil {
			slog.WarnContext(ctx, "failed to initialise MCP manager", "error", initErr)
		}
	}

	// Wire MCP provider into AgentToolResolver — adapter routes per-request
	// ctx (with tenant_id) to the correct ClientRegistry.
	if components.AgentToolResolver != nil {
		components.AgentToolResolver.SetMCPProvider(newMCPProviderAdapter(mcpManager))
	}

	// Register admin tools and reload registry.
	//
	// Admin tools (admin_list_agents, admin_create_schema, etc.) are tenant-
	// agnostic at registration time — they scope queries via ctx at call time
	// using domain.TenantIDFromContext. Previously this branch was gated on
	// agentRegistry != nil, which in multi-tenant mode (sc.RequireTenant=true)
	// is always nil because Single() is only called in single-tenant mode.
	// That caused builder-assistant to crash on every turn with
	// "unknown builtin tool: admin_list_agents" since the builtin store was
	// never populated. Registration only needs pgDB — the repos it captures
	// are the global DB handles that honor the request context.
	if pgDB != nil {

		// Wire admin tools into builtin store for builder-assistant.
		if components.AgentToolResolver != nil {
			// mcpSyncer keeps the live per-tenant MCP client registry in step
			// with the provisioning tools' lifecycle writes (same tenant-scoped
			// path as the REST reconnectAfterCRUD hook). Nil on the legacy
			// no-DB boot where the manager has no reader — tools skip the sync.
			var mcpSyncer admintools.MCPClientSyncer
			if mcpManager != nil {
				mcpSyncer = newMCPClientSyncAdapter(mcpManager)
			}

			// Knowledge-base tool deps: the same guarded upload service the REST
			// path uses (document quota seam wired), plus the KB store/repo. Lets
			// an MCP client build a grounded agent end to end. The doc guard is
			// the plugin itself — CE Noop admits everything.
			kbRepoForTools := configrepo.NewGORMKnowledgeBaseRepository(pgDB)
			uploadForTools := svcknowledge.NewUploadService(configrepo.NewGORMKnowledgeRepository(pgDB))
			uploadForTools.SetEmbeddingResolver(&embeddingModelResolver{db: pgDB})
			uploadForTools.SetKBEmbeddingResolver(&kbEmbeddingResolver{db: pgDB})
			uploadForTools.SetDocumentGuard(sc.Plugin)
			uploadForTools.SetEmbedderFactory(&embedderFactoryAdapter{plugin: sc.Plugin})
			knowledgeToolDeps := newKnowledgeToolAdapter(
				&kbStoreAdapter{repo: kbRepoForTools, db: pgDB},
				kbRepoForTools,
				uploadForTools,
				pgDB,
			)

			publicBaseURL := ""
			if bootstrapCfg != nil {
				publicBaseURL = bootstrapCfg.Engine.PublicBaseURL
			}
			// One agent-relation repo instance feeds both the MCP adapter (List/
			// Delete) and the shared create-seam usecase (cycle guard), so the MCP
			// admin tool inherits the same acyclicity guard as the REST path.
			adminRelRepo := configrepo.NewGORMAgentRelationRepository(pgDB)
			admintools.RegisterAdminTools(components.AgentToolResolver.BuiltinStore(), admintools.AdminToolDependencies{
				AgentRepo:  newAdminAgentRepoAdapter(configrepo.NewGORMAgentRepository(pgDB)),
				SchemaRepo: newAdminSchemaRepoAdapter(configrepo.NewGORMSchemaRepository(pgDB)),
				// Guarded creation path: tool-driven schema creation passes the
				// same plugin quota seam as the REST handler and the fork.
				SchemaCreator:     newAdminSchemaCreatorAdapter(newSchemaCreateUsecase(configrepo.NewGORMSchemaRepository(pgDB), sc.Plugin)),
				MCPServerRepo:     newAdminMCPServerRepoAdapter(configrepo.NewGORMMCPServerRepository(pgDB)),
				ModelRepo:         newAdminModelRepoAdapter(configrepo.NewGORMLLMProviderRepository(pgDB)),
				AgentRelationRepo: newAdminAgentRelationRepoAdapter(adminRelRepo, configrepo.NewGORMAgentRepository(pgDB), newAgentRelationCreateUsecase(adminRelRepo)),
				SessionRepo:       newAdminSessionRepoAdapter(configrepo.NewGORMSessionRepository(pgDB)),
				CapabilityRepo:    newAdminCapabilityRepoAdapter(configrepo.NewGORMCapabilityRepository(pgDB)),
				Reloader: func(ctx context.Context) {
					if registryMgr == nil {
						return
					}
					// Tenant-scoped: a tenant's admin/provisioning write invalidates
					// only its own cached agent registry, never a broadcast eviction
					// of every tenant (per-tenant scope). CE has no tenant in ctx, so
					// InvalidateAll reloads the single shared registry.
					if tid := domain.TenantIDFromContext(ctx); tid != "" {
						registryMgr.InvalidateTenant(tid)
						return
					}
					registryMgr.InvalidateAll()
				},
				TransportPolicy:   sc.Plugin.TransportPolicy(),
				WidgetTokenMinter: &widgetTokenMinterAdapter{repo: apiTokenRepo},
				PublicBaseURL:     publicBaseURL,
				MCPSyncer:         mcpSyncer,
				KnowledgeBase:     knowledgeToolDeps,
			})
			slog.InfoContext(ctx, "admin tools registered into builtin store")
		}

		// Reload registry so the seeded builder-assistant is available at runtime.
		if registryMgr != nil {
			registryMgr.InvalidateAll()
		}
	}

	// Wire knowledge search into AgentToolResolver.
	// embeddingsClient may be nil (no Ollama) — per-agent resolver provides embedding models.
	if components.AgentToolResolver != nil && knowledgeRepo != nil {
		components.AgentToolResolver.SetKnowledge(knowledgeRepo, embeddingsClient)
		if pgDB != nil {
			components.AgentToolResolver.SetKnowledgeEmbedderResolver(
				&knowledgeEmbedderResolverAdapter{resolver: &embeddingModelResolver{db: pgDB}, plugin: sc.Plugin})
			components.AgentToolResolver.SetKnowledgeKBResolver(
				configrepo.NewGORMKnowledgeBaseRepository(pgDB))
		}
	}

	// Wire spawner into AgentToolResolver for HTTP chat path spawn support.
	// CompositeAgentSpawner routes spawn requests based on agent lifecycle mode:
	// "spawn" agents → pool (unchanged), "persistent" agents → lifecycle.Manager.
	var lifecycleManager *lifecycle.Manager
	var lifecycleDispatcher *lifecycle.Dispatcher
	var agentLifecycleReader AgentLifecycleReader
	var poolRunner *poolBasedRunner
	if components.AgentPoolAdapter != nil && registryMgr != nil {
		poolRunner = &poolBasedRunner{pool: components.AgentPoolAdapter}
		lifecycleManager = lifecycle.NewManager(poolRunner)
		lifecycleDispatcher = lifecycle.NewDispatcher(lifecycleManager)

		if agentRegistry != nil {
			// Single-tenant: use pre-loaded singleton registry.
			agentLifecycleReader = newAgentRegistryLifecycleAdapter(agentRegistry)
			lifecycleManager.SetUUIDResolver(agentRegistry)
		} else {
			// Multi-tenant: resolve per-request via Manager.GetForContext.
			agentLifecycleReader = newManagerLifecycleAdapter(registryMgr)
			lifecycleManager.SetUUIDResolver(agentregistry.NewTenantAwareFlowProvider(registryMgr))
		}

		if components.AgentToolResolver != nil {
			compositeSpawner := NewCompositeAgentSpawner(
				components.AgentPoolAdapter,
				lifecycleManager,
				agentLifecycleReader,
			)
			components.AgentToolResolver.SetSpawner(compositeSpawner, components.AgentPoolAdapter)
			slog.InfoContext(ctx, "CompositeAgentSpawner wired into AgentToolResolver")
		}
	}

	// US-001: Wire capability injector into AgentToolResolver
	if components.AgentToolResolver != nil && pgDB != nil {
		capRepo := configrepo.NewGORMCapabilityRepository(pgDB)
		injector := capability.NewInjector(&capabilityInjectorAdapter{repo: capRepo}, capRegistry)
		components.AgentToolResolver.SetCapabilityInjector(injector)
		slog.InfoContext(ctx, "Capability injector wired into AgentToolResolver")
	}

	// Wire dynamic KG tool factory so list_<entity_type> / get_<entity_type>
	// derived tool names actually construct runtime tools at chat time. Without
	// this, KG tools resolved by the capability resolver are silently dropped
	// in ResolveForAgent (no factory registered in the builtin store).
	if components.AgentToolResolver != nil && pgDB != nil && kgToolProvider != nil {
		kgEntityRepo := configrepo.NewGORMKGEntityRepository(pgDB)
		kgSchemaRepo := configrepo.NewGORMKGSchemaRepository(pgDB)
		// Route the tool-path EntityReader through kgread.Usecase so it
		// inherits the same hardening as the REST path: schema-bound filter
		// whitelist, range-on-non-numeric rejection, MaxFilterInSize cap,
		// MaxBatchGetIDs cap, and KGQueryTimeout wrap. Without this, a
		// prompt-injected LLM call could bypass every 1.4.0 mitigation
		// (security review KG14-SEC-09).
		kgBundleRepo := configrepo.NewGORMKGBundleRepository(pgDB)
		kgToolUC := kgread.New(
			kgBundleRepo,
			kgSchemaRepo,
			&kgEntityReaderAdapter{repo: kgEntityRepo},
		)
		kgFactory := kgtools.NewAgentToolFactory(
			kgToolProvider,
			&kgEntityReaderForToolFactory{uc: kgToolUC, schemas: kgSchemaRepo},
			kgSchemaRepo,
		)
		components.AgentToolResolver.SetKGToolFactory(kgFactory)
		slog.InfoContext(ctx, "KG tool factory wired into AgentToolResolver")
	}

	// US-006: Wire circuit breaker registry into AgentToolResolver
	var cbRegistry *resilience.CircuitBreakerRegistry
	if components.AgentToolResolver != nil {
		cbRegistry = resilience.NewCircuitBreakerRegistry(resilience.DefaultCircuitBreakerConfig())
		components.AgentToolResolver.SetCircuitBreakerRegistry(&circuitBreakerRegistryAdapter{registry: cbRegistry})
		slog.InfoContext(ctx, "Circuit breaker registry wired into AgentToolResolver")

		// Wire 30s default tool timeout into AgentToolResolver (AC-RESIL-05)
		components.AgentToolResolver.SetToolTimeout(30_000) // 30 seconds in ms
		slog.InfoContext(ctx, "Tool timeout wired into AgentToolResolver", "timeout_ms", 30000)
	}

	// Wire per-agent capability config reader (memory max_entries, knowledge top_k)
	var capReader *capabilityConfigReader
	if pgDB != nil {
		capReader = &capabilityConfigReader{db: pgDB}
		if components.AgentToolResolver != nil {
			components.AgentToolResolver.SetCapabilityConfigReader(capReader)
			slog.InfoContext(ctx, "Capability config reader wired into AgentToolResolver")
		}
	}

	// HTTP REST API server — starts only when bootstrap config is available.
	// Supports two modes:
	//   Single-port (default): all routes on one port (backward compatible)
	//   Two-port: external (data plane) + internal (control plane)
	var httpServer *deliveryhttp.Server         // main server (single-port) or external (two-port)
	var internalHTTPServer *deliveryhttp.Server // nil in single-port mode
	var httpPort int
	var internalHTTPPort int
	var httpAuthMW *deliveryhttp.AuthMiddleware
	var byokMW *deliveryhttp.BYOKMiddleware
	var byokResolver *byokTenantResolver
	if bootstrapCfg != nil {
		httpPort = bootstrapCfg.Engine.Port
		if httpPort == 0 {
			httpPort = 8443
		}
		internalHTTPPort = bootstrapCfg.Engine.InternalPort // 0 = single-port mode

		// AUTH_MODE=local has no real authentication — anyone reaching the
		// listen address gets admin access. Warn loudly when the bind is not
		// loopback so operators don't accidentally expose admin API publicly.
		warnUnsafeLocalBind(ctx, bootstrapCfg.Security.AuthMode, bootstrapCfg.Engine.Host, httpPort)

		if internalHTTPPort > 0 {
			// Two-port mode: external gets configurable CORS, internal gets permissive CORS
			httpServer = deliveryhttp.NewServerWithCORS(httpPort, bootstrapCfg.Engine.CORSOrigins)
			internalHTTPServer = deliveryhttp.NewServer(internalHTTPPort)
		} else {
			// Single-port mode (backward compatible)
			httpServer = deliveryhttp.NewServer(httpPort)
		}
		r := httpServer.Router()
		// internalRouter is the router for management/admin routes.
		// In single-port mode it points to the same router as r.
		// In two-port mode it points to the internal server's router.
		internalRouter := r
		if internalHTTPServer != nil {
			internalRouter = internalHTTPServer.Router()
		}

		// Metrics middleware — records request count and duration for all routes.
		// Applied before auth so every request is instrumented regardless of auth status.
		r.Use(deliveryhttp.MetricsMiddleware)
		if internalHTTPServer != nil {
			internalRouter.Use(deliveryhttp.MetricsMiddleware)
		}

		// Security headers — applied globally before any route so every response
		// carries nosniff/frame-ancestors/CSP/referrer-policy. Widget routes
		// (which must be embeddable) install their own handler later with a
		// per-tenant frame-ancestors allowlist (key widget_embed_origins via settings table).
		r.Use(deliveryhttp.SecurityHeadersMiddleware)
		if internalHTTPServer != nil {
			internalRouter.Use(deliveryhttp.SecurityHeadersMiddleware)
		}

		// Extra HTTP middleware contributed by the plugin (e.g. EdDSA JWT verifier,
		// entitlements). Must be registered before any routes — chi panics otherwise.
		for _, mw := range sc.Plugin.HTTPMiddleware() {
			r.Use(mw)
			if internalHTTPServer != nil {
				internalRouter.Use(mw)
			}
		}

		// Auth — EdDSA verifier in all modes.
		//
		// Local mode: engine generates its own Ed25519 keypair on first boot,
		// signs short-lived admin sessions via POST /auth/local-session.
		// External mode: engine loads a pre-provisioned public key; token
		// issuance is owned by an external issuer.
		// The plugin may override the default verifier entirely — the
		// middleware uses whatever it gets as long as the interface matches.
		var jwtVerifier pluginpkg.JWTVerifier
		var localSessionPrivKey []byte // non-nil in local mode, enables /auth/local-session route below
		if pluginVerifier := sc.Plugin.JWTVerifier(); pluginVerifier != nil {
			jwtVerifier = pluginVerifier
		} else {
			switch bootstrapCfg.Security.AuthMode {
			case config.AuthModeLocal:
				kp, err := auth.LoadOrGenerateKeypair(bootstrapCfg.Security.JWTKeysDir)
				if err != nil {
					return fmt.Errorf("load/generate local jwt keypair: %w", err)
				}
				verifier, err := auth.NewEdDSAVerifier(kp.Public, bootstrapCfg.Security.JWTExpectedAudience)
				if err != nil {
					return fmt.Errorf("build local EdDSA verifier: %w", err)
				}
				jwtVerifier = verifier
				localSessionPrivKey = kp.Private
			case config.AuthModeExternal:
				pub, err := auth.LoadPublicKey(bootstrapCfg.Security.JWTPublicKeyPath)
				if err != nil {
					return fmt.Errorf("load external jwt public key: %w", err)
				}
				verifier, err := auth.NewEdDSAVerifier(pub, bootstrapCfg.Security.JWTExpectedAudience)
				if err != nil {
					return fmt.Errorf("build external EdDSA verifier: %w", err)
				}
				jwtVerifier = verifier
			default:
				return fmt.Errorf("invalid auth_mode %q (expected %q or %q)",
					bootstrapCfg.Security.AuthMode, config.AuthModeLocal, config.AuthModeExternal)
			}
		}
		authMW := deliveryhttp.NewAuthMiddlewareWithVerifier(jwtVerifier, &tokenRepoHTTPAdapter{repo: apiTokenRepo})
		httpAuthMW = authMW

		// V2 §5.8: per-end-user BYOK middleware. The resolver reads the live
		// config from the tenant-scoped `settings` rows on each request (admin UI
		// updates take effect immediately, isolated per tenant) and falls back to
		// the YAML bootstrap when a tenant has no row. sentinelFallback is true
		// only in local auth-mode (the CE token carries no tenant); in external
		// multi-tenant mode an unattributed request fails closed (F7). Mounted
		// after auth on chat / agent endpoints below (N5).
		byokResolver = newBYOKTenantResolver(pgDB, cfg.BYOK,
			bootstrapCfg.Security.AuthMode == config.AuthModeLocal)
		byokMW = deliveryhttp.NewBYOKMiddleware(byokResolver)

		// Audit logger
		auditLogger := audit.NewLogger(pgDB)

		// Update checker (non-blocking, air-gap safe).
		// Endpoint override comes from bootstrap config (env SYNTHETICBREW_VERSIONS_URL).
		updateChecker := versioncheck.NewUpdateChecker(sc.Version, bootstrapCfg.Updates.VersionsURL)
		updateChecker.Start(ctx)

		// Local admin session issuer (public) — only wired in local auth mode.
		// Signs Ed25519 admin sessions with the local keypair generated at
		// boot. External mode never exposes this route; token issuance
		// is owned by an external issuer.
		var localSessionHandler *deliveryhttp.LocalSessionHandler
		if localSessionPrivKey != nil {
			localSessionTTL := bootstrapCfg.Security.LocalSessionTTL
			if localSessionTTL <= 0 {
				localSessionTTL = time.Hour
			}
			localSessionHandler = deliveryhttp.NewLocalSessionHandler(localSessionPrivKey, localSessionTTL)
		}

		// TenantMiddleware enforces presence of tenant_id after auth.
		// In CE mode RequireTenant is false, so requests without tenant_id
		// pass through; multi-tenant setups enable RequireTenant to reject
		// unscoped requests with 403.
		tenantExtractor := deliveryhttp.NewJWTTenantExtractor("tenant_id")
		tenantMW := deliveryhttp.NewTenantMiddleware(tenantExtractor, sc.RequireTenant)

		// Protected management routes + public health/registry/local-session
		// — extracted to registerHTTPRoutes (see routes_register.go).
		registerHTTPRoutes(routesDeps{
			Ctx:                  ctx,
			Version:              sc.Version,
			DB:                   pgDB,
			AgentRegistry:        agentRegistry,
			RegistryMgr:          registryMgr,
			Components:           components,
			TaskRepo:             taskRepo,
			APITokenRepo:         apiTokenRepo,
			KnowledgeRepo:        knowledgeRepo,
			EmbeddingsClient:     embeddingsClient,
			MCPManager:           mcpManager,
			CBRegistry:           cbRegistry,
			LifecycleManager:     lifecycleManager,
			LifecycleDispatcher:  lifecycleDispatcher,
			AgentLifecycleReader: agentLifecycleReader,
			CapReader:            capReader,
			AuthMW:               authMW,
			TenantMW:             tenantMW,
			BYOKMW:               byokMW,
			BYOKResolver:         byokResolver,
			AuditLogger:          auditLogger,
			UpdateChecker:        updateChecker,
			LocalSessionHandler:  localSessionHandler,
			TransportPolicy:      sc.Plugin.TransportPolicy(),
			Plugin:               sc.Plugin,
			ExternalRouter:       r,
			InternalRouter:       internalRouter,
			HasInternalServer:    internalHTTPServer != nil,
			KGToolProvider:       kgToolProvider,
		})

		// Serve Admin Dashboard SPA — internal only (extracted to spa_handler.go).
		mountSPA(internalRouter, "/admin", "/usr/share/syntheticbrew/admin")

		// Coding-agent onboarding instructions — public markdown, the single
		// source of truth the admin's "connect a coding agent" button points
		// fetch-capable agents at.
		{
			setupBaseURL := ""
			if bootstrapCfg != nil {
				setupBaseURL = bootstrapCfg.Engine.PublicBaseURL
			}
			agentSetupHandler := deliveryhttp.NewAgentSetupPromptHandler(setupBaseURL)
			r.Get("/agent-setup/prompt.md", agentSetupHandler.Get)
		}

		// Serve widget.js (static file) — external only (or both in single-port mode).
		// V2: the admin generates a <script src="…/widget.js" data-agent="…" …>
		// snippet client-side (docs/architecture/agent-first-runtime.md §4.3),
		// so only the static bundle is served here — no dynamic /widget/{id}.js
		// bootstrap endpoint and no server-side widget configuration.
		widgetPath := "/usr/share/syntheticbrew/widget/widget.js"
		if _, statErr := os.Stat(widgetPath); statErr == nil {
			widgetFileHandler := func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/javascript")
				w.Header().Set("Cache-Control", "public, max-age=3600")
				http.ServeFile(w, req, widgetPath)
			}
			widgetAdapter := &widgetEmbedOriginsAdapter{repo: configrepo.NewGORMSettingRepository(pgDB)}
			r.Group(func(r chi.Router) {
				// Widget is publicly embeddable — optional auth so that if a
				// caller presents a valid JWT, tenant context is populated and
				// the widget CSP lookup returns the tenant's configured
				// widget_embed_origins. Anonymous callers get frame-ancestors
				// 'none' (safe default: blocks embedding until configured).
				if httpAuthMW != nil {
					r.Use(httpAuthMW.AuthenticateOptional)
				}
				r.Use(deliveryhttp.WidgetSecurityHeadersMiddleware(widgetAdapter))
				r.Get("/widget.js", widgetFileHandler)
			})
			slog.InfoContext(ctx, "Widget served", "path", widgetPath)
		}

		// NOTE: HTTP server start is deferred until after SessionProcessor is created,
		// so the chat endpoint can be wired with all required dependencies.
	}

	// Create event store (PostgreSQL) for reliable event replay on reconnect
	eventStore, err := eventstore.New(pgDB)
	if err != nil {
		return fmt.Errorf("create event store: %w", err)
	}

	// Create session registry for server-streaming API and bridge
	sessionRegistry := flowregistry.NewSessionRegistry(eventStore)

	// Engine components are always available
	// Use AgentRegistry as FlowProvider (replaces legacy FlowManager for agent resolution).
	// In multi-tenant mode agentRegistry is nil and we must dispatch per-request via the
	// Manager, otherwise the static FlowManager has no agents (flows.yaml is empty).
	flowResolution := agentregistry.ResolveFlowProvider(agentRegistry, registryMgr, components.FlowManager)
	flowProvider := flowResolution.Provider
	tenantAwareProvider := flowResolution.TenantAware
	agentModelResolver := agentregistry.ResolveAgentModelResolver(agentRegistry, tenantAwareProvider)

	factory := turnexecutorfactory.New(
		components.Engine,
		flowProvider,
		components.AgentToolResolver,
		components.ModelSelector,
		components.AgentConfig,
		components.AgentPoolAdapter,
		func() []turnexecutor.ContextReminderProvider {
			if components.AgentService != nil {
				return components.AgentService.GetContextReminders()
			}
			return nil
		},
		components.ModelCache,
		agentModelResolver,
	)

	// Wire the deployment egress policy into the untrusted BYOK model path.
	factory.SetEgressPolicy(sc.Plugin.EgressPolicy())

	// Wire agent UUID resolver so engine execution context uses uuid FK, not agent name.
	if agentRegistry != nil {
		factory.SetAgentUUIDResolver(agentRegistry)
		loggerInstance.InfoContext(ctx, "AgentUUIDResolver wired into TurnExecutorFactory")
	} else if tenantAwareProvider != nil {
		factory.SetAgentUUIDResolver(tenantAwareProvider)
		loggerInstance.InfoContext(ctx, "Tenant-aware AgentUUIDResolver wired into TurnExecutorFactory")
	}

	// Wire memory storage into factory for memory_recall/memory_store tools (US-001 Memory capability)
	if pgDB != nil {
		memStorage := persistence.NewMemoryStorage(pgDB)
		factory.SetMemory(memStorage, memStorage, 0) // maxEntries=0 means unlimited
		loggerInstance.InfoContext(ctx, "Memory storage wired into TurnExecutorFactory")

		// BUG-007: Wire schema resolver so memory/knowledge tools get SchemaID (UUID).
		factory.SetSchemaResolver(&agentSchemaIDResolver{db: pgDB})
		loggerInstance.InfoContext(ctx, "Schema resolver wired into TurnExecutorFactory")

		// Wire EngineTaskManager so agents use DB-backed tasks (visible in Admin)
		factory.SetEngineTaskManager(components.TaskManager)
		loggerInstance.InfoContext(ctx, "EngineTaskManager wired into TurnExecutorFactory")

		// V2: triggers-driven task fan-out (cron/webhook) removed. The background
		// task worker still runs so agents that spawn sub-tasks through the unified
		// task manager continue to be picked up; just no scheduler on top of it.

		// Wire per-agent capability config reader for memory max_entries
		if capReader != nil {
			factory.SetCapabilityConfigReader(capReader)
		}
	}

	// HITL interrupt state-tracker repo — wired only when DB is available.
	// Shared between SessionProcessor (writes rows when interrupt_request events
	// are emitted) and the chat HTTP adapter (reads + resolves on resume).
	var interruptRepo *configrepo.GORMInterruptRepository
	if pgDB != nil {
		interruptRepo = configrepo.NewGORMInterruptRepository(pgDB)
	}

	// Create shared SessionProcessor
	var interruptCreator sessionprocessor.InterruptCreator
	if interruptRepo != nil {
		interruptCreator = interruptRepo
	}
	sessProcessor := sessionprocessor.New(sessionRegistry, factory, eventStore, interruptCreator)

	// Wire TurnExecutorFactory into poolBasedRunner so chat agents delegated via
	// lifecycle.Manager use the SSE path instead of the code-agent pool path.
	if poolRunner != nil {
		poolRunner.SetChatFactory(factory)
		slog.InfoContext(ctx, "TurnExecutorFactory wired into poolBasedRunner for chat agent delegation")
	}

	// Background task worker: picks up tasks created by agents (e.g. via
	// spawn_agent) and runs them in parallel through the session processor.
	// V2: cron/webhook trigger scheduler on top of this is deferred to V3.
	if taskRepo != nil {
		taskExecutor := taskrunner.NewTaskExecutor(
			components.TaskManager,
			sessionRegistry,
			sessProcessor,
			0, // 0 → DefaultTaskTimeout
		)
		if taskWorker := taskrunner.StartBackgroundWorker(taskExecutor, 4); taskWorker != nil {
			defer taskWorker.Stop()
		}
	}

	// Wire up agent pool if available (multi-agent mode)
	if components.AgentPool != nil && components.AgentPoolAdapter != nil {
		// SessionStorage removed (V2 Group N: runtime_sessions table dropped).
		sessProcessor.SetAgentPoolRegistrar(components.AgentPool)
		// Re-wire AgentPool with AgentRegistry as FlowProvider (replaces legacy FlowManager)
		// so spawned agents can resolve flows from DB, not just YAML
		if agentRegistry != nil {
			components.AgentPool.SetEngine(
				components.Engine, agentRegistry,
				components.AgentToolResolver, components.ToolDepsProvider,
				components.ModelCache, agentRegistry,
			)
			components.AgentPool.SetModelResolver(agentRegistry, components.ModelCache)
		}
		loggerInstance.InfoContext(ctx, "Multi-agent mode enabled (Supervisor + Code Agents)")
	} else {
		loggerInstance.InfoContext(ctx, "Single-agent mode (no WorkStorage)")
	}

	// Wire chat endpoint and start HTTP server(s) now that SessionProcessor is ready.
	// In multi-tenant mode agentRegistry is nil (per-tenant registries loaded on demand),
	// but we still register the routes so auth middleware can reject unauthenticated requests.
	if httpServer != nil && (agentRegistry != nil || registryMgr != nil) {
		chatService := &chatServiceHTTPAdapter{
			registry:    sessionRegistry,
			processor:   sessProcessor,
			agents:      agentRegistry,
			registryMgr: registryMgr,
			chatEnabled: components.AgentService != nil || components.ModelCache != nil,
		}
		// Set the BYOK resolver only when present — assigning a nil concrete
		// pointer to the interface field would make it non-nil and panic on call.
		if byokResolver != nil {
			chatService.byok = byokResolver
		}
		var schemaRepoForChat *configrepo.GORMSchemaRepository
		if pgDB != nil {
			schemaRepoForChat = configrepo.NewGORMSchemaRepository(pgDB)
			chatService.schemas = schemaRepoForChat
			chatService.sessions = configrepo.NewGORMSessionRepository(pgDB)
			chatService.interrupts = interruptRepo
			chatService.eventStore = eventStore
			chatService.history = repository.NewMessageRepositoryImpl(pgDB)
			// Usage-limit enforcement: gate a turn before it runs and settle
			// the counters when it completes. Both stores are tenant-scoped
			// (resolve the tenant from context). Wired only with a DB.
			chatService.usage = usagelimit.New(
				configrepo.NewGORMUsageLimitRepository(pgDB),
				configrepo.NewGORMUsageCounterRepository(pgDB),
				nil,
			)
			chatService.accumulator = usageAccumulator
			// Active-users gate: caps DISTINCT end users per rolling window
			// (policy-driven, plugin seam writes only). Applies to BYOK turns
			// too — it limits platform activity, not model spend.
			var activeUsersFloor activeusers.FloorProvider
			if f := sc.Plugin.ActiveUsersFloor(); f != nil {
				activeUsersFloor = f
			}
			chatService.activeUsers = activeusers.New(
				configrepo.NewGORMTenantPolicyRepository(pgDB),
				configrepo.NewGORMActiveUserRepository(pgDB),
				nil,
				activeUsersFloor,
			)
		}
		chatHandler := deliveryhttp.NewChatHandler(chatService, schemaRepoForChat, forwardHeadersStore.GetForContext)

		// Admin assistant — admin JWT required, chats against the seeded
		// builder-schema; the schema resolver runs per-request so a late seed
		// is picked up without a restart. The tenant-aware lookup body lives
		// in builder_assistant.go (`NewBuilderSchemaResolver`) so the SCC-02
		// gate has its own test surface.
		builderSchemaResolver := NewBuilderSchemaResolver(pgDB, builderSchemaName)
		adminAssistantSessionRepo := configrepo.NewGORMSessionRepository(pgDB)
		adminAssistantHandler := deliveryhttp.NewAdminAssistantHandler(chatService, builderSchemaResolver, forwardHeadersStore.GetForContext, adminAssistantSessionRepo)

		// Widget bootstrap config — reads the widget_attribution policy through
		// the tenant-scoped policy repo. Without a DB the handler is wired with a
		// nil reader and attribution resolves to false.
		var widgetConfigHandler *deliveryhttp.WidgetConfigHandler
		if pgDB != nil {
			widgetConfigHandler = deliveryhttp.NewWidgetConfigHandler(configrepo.NewGORMTenantPolicyRepository(pgDB))
		} else {
			widgetConfigHandler = deliveryhttp.NewWidgetConfigHandler(nil)
		}

		chatDeps := chatRoutesDeps{
			AuthMW:                httpAuthMW,
			BYOKMW:                byokMW,
			ChatHandler:           chatHandler,
			AdminAssistantHandler: adminAssistantHandler,
			WidgetConfigHandler:   widgetConfigHandler,
			AgentManagerExt: &agentManagerHTTPAdapter{
				repo:        configrepo.NewGORMAgentRepository(pgDB),
				registry:    agentRegistry,
				registryMgr: registryMgr,
				db:          pgDB,
				schemaRepo:  configrepo.NewGORMSchemaRepository(pgDB),
				kbRepo:      configrepo.NewGORMKnowledgeBaseRepository(pgDB),
			},
		}

		// Chat API + admin assistant on external port (or single-port).
		mountChatRoutes(httpServer.Router(), chatDeps)
		mountAdminAssistantRoutes(httpServer.Router(), chatDeps)
		// In two-port mode, also register on internal port for the embeddable widget,
		// and expose a read-only agent list on the external router.
		if internalHTTPServer != nil {
			mountChatRoutes(internalHTTPServer.Router(), chatDeps)
			mountAdminAssistantRoutes(internalHTTPServer.Router(), chatDeps)
			mountSecondaryAgentList(httpServer.Router(), chatDeps)
		}

	}

	// Start HTTP server(s) — independent of agentRegistry (multi-tenant mode has no singleton).
	if httpServer != nil {
		go func() {
			if err := httpServer.Start(); err != nil && err != http.ErrServerClosed {
				slog.ErrorContext(context.Background(), "HTTP server error", "error", err)
			}
		}()
		if internalHTTPServer != nil {
			go func() {
				if err := internalHTTPServer.Start(); err != nil && err != http.ErrServerClosed {
					slog.ErrorContext(context.Background(), "Internal HTTP server error", "error", err)
				}
			}()
			slog.InfoContext(ctx, "Two-port mode enabled",
				"external_port", httpPort, "internal_port", internalHTTPPort)
		} else {
			slog.InfoContext(ctx, "HTTP REST API server started", "port", httpPort)
		}
	}

	loggerInstance.InfoContext(ctx, "SyntheticBrew Server started successfully",
		"host", cfg.Server.Host,
		"http_port", httpPort,
		"internal_port", internalHTTPPort,
	)

	// Write port file for CLI discovery BEFORE emitting READY.
	portFileHost := cfg.Server.Host
	if portFileHost == "" || portFileHost == "0.0.0.0" {
		portFileHost = "127.0.0.1"
	}
	portWriter := portfile.NewWriter(dataDir)
	if err := portWriter.Write(portfile.PortInfo{
		PID:          os.Getpid(),
		HTTPPort:     httpPort,
		InternalPort: internalHTTPPort,
		Host:         portFileHost,
		StartedAt:    time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		slog.WarnContext(context.Background(), "Failed to write port file", "error", err)
	} else {
		slog.InfoContext(context.Background(), "Port file written", "path", portWriter.Path())
	}

	// In managed mode, emit READY protocol AFTER port file is written.
	if sc.Managed {
		readyPort := httpPort
		if readyPort == 0 {
			readyPort = internalHTTPPort
		}
		fmt.Printf("READY:%d\n", readyPort)
		os.Stdout.Sync()
	}

	// Start memory retention cleanup goroutine (deletes expired entries based on per-agent config)
	if pgDB != nil {
		memorysvc.NewRetentionWorker(pgDB).Start(ctx)
	}

	// Wait for shutdown signal
	sig := <-sigChan
	loggerInstance.InfoContext(ctx, "Received shutdown signal", "signal", sig)
	cancel()

	loggerInstance.InfoContext(ctx, "Shutting down SyntheticBrew Server...")

	// Stop plugin resources — no-op in CE.
	sc.Plugin.Stop()
	slog.InfoContext(context.Background(), "plugin stopped")

	// Close MCP client connections — fans out CloseAll across every
	// per-tenant ClientRegistry the Manager has accumulated.
	mcpManager.Shutdown()
	slog.InfoContext(context.Background(), "MCP clients closed")

	// Remove port file on shutdown
	if err := portWriter.Remove(); err != nil {
		slog.WarnContext(context.Background(), "Failed to remove port file", "error", err)
	}

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if httpServer != nil {
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			slog.WarnContext(context.Background(), "HTTP server shutdown error", "error", err)
		}
	}
	if internalHTTPServer != nil {
		if err := internalHTTPServer.Shutdown(shutdownCtx); err != nil {
			slog.WarnContext(context.Background(), "Internal HTTP server shutdown error", "error", err)
		}
	}

	loggerInstance.InfoContext(ctx, "SyntheticBrew Server stopped")
	return nil
}

// UserDataDir is a thin alias around platform.UserDataDir kept here to avoid
// churn at every call site (managed-mode init, port-file path, etc.). The
// actual platform path resolution — and the only OS env reads outside
// pkg/config — live in internal/infrastructure/platform.
func UserDataDir() (string, error) {
	return platform.UserDataDir()
}

// ensureManagedDirs creates the required subdirectories in the data directory.
func ensureManagedDirs(dataDir string) error {
	dirs := []string{
		filepath.Join(dataDir, "logs"),
		filepath.Join(dataDir, "data"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}
	return nil
}

// isLoopbackBind reports whether host binds the HTTP listener to the local
// machine only. Empty string is Go's net.Listen "all interfaces" default and
// is treated as non-loopback (which is why AUTH_MODE=local with an empty
// host triggers the public-bind warning).
func isLoopbackBind(host string) bool {
	switch host {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
}

// warnUnsafeLocalBind emits a startup WARN when AUTH_MODE=local is paired with
// a non-loopback bind. Extracted from Run() so the gate is unit-testable
// against a captured slog handler.
func warnUnsafeLocalBind(ctx context.Context, authMode, host string, port int) {
	if authMode != config.AuthModeLocal {
		return
	}
	if isLoopbackBind(host) {
		return
	}
	slog.WarnContext(ctx,
		"AUTH_MODE=local with non-loopback bind — admin API is unauthenticated; anyone reaching this address has admin access. Use AUTH_MODE=external for production or restrict bind to 127.0.0.1.",
		"listen_host", host,
		"listen_port", port,
	)
}
