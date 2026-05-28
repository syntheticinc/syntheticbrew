package app

import (
	"context"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	deliveryhttp "github.com/syntheticinc/syntheticbrew/internal/delivery/http"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agentregistry"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/audit"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/indexing"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/kgtools"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/knowledge"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm/registry"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/mcp"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/versioncheck"
	svcknowledge "github.com/syntheticinc/syntheticbrew/internal/service/knowledge"
	"github.com/syntheticinc/syntheticbrew/internal/service/lifecycle"
	mcpcatalog "github.com/syntheticinc/syntheticbrew/internal/service/mcp"
	"github.com/syntheticinc/syntheticbrew/internal/service/resilience"
	svcschematemplate "github.com/syntheticinc/syntheticbrew/internal/service/schematemplate"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/kgapply"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/kgmutate"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/kgread"
	ucschematemplate "github.com/syntheticinc/syntheticbrew/internal/usecase/schematemplate"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
	pluginpkg "github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// routesDeps bundles the dependencies passed to registerHTTPRoutes.
type routesDeps struct {
	Ctx                  context.Context
	Version              string
	DB                   *gorm.DB
	AgentRegistry        *agentregistry.AgentRegistry
	RegistryMgr          *agentregistry.Manager
	Components           *InfraComponents
	TaskRepo             *configrepo.GORMTaskRepository
	APITokenRepo         *configrepo.GORMAPITokenRepository
	KnowledgeRepo        *configrepo.GORMKnowledgeRepository
	KnowledgeIndexer     *knowledge.Indexer
	EmbeddingsClient     *indexing.EmbeddingsClient
	MCPManager           *mcp.Manager
	CBRegistry           *resilience.CircuitBreakerRegistry
	LifecycleManager     *lifecycle.Manager
	LifecycleDispatcher  *lifecycle.Dispatcher
	AgentLifecycleReader AgentLifecycleReader
	CapReader            *capabilityConfigReader
	AuthMW               *deliveryhttp.AuthMiddleware
	TenantMW             *deliveryhttp.TenantMiddleware
	BYOKMW               *deliveryhttp.BYOKMiddleware
	AuditLogger          *audit.Logger
	UpdateChecker        *versioncheck.UpdateChecker
	LocalSessionHandler  *deliveryhttp.LocalSessionHandler
	TransportPolicy      mcpcatalog.TransportPolicy
	Plugin               pluginpkg.Plugin
	BYOKConfig           config.BYOKConfig
	ExternalRouter       chi.Router
	InternalRouter       chi.Router
	HasInternalServer    bool // true in two-port mode
	// KnowledgeDataDir is the directory under which uploaded knowledge files
	// are stored. Sourced from BootstrapConfig.Knowledge.DataDir
	// (env DATA_DIR). Empty string defaults to "data".
	KnowledgeDataDir string
	// KGToolProvider is the per-tenant Knowledge Graph tool resolver shared
	// between the strategy registry (capability dispatch) and the apply usecase
	// (collision detection). Constructed once in server.go.
	KGToolProvider *kgtools.Provider
}

// registerHTTPRoutes registers the public health/registry/local-session
// endpoints on the appropriate routers, then mounts the protected
// management routes (auth + tenant + audit middleware) on the internal
// router.
func registerHTTPRoutes(deps routesDeps) {
	// Local aliases for concise use inside the body.
	ctx := deps.Ctx
	pgDB := deps.DB
	agentRegistry := deps.AgentRegistry
	registryMgr := deps.RegistryMgr
	components := deps.Components
	taskRepo := deps.TaskRepo
	apiTokenRepo := deps.APITokenRepo
	knowledgeRepo := deps.KnowledgeRepo
	knowledgeIndexer := deps.KnowledgeIndexer
	mcpManager := deps.MCPManager
	cbRegistry := deps.CBRegistry
	lifecycleManager := deps.LifecycleManager
	lifecycleDispatcher := deps.LifecycleDispatcher
	agentLifecycleReader := deps.AgentLifecycleReader
	authMW := deps.AuthMW
	tenantMW := deps.TenantMW
	byokMW := deps.BYOKMW
	auditLogger := deps.AuditLogger
	updateChecker := deps.UpdateChecker
	localSessionHandler := deps.LocalSessionHandler
	transportPolicy := deps.TransportPolicy
	plugin := deps.Plugin
	byokFallbackCfg := deps.BYOKConfig
	r := deps.ExternalRouter
	internalRouter := deps.InternalRouter

	// Health (public) — available on both ports
	healthHandler := deliveryhttp.NewHealthHandler(deps.Version, &agentCounterHTTPAdapter{registry: agentRegistry})
	healthHandler.SetUpdateChecker(updateChecker)
	r.Get("/api/v1/health", healthHandler.ServeHTTP)

	// Model registry (public — read-only catalog, no auth needed)
	modelRegistry := registry.New()
	registryHandler := deliveryhttp.NewModelRegistryHandler(modelRegistry)

	if deps.HasInternalServer {
		// Two-port mode: register public routes on internal router too
		internalRouter.Get("/api/v1/health", healthHandler.ServeHTTP)
		internalRouter.Get("/api/v1/models/registry", registryHandler.List)
		internalRouter.Get("/api/v1/models/registry/providers", registryHandler.ListProviders)
		if localSessionHandler != nil {
			internalRouter.Post("/api/v1/auth/local-session", localSessionHandler.Issue)
			internalRouter.Post("/api/v1/auth/local-session/refresh", localSessionHandler.Refresh)
		}
	}
	// Single-port or external: model registry + local session on main router
	r.Get("/api/v1/models/registry", registryHandler.List)
	r.Get("/api/v1/models/registry/providers", registryHandler.ListProviders)
	if localSessionHandler != nil {
		r.Post("/api/v1/auth/local-session", localSessionHandler.Issue)
		r.Post("/api/v1/auth/local-session/refresh", localSessionHandler.Refresh)
	}

	// Protected management routes — on internalRouter (= r in single-port mode)
	internalRouter.Group(func(r chi.Router) {
		r.Use(authMW.Authenticate)
		r.Use(tenantMW.Handler)
		r.Use(deliveryhttp.AuditMiddleware(&auditHTTPAdapter{logger: auditLogger}))

		// Schema repo (created early because agent manager needs it for used_in_schemas)
		schemaRepo := configrepo.NewGORMSchemaRepository(pgDB)

		// Agents
		agentRepo := configrepo.NewGORMAgentRepository(pgDB)
		// kbRepo is used by PatchAgent/CreateAgent to apply
		// knowledge_base_ids changes to the knowledge_base_agents M2M table.
		agentKBRepo := configrepo.NewGORMKnowledgeBaseRepository(pgDB)
		agentManager := &agentManagerHTTPAdapter{repo: agentRepo, registry: agentRegistry, registryMgr: registryMgr, db: pgDB, schemaRepo: schemaRepo, kbRepo: agentKBRepo}
		agentHandler := deliveryhttp.NewAgentHandlerWithManager(agentManager)
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeAgentsRead))
			r.Get("/api/v1/agents", agentHandler.List)
			r.Get("/api/v1/agents/{name}", agentHandler.Get)
		})
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeAgentsWrite))
			r.Post("/api/v1/agents", agentHandler.Create)
			r.Put("/api/v1/agents/{name}", agentHandler.Update)
			r.Patch("/api/v1/agents/{name}", agentHandler.Patch)
			r.Delete("/api/v1/agents/{name}", agentHandler.Delete)
		})

		// Agent Lifecycle
		if lifecycleManager != nil && agentLifecycleReader != nil {
			lifecycleProvider := newLifecycleHTTPAdapter(lifecycleManager, agentLifecycleReader)
			lifecycleHandler := deliveryhttp.NewLifecycleHandler(lifecycleProvider)
			r.Group(func(r chi.Router) {
				r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeAgentsRead))
				r.Get("/api/v1/agents/{name}/lifecycle", lifecycleHandler.Status)
			})
		}

		// Agent Capabilities
		capRepo := configrepo.NewGORMCapabilityRepository(pgDB)
		capHandler := deliveryhttp.NewCapabilityHandler(&capabilityServiceHTTPAdapter{repo: capRepo, registryMgr: registryMgr})
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeAgentsRead))
			r.Get("/api/v1/agents/{name}/capabilities", capHandler.List)
		})
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeAgentsWrite))
			r.Post("/api/v1/agents/{name}/capabilities", capHandler.Add)
			r.Put("/api/v1/agents/{name}/capabilities/{capId}", capHandler.Update)
			r.Delete("/api/v1/agents/{name}/capabilities/{capId}", capHandler.Remove)
		})

		// Models
		llmProviderRepo := configrepo.NewGORMLLMProviderRepository(pgDB)
		modelService := &modelServiceHTTPAdapter{repo: llmProviderRepo, modelCache: components.ModelCache, agentRepo: agentRepo}
		modelHandler := deliveryhttp.NewModelHandler(modelService)
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeModelsRead))
			r.Get("/api/v1/models", modelHandler.List)
		})
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeModelsWrite))
			r.Post("/api/v1/models", modelHandler.Create)
			r.Put("/api/v1/models/{name}", modelHandler.Update)
			r.Patch("/api/v1/models/{name}", modelHandler.Patch)
			r.Delete("/api/v1/models/{name}", modelHandler.Delete)
			r.Post("/api/v1/models/{name}/verify", modelHandler.Verify)
		})

		// Tasks
		taskHandler := deliveryhttp.NewTaskHandler(&taskServiceHTTPAdapter{
			repo:          taskRepo,
			manager:       components.TaskManager,
			sessionReader: configrepo.NewGORMSessionRepository(pgDB),
		})
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeTasks))
			r.Post("/api/v1/tasks", taskHandler.Create)
			r.Get("/api/v1/tasks", taskHandler.List)
			r.Get("/api/v1/tasks/{id}", taskHandler.Get)
			r.Delete("/api/v1/tasks/{id}", taskHandler.Cancel)
			r.Get("/api/v1/tasks/{id}/subtasks", taskHandler.ListSubtasks)
			r.Post("/api/v1/tasks/{id}/approve", taskHandler.Approve)
			r.Post("/api/v1/tasks/{id}/start", taskHandler.Start)
			r.Post("/api/v1/tasks/{id}/complete", taskHandler.Complete)
			r.Post("/api/v1/tasks/{id}/fail", taskHandler.Fail)
			r.Post("/api/v1/tasks/{id}/priority", taskHandler.SetPriority)
		})

		// Dispatch Tasks (lifecycle dispatcher queries) — gated by ScopeTasks
		// at the middleware layer plus per-user ACL inside the handler
		// (extractSessionACL → SessionOwnerReader). Cross-user reads return
		// 404 so dispatch routes can't be used to enumerate session UUIDs.
		if lifecycleDispatcher != nil {
			dispatchSessionRepo := configrepo.NewGORMSessionRepository(pgDB)
			dispatchHandler := deliveryhttp.NewDispatchHandler(lifecycleDispatcher, dispatchSessionRepo)
			r.Group(func(r chi.Router) {
				r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeTasks))
				r.Get("/api/v1/dispatch/tasks/{taskId}", dispatchHandler.Get)
				r.Get("/api/v1/sessions/{sessionId}/dispatch-tasks", dispatchHandler.ListBySession)
			})
		}

		// Config
		configImportExport := &configImportExportHTTPAdapter{db: pgDB}
		configHandler := deliveryhttp.NewConfigHandler(
			&configReloaderHTTPAdapter{registry: agentRegistry, mcpManager: mcpManager, db: pgDB, transportPolicy: transportPolicy},
			configImportExport,
		)
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeConfig))
			r.Post("/api/v1/config/reload", configHandler.Reload)
			r.Post("/api/v1/config/import", configHandler.Import)
			r.Get("/api/v1/config/export", configHandler.Export)
		})

		// Knowledge
		if knowledgeRepo != nil {
			var reindexer deliveryhttp.KnowledgeReindexer
			if knowledgeIndexer != nil {
				reindexer = &knowledgeReindexerHTTPAdapter{
					indexer:  knowledgeIndexer,
					registry: agentRegistry,
				}
			}
			kbRepo := configrepo.NewGORMKnowledgeBaseRepository(pgDB)
			knowledgeHandler := deliveryhttp.NewKnowledgeHandler(
				&knowledgeStatsHTTPAdapter{repo: knowledgeRepo, kbRepo: kbRepo},
				reindexer,
			)

			dataDir := deps.KnowledgeDataDir
			if dataDir == "" {
				dataDir = "data"
			}

			uploadSvc := svcknowledge.NewUploadService(knowledgeRepo, dataDir)
			uploadSvc.SetEmbeddingResolver(&embeddingModelResolver{db: pgDB})
			uploadSvc.SetKBEmbeddingResolver(&kbEmbeddingResolver{db: pgDB})
			knowledgeHandler.SetFileUploader(&knowledgeUploadHTTPAdapter{svc: uploadSvc})
			knowledgeHandler.SetFileLister(&knowledgeFileListerHTTPAdapter{svc: uploadSvc, kbRepo: kbRepo})

			// Knowledge Bases (many-to-many) handler
			kbHandler := deliveryhttp.NewKnowledgeBaseHandler(
				&kbStoreAdapter{repo: kbRepo, db: pgDB},
				&kbFileManagerAdapter{svc: uploadSvc},
				kbRepo,
			)

			// Legacy agent-scoped knowledge endpoints
			r.Group(func(r chi.Router) {
				r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeAgentsRead))
				r.Get("/api/v1/agents/{name}/knowledge/status", knowledgeHandler.Status)
				r.Get("/api/v1/agents/{name}/knowledge/files", knowledgeHandler.ListFiles)
			})
			r.Group(func(r chi.Router) {
				r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeAgentsWrite))
				r.Post("/api/v1/agents/{name}/knowledge/reindex", knowledgeHandler.Reindex)
				r.Post("/api/v1/agents/{name}/knowledge/files", knowledgeHandler.UploadFile)
				r.Delete("/api/v1/agents/{name}/knowledge/files/{file_id}", knowledgeHandler.DeleteFile)
				r.Post("/api/v1/agents/{name}/knowledge/files/{file_id}/reindex", knowledgeHandler.ReindexFile)
			})

			// Knowledge Base CRUD + file management endpoints
			r.Group(func(r chi.Router) {
				r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeAgentsRead))
				r.Get("/api/v1/knowledge-bases", kbHandler.List)
				r.Get("/api/v1/knowledge-bases/{name}", kbHandler.Get)
				r.Get("/api/v1/knowledge-bases/{name}/files", kbHandler.ListFiles)
				r.Get("/api/v1/knowledge-bases/{name}/files/{file_id}", kbHandler.GetFile)
			})
			r.Group(func(r chi.Router) {
				r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeAgentsWrite))
				r.Post("/api/v1/knowledge-bases", kbHandler.Create)
				r.Put("/api/v1/knowledge-bases/{name}", kbHandler.Update)
				r.Patch("/api/v1/knowledge-bases/{name}", kbHandler.PatchKB)
				r.Delete("/api/v1/knowledge-bases/{name}", kbHandler.Delete)
				r.Post("/api/v1/knowledge-bases/{name}/agents/{agent_name}", kbHandler.LinkAgent)
				r.Delete("/api/v1/knowledge-bases/{name}/agents/{agent_name}", kbHandler.UnlinkAgent)
				r.Post("/api/v1/knowledge-bases/{name}/files", kbHandler.UploadFile)
				r.Delete("/api/v1/knowledge-bases/{name}/files/{file_id}", kbHandler.DeleteFile)
				r.Post("/api/v1/knowledge-bases/{name}/files/{file_id}/reindex", kbHandler.ReindexFile)
			})

			// Knowledge Graphs (Этап 2 — engine 1.3.0). Wire repos → usecases →
			// HTTP adapters → handlers, all behind the standard tenant-scoped
			// middleware. The kgToolProvider was constructed earlier in server.go
			// so the strategy registry already references it.
			var kgReadHandler *deliveryhttp.KGReadHandler
			var kgMutateHandler *deliveryhttp.KGMutateHandler
			{
				kgBundleRepo := configrepo.NewGORMKGBundleRepository(pgDB)
				kgSchemaRepo := configrepo.NewGORMKGSchemaRepository(pgDB)
				kgEntityRepo := configrepo.NewGORMKGEntityRepository(pgDB)
				kgTxRunner := configrepo.NewGORMTransactionRunner(pgDB)

				kgValidator := kgtools.NewSchemaValidator()
				kgCollision := kgtools.NewCollisionDetector(
					// Engine builtins: capability tools that ship with the engine
					// and the spawn helpers. New entity_types that would generate
					// any of these names are rejected at apply time.
					kgtools.StaticToolNames{Names: []string{
						"memory_recall", "memory_store",
						"knowledge_search",
						"spawn_agent", "spawn_async", "spawn_sync",
					}},
					// In-memory KG registry — catches collisions for bundles that
					// have already served chat traffic in this engine process.
					kgtools.RegistryToolNames{Provider: deps.KGToolProvider},
					// Authoritative cross-bundle source — reads persisted schemas
					// across the tenant so collisions are caught immediately at
					// apply time, even on a freshly started engine.
					kgtools.DBSchemaToolNames{Lister: kgSchemaRepo},
				)

				// Quota enforcer adapter — turns a plugin.KGEnforcer (nullable)
				// into the usecase consumer-side interface. Nil enforcer is
				// passed through as nil so the usecase short-circuits the gate.
				var applyEnforcer kgapply.QuotaEnforcer
				var mutateEnforcer kgmutate.QuotaEnforcer
				if pe := deps.Plugin.KGEnforcer(); pe != nil {
					applyEnforcer = &kgEnforcerAdapter{enforcer: pe}
					mutateEnforcer = &kgEnforcerAdapter{enforcer: pe}
				}

				applyUC := kgapply.New(
					kgBundleRepo, kgSchemaRepo, kgEntityRepo,
					kgValidator, kgCollision, applyEnforcer,
					&kgAdvisoryLockerNoop{}, kgTxRunner,
				)
				readUC := kgread.New(kgBundleRepo, kgSchemaRepo, &kgEntityReaderAdapter{repo: kgEntityRepo})
				mutateUC := kgmutate.New(
					kgBundleRepo, kgSchemaRepo, kgEntityRepo,
					kgValidator, kgCollision, mutateEnforcer, kgTxRunner,
				)

				// Wire bundle invalidator so apply/mutate flush the per-tenant
				// kgtools cache. Without this, in-flight chat sessions and new
				// sessions in the same process would see stale tool lists
				// until the engine restarts.
				kgInvalidator := &kgProviderInvalidatorAdapter{provider: deps.KGToolProvider}
				applyUC.SetInvalidator(kgInvalidator)
				mutateUC.SetInvalidator(kgInvalidator)

				// Wire KG into /config/import + /config/export so the existing
				// GitOps endpoint round-trips bundles alongside agents/models/mcp.
				configImportExport.SetKnowledgeGraphs(applyUC, kgBundleRepo, kgSchemaRepo, kgEntityRepo)

				kgReadHandler = deliveryhttp.NewKGReadHandler(newKGReadHTTPAdapter(readUC))
				kgMutateHandler = deliveryhttp.NewKGMutateHandler(newKGMutateHTTPAdapter(applyUC, mutateUC))
			}
			if kgReadHandler != nil && kgMutateHandler != nil {
				r.Group(func(r chi.Router) {
					r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeAgentsRead))
					r.Get("/api/v1/knowledge-graphs", kgReadHandler.ListBundles)
					r.Get("/api/v1/knowledge-graphs/{bundle}", kgReadHandler.GetBundle)
					r.Get("/api/v1/knowledge-graphs/{bundle}/schemas", kgReadHandler.ListSchemas)
					r.Get("/api/v1/knowledge-graphs/{bundle}/schemas/{entity_type}", kgReadHandler.GetSchema)
					r.Get("/api/v1/knowledge-graphs/{bundle}/entities/{entity_type}", kgReadHandler.ListEntities)
					r.Get("/api/v1/knowledge-graphs/{bundle}/entities/{entity_type}/{id}", kgReadHandler.GetEntity)
				})
				r.Group(func(r chi.Router) {
					r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeAgentsWrite))
					r.Post("/api/v1/knowledge-graphs/{bundle}/import", kgMutateHandler.BulkImport)
					r.Post("/api/v1/knowledge-graphs/{bundle}/entities/{entity_type}", kgMutateHandler.CreateEntity)
					r.Put("/api/v1/knowledge-graphs/{bundle}/entities/{entity_type}/{id}", kgMutateHandler.UpdateEntity)
					r.Delete("/api/v1/knowledge-graphs/{bundle}/entities/{entity_type}/{id}", kgMutateHandler.DeleteEntity)
					r.Put("/api/v1/knowledge-graphs/{bundle}/schemas/{entity_type}", kgMutateHandler.UpsertSchema)
					r.Delete("/api/v1/knowledge-graphs/{bundle}", kgMutateHandler.DeleteBundle)
				})
			}
		}

		auditRepo := configrepo.NewGORMAuditRepository(pgDB)
		auditHandler := deliveryhttp.NewAuditHandler(&auditServiceHTTPAdapter{repo: auditRepo})
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeAuditRead))
			r.Get("/api/v1/audit", auditHandler.List)
		})

		// API Tokens (admin-only)
		tokenHandler := deliveryhttp.NewTokenHandler(&tokenRepoHTTPAdapter{repo: apiTokenRepo})
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireAdminSession)
			r.Post("/api/v1/auth/tokens", tokenHandler.CreateToken)
			r.Get("/api/v1/auth/tokens", tokenHandler.ListTokens)
			r.Delete("/api/v1/auth/tokens/{id}", tokenHandler.DeleteToken)
		})

		// MCP Servers
		mcpServerRepo := configrepo.NewGORMMCPServerRepository(pgDB)
		mcpHandler := deliveryhttp.NewMCPHandler(&mcpServiceHTTPAdapter{repo: mcpServerRepo, mcpManager: mcpManager}, transportPolicy)
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeMCPRead))
			r.Get("/api/v1/mcp-servers", mcpHandler.List)
		})
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeMCPWrite))
			r.Post("/api/v1/mcp-servers", mcpHandler.Create)
			r.Put("/api/v1/mcp-servers/{name}", mcpHandler.Update)
			r.Patch("/api/v1/mcp-servers/{name}", mcpHandler.Patch)
			r.Delete("/api/v1/mcp-servers/{name}", mcpHandler.Delete)
			r.Post("/api/v1/mcp-servers/{name}/refresh", mcpHandler.Refresh)
		})

		// Schemas (with agent_relations). Chat access on a schema is
		// controlled by schemas.chat_enabled; edge graph lives in
		// agent_relations (source→target delegation).
		agentRelationRepo := configrepo.NewGORMAgentRelationRepository(pgDB)
		schemaHandler := deliveryhttp.NewSchemaHandler(
			&schemaServiceHTTPAdapter{repo: schemaRepo, db: pgDB},
			&agentRelationServiceHTTPAdapter{repo: agentRelationRepo, agentRepo: agentRepo, schemaRepo: schemaRepo, db: pgDB},
			schemaRepo,
		)
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeSchemasRead))
			r.Get("/api/v1/schemas", schemaHandler.ListSchemas)
			r.Get("/api/v1/schemas/{name}", schemaHandler.GetSchema)
			r.Get("/api/v1/schemas/{name}/agents", schemaHandler.ListSchemaAgents)
			r.Get("/api/v1/schemas/{name}/agent-relations", schemaHandler.ListAgentRelations)
			r.Get("/api/v1/schemas/{name}/agent-relations/{relationId}", schemaHandler.GetAgentRelation)
		})
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeSchemasWrite))
			r.Post("/api/v1/schemas", schemaHandler.CreateSchema)
			r.Put("/api/v1/schemas/{name}", schemaHandler.UpdateSchema)
			r.Patch("/api/v1/schemas/{name}", schemaHandler.PatchSchema)
			r.Delete("/api/v1/schemas/{name}", schemaHandler.DeleteSchema)
			// Schema membership is derived from agent_relations —
			// create or remove a delegation relation to add or remove a member.
			r.Post("/api/v1/schemas/{name}/agent-relations", schemaHandler.CreateAgentRelation)
			r.Put("/api/v1/schemas/{name}/agent-relations/{relationId}", schemaHandler.UpdateAgentRelation)
			r.Delete("/api/v1/schemas/{name}/agent-relations/{relationId}", schemaHandler.DeleteAgentRelation)
		})

		// No /api/v1/widgets routes: the admin UI generates embed snippets client-side.

		// Settings — split read / write to keep least-privilege.
		settingRepo := configrepo.NewGORMSettingRepository(pgDB)
		settingHandler := deliveryhttp.NewSettingHandler(&settingServiceHTTPAdapter{
			repo:         settingRepo,
			byokMW:       byokMW,
			db:           pgDB,
			byokFallback: byokFallbackCfg,
		})
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeSettingsRead))
			r.Get("/api/v1/settings", settingHandler.List)
		})
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeSettingsWrite))
			r.Put("/api/v1/settings/{key}", settingHandler.Update)
		})

		// Builder-assistant restore (admin-only)
		baHandler := deliveryhttp.NewBuilderAssistantHandler(&builderAssistantRestorerAdapter{db: pgDB, registry: agentRegistry})
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireAdminSession)
			r.Post("/api/v1/admin/builder-assistant/restore", baHandler.Restore)
		})

		// Sessions — split read / write under granular scopes. Replaces the
		// pre-1.1.4 RequireAdminSession gate that rejected any api_token.
		// Per-user ACL hardening lives inside session_handler (extractSessionACL):
		// trusted-proxy api_token actors and ScopeAdmin pass through tenant-wide;
		// regular end-user JWT actors are auto-filtered to their own user_sub.
		sessionRepo := configrepo.NewGORMSessionRepository(pgDB)
		messageRepo := configrepo.NewGORMEventRepository(pgDB)
		sessionHandler := deliveryhttp.NewSessionHandler(&sessionServiceHTTPAdapter{repo: sessionRepo, messageRepo: messageRepo, db: pgDB})
		sessionHandler.SetEventService(&eventServiceHTTPAdapter{repo: messageRepo})
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeSessionsRead))
			r.Get("/api/v1/sessions", sessionHandler.List)
			r.Get("/api/v1/sessions/{id}", sessionHandler.Get)
			r.Get("/api/v1/sessions/{id}/messages", sessionHandler.ListMessages)
		})
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeSessionsWrite))
			r.Post("/api/v1/sessions", sessionHandler.Create)
			r.Put("/api/v1/sessions/{id}", sessionHandler.Update)
			r.Delete("/api/v1/sessions/{id}", sessionHandler.Delete)
		})

		// Tool metadata — read-only registry of builtin tools.
		toolMetaHandler := deliveryhttp.NewToolMetadataHandler(&toolMetadataHTTPAdapter{})
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeToolsRead))
			r.Get("/api/v1/tools/metadata", toolMetaHandler.List)
		})

		// Memory (per-schema)
		memoryStorage := persistence.NewMemoryStorage(pgDB)
		memoryHandler := deliveryhttp.NewMemoryHandler(
			&memoryListerHTTPAdapter{storage: memoryStorage},
			&memoryClearerHTTPAdapter{storage: memoryStorage},
			schemaRepo,
		)
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeSchemasRead))
			r.Get("/api/v1/schemas/{name}/memory", memoryHandler.ListMemories)
		})
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeSchemasWrite))
			r.Delete("/api/v1/schemas/{name}/memory", memoryHandler.ClearMemories)
			r.Delete("/api/v1/schemas/{name}/memory/{entry_id}", memoryHandler.DeleteMemory)
		})

		// MCP Catalog (read-only) — DB-backed.
		if pgDB != nil {
			catalogRepo := configrepo.NewGORMMCPCatalogRepository(pgDB)
			catalogSvc := mcpcatalog.NewCatalogService(catalogRepo)
			catalogHandler := deliveryhttp.NewCatalogHandler(catalogSvc)
			r.Get("/api/v1/mcp/catalog", catalogHandler.ListCatalog)
		}

		// Schema templates catalog + fork — DB-backed. Reads are open to any
		// authenticated user; fork requires schemas-write scope.
		if pgDB != nil {
			tmplRepo := configrepo.NewGORMSchemaTemplateRepository(pgDB)
			forkSvc := svcschematemplate.NewForkService(pgDB, tmplRepo)
			forkAdapter := svcschematemplate.NewUsecaseForkerAdapter(forkSvc)
			tmplUC := ucschematemplate.New(tmplRepo, forkAdapter)
			tmplHandler := deliveryhttp.NewSchemaTemplateHandler(tmplUC, "1.0")
			r.Get("/api/v1/schema-templates", tmplHandler.List)
			r.Get("/api/v1/schema-templates/{name}", tmplHandler.Get)
			r.Group(func(r chi.Router) {
				r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeSchemasWrite))
				r.Post("/api/v1/schema-templates/{name}/fork", tmplHandler.Fork)
			})
		}

		// Usage
		usageHandler := deliveryhttp.NewUsageHandler(pgDB, plugin)
		r.Get("/api/v1/usage", usageHandler.GetUsage)

		// Tool Call Log — per-tool-call observability for debugging agent behavior.
		toolCallRepoOSS := configrepo.NewToolCallEventRepository(pgDB)
		toolCallLogHandlerOSS := deliveryhttp.NewToolCallLogHandler(&toolCallLogHTTPAdapter{repo: toolCallRepoOSS})
		r.Get("/api/v1/audit/tool-calls", toolCallLogHandlerOSS.List)

		// Resilience — circuit breaker observability + reset.
		// Read = list breakers (low-risk metric); Write = reset (operational mutation).
		resilienceHandler := deliveryhttp.NewResilienceHandler(
			&circuitBreakerQuerierHTTPAdapter{registry: cbRegistry},
		)
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeResilienceRead))
			r.Get("/api/v1/resilience/circuit-breakers", resilienceHandler.ListCircuitBreakers)
		})
		r.Group(func(r chi.Router) {
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeResilienceWrite))
			r.Post("/api/v1/resilience/circuit-breakers/{name}/reset", resilienceHandler.ResetCircuitBreaker)
		})

		// capRepo is used for capability CRUD HTTP handlers.
	})

	// Extra HTTP routes contributed by the plugin (metrics, rate-limit
	// usage, etc.). Noop plugin registers nothing.
	plugin.RegisterHTTP(r, internalRouter)
	_ = ctx
}
