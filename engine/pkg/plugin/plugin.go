// Package plugin defines the runtime extension point for SyntheticBrew engine.
//
// CE (Community Edition) builds use plugin.Noop{} by default — all extension
// points are silently skipped. External modules (shipped separately) can
// implement Plugin and pass it to pkg/app.ServerRun to extend CE behavior
// without modifying CE source.
package plugin

import (
	"context"
	"errors"
	"net/http"

	"github.com/cloudwego/eino/components/model"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
)

// ErrSchemaQuotaExceeded is returned by Plugin.OnSchemaCreate when creating
// the requested schemas would exceed the tenant's configured limit. Callers
// surface it as a payment-required condition instead of a generic failure.
var ErrSchemaQuotaExceeded = errors.New("schema quota exceeded")

// ErrDocumentQuotaExceeded is returned by Plugin.OnDocumentCreate when adding
// the requested knowledge documents would exceed the tenant's configured limit.
// Callers surface it as a payment-required / rejected condition instead of a
// generic failure.
var ErrDocumentQuotaExceeded = errors.New("knowledge document quota exceeded")

// ModelSelectorConfigurator is a minimal interface for registering models.
// Implemented by *llm.ModelSelector (internal CE type).
type ModelSelectorConfigurator interface {
	SetModel(agentName string, m model.ToolCallingChatModel, displayName string)
	// SetDefault replaces the fallback model used when an agent has no
	// per-agent model. A plugin uses it to install a process-wide default.
	SetDefault(m model.ToolCallingChatModel, displayName string)
}

// stepsLimitKey is the private context key used to propagate the per-tenant
// step limit from the external plugin's entitlements middleware to the step callback.
type stepsLimitKey struct{}

// WithStepsLimit returns ctx with the monthly step limit attached. Called by
// the external plugin's entitlements middleware so the CE step callback can
// read the limit without importing the external plugin's types.
func WithStepsLimit(ctx context.Context, limit int) context.Context {
	return context.WithValue(ctx, stepsLimitKey{}, limit)
}

// StepsLimitFromContext returns the step limit stored in ctx, or 0 if none
// was set. 0 means no enforcement (CE mode or missing entitlements).
func StepsLimitFromContext(ctx context.Context) int {
	v, _ := ctx.Value(stepsLimitKey{}).(int)
	return v
}

// Plugin is the runtime extension point. CE uses Noop by default.
//
// Implementations plug custom JWT verification, HTTP middleware, additional
// routes, gRPC interceptors, and session admission checks into the server
// without touching its internal assembly code.
type Plugin interface {
	// JWTVerifier returns a custom JWT verifier. Nil means use the CE default
	// (HMAC shared-secret verifier from auth_middleware).
	JWTVerifier() JWTVerifier

	// HTTPMiddleware returns extra middleware to attach to the main HTTP
	// router, in order. Return nil for none.
	HTTPMiddleware() []func(http.Handler) http.Handler

	// RegisterHTTP mounts extra HTTP routes. mainRouter is the external/data
	// plane router; internalRouter is the management/admin plane router.
	// In single-port mode the two routers are the same object.
	RegisterHTTP(mainRouter chi.Router, internalRouter chi.Router)

	// GRPCServerOptions returns extra gRPC server options (interceptors,
	// credentials, etc.) to append to the CE chain.
	GRPCServerOptions() []grpc.ServerOption

	// CheckSessionAllowed reports whether a new session may start.
	// Returns "" to allow; non-empty reason rejects the session.
	CheckSessionAllowed(ctx context.Context) string

	// OnAgentStep is invoked by the runtime after every agent step. Plugins
	// use it to report per-step usage and to enforce configured limits.
	// stepsLimit is the monthly cap read from context by the CE callback
	// (0 means no enforcement). Returns ErrStepsQuotaExceeded when the
	// tenant's monthly budget is exhausted; the caller cancels the request
	// context so Eino aborts subsequent steps.
	//
	// An empty tenantID means the call is outside any tenant scope
	// (CE/self-hosted); implementations should no-op and return nil.
	OnAgentStep(ctx context.Context, tenantID string, stepsLimit int) error

	// OnSchemaCreate is invoked before the engine persists new user-facing
	// schemas — single creations pass n=1, batch operations (e.g. a template
	// fork) pass the whole batch size so the decision covers the batch
	// atomically. Returning ErrSchemaQuotaExceeded (or any error) aborts the
	// creation. System bootstrap paths (seeding) do not call it.
	//
	// An empty tenantID means the call is outside any tenant scope
	// (CE/self-hosted); implementations should no-op and return nil.
	OnSchemaCreate(ctx context.Context, tenantID string, n int) error

	// OnDocumentCreate is invoked before the engine persists new knowledge
	// documents — single uploads pass n=1. It is called inside the shared
	// upload service so every ingest path (REST upload and the knowledge tools)
	// is gated at one point. Returning ErrDocumentQuotaExceeded (or any error)
	// aborts the ingest. System bootstrap paths (seeding) do not call it.
	//
	// An empty tenantID means the call is outside any tenant scope
	// (CE/self-hosted); implementations should no-op and return nil.
	OnDocumentCreate(ctx context.Context, tenantID string, n int) error

	// SetTenantSeeder installs a callback the plugin can invoke when it
	// accepts a tenant-provisioning request. The engine wires a seeder backed
	// by its schema/agent repositories so that a plugin's provisioning can
	// populate a freshly-created tenant with default data without importing
	// engine internals. CE's Noop ignores the seeder.
	SetTenantSeeder(seeder TenantSeeder)

	// SetSchemaCounter installs a callback the plugin can call to count
	// schemas visible to the tenant in the current request context. An external
	// plugin's quota middleware uses it to enforce SchemasLimit without issuing an
	// internal HTTP sub-request to itself — the earlier sub-request design
	// hard-coded the loopback port and silently failed (fail-open) whenever
	// the engine ran on a non-default port (hosted deployments bind 8443, the
	// sub-request targeted 9555). CE's Noop ignores the counter because it
	// has no quota middleware.
	SetSchemaCounter(counter SchemaCounter)

	// SetUsageLimitWriter installs a callback a provisioning plugin can invoke
	// to install a default usage limit on a freshly-created tenant. The engine
	// wires a writer backed by its tenant-scoped usage-limit repository so the
	// plugin can set the cap without importing engine internals or the tenant
	// context key. CE's Noop ignores it because it has no provisioning endpoint.
	SetUsageLimitWriter(writer UsageLimitWriter)

	// SetEmbeddingModelWriter installs a callback a provisioning plugin can
	// invoke to install a default embedding model on a freshly-created tenant.
	// The engine wires a writer backed by its tenant-scoped model repository so
	// the plugin can provision the model without importing engine internals or
	// the tenant context key — the same "use the engine's real code path"
	// contract as SetUsageLimitWriter. CE's Noop ignores it because it has no
	// provisioning endpoint.
	SetEmbeddingModelWriter(writer EmbeddingModelWriter)

	// SetTenantPolicyWriter installs a callback a plugin can invoke to write
	// protected per-tenant policy entries. The engine wires a writer backed by
	// its tenant-scoped policy repository so the plugin can upsert/delete
	// entries without importing engine internals or the tenant context key.
	// CE's Noop ignores it because nothing writes policies in CE.
	SetTenantPolicyWriter(writer TenantPolicyWriter)

	// SetTenantPolicyReader installs a callback a plugin can invoke to read
	// protected per-tenant policy entries. The engine wires a reader backed by
	// its tenant-scoped policy repository. CE's Noop ignores it because
	// nothing reads policies in CE.
	SetTenantPolicyReader(reader TenantPolicyReader)

	// SetKnowledgeDocumentCounter installs a callback the plugin can call to
	// count knowledge documents belonging to the tenant, mirroring
	// SetSchemaCounter: an in-process count instead of an internal HTTP
	// sub-request. CE's Noop ignores the counter because it has no consumer.
	SetKnowledgeDocumentCounter(counter KnowledgeDocumentCounter)

	// TransportPolicy returns the MCP transport policy for this deployment.
	// CE / bare-metal deployments return PermissiveTransportPolicy (all
	// transports allowed). Managed / multi-tenant deployments return
	// RestrictedTransportPolicy (stdio blocked to prevent host code execution).
	TransportPolicy() TransportPolicy

	// EgressPolicy returns the outbound-LLM egress policy for this deployment.
	// CE / bare-metal deployments return PermissiveEgressPolicy (all
	// destinations allowed — a self-hosted operator may target internal hosts).
	// Managed / multi-tenant deployments return a tightened policy (deny private
	// ranges + hostname allowlist). Never nil — the untrusted BYOK path composes
	// this with a hardcoded deny-private baseline it can never relax.
	EgressPolicy() EgressPolicy

	// PrepareModelSelector is called once at server startup. The plugin may
	// register per-agent models on the selector; CE's Noop leaves the
	// selector untouched so all agents use the default BYOK model.
	PrepareModelSelector(selector ModelSelectorConfigurator, byok model.ToolCallingChatModel)

	// UsageExtras returns additional fields to merge into GET /api/v1/usage.
	// CE's Noop returns nil so only the built-in counters are exposed.
	UsageExtras(ctx context.Context, tenantID string) map[string]any

	// DocsMCPEndpoint returns an optional URL for a hosted Docs MCP server to
	// install in seed data. CE's Noop returns "" so seed does not create a
	// Docs MCP entry.
	DocsMCPEndpoint() string

	// KGEnforcer returns the optional limit enforcer for
	// Knowledge Graph entity writes. Nil means no enforcement (the CE default).
	// Engine fail-closes on enforcer errors — quota cannot be bypassed.
	KGEnforcer() KGEnforcer

	// KGCounter returns the optional counter source for tenant-level KG
	// stats (bundles count, entities count). Used in the admin UI bundles
	// header and usage displays. Nil means the engine reads counts
	// directly from the database without extra enrichment.
	KGCounter() KGCounter

	// ActiveUsersFloor returns the optional floor provider for the
	// distinct-user gate. Nil — the CE default — means no floor: a missing
	// active_users_limit policy leaves the gate unlimited, which is correct for
	// self-hosted CE. A plugin returns a provider to supply a fallback limit so
	// a missing/malformed policy row cannot silently disable the gate.
	ActiveUsersFloor() ActiveUsersFloor

	// EmbedderFor optionally returns a custom Embedder for a knowledge model
	// resolved from the DB (base URL, API key, model name, dimension). It lets a
	// plugin route embedding generation for a specific model over its own
	// channel instead of the engine's built-in OpenAI-compatible client — keyed
	// off an opaque base-URL marker the engine passes through verbatim and never
	// interprets.
	//
	// Returning (nil, false) — the CE Noop default — tells the engine to fall
	// back to its built-in client. The engine passes the DB-resolved fields
	// verbatim and never interprets baseURL itself, so the marker convention is
	// entirely the plugin's concern.
	EmbedderFor(ctx context.Context, baseURL, apiKey, model string, dim int) (Embedder, bool)

	// Stop releases any background resources held by the plugin
	// (watchers, tickers, etc.).
	Stop()
}

// Embedder generates vector embeddings for text. It mirrors the engine's
// built-in embedding client signature (both single and batch) so a
// plugin-provided embedder is structurally interchangeable at both the
// knowledge upload service's ingest interface (EmbedBatch) and the
// knowledge_search tool's query interface (Embed). Primitive types only — no
// engine-internal imports — so pkg/plugin stays free of a dependency cycle on
// the knowledge service.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// SchemaCounter returns the number of schemas belonging to tenantID. The
// engine wires a concrete counter over its schema repository; quota
// middleware calls it instead of issuing an internal HTTP sub-request that
// would require port discovery and a round-trip through the entire
// middleware chain.
//
// tenantID is passed explicitly rather than read from ctx so the plugin
// does not need to know about CE's internal tenant context key — the
// engine-side counter applies its own tenant scoping.
//
// A non-nil error means "counting failed" — the plugin decides whether to
// fail-open (let the write through, log a warning) or fail-closed depending
// on policy. Empty tenantID should yield (0, nil) — CE / single-tenant mode
// has no quota enforcement surface.
type SchemaCounter interface {
	CountSchemas(ctx context.Context, tenantID string) (int, error)
}

// SchemaCounterFunc adapts a plain function to the SchemaCounter interface so
// callers can wire an inline closure without declaring a new type.
type SchemaCounterFunc func(ctx context.Context, tenantID string) (int, error)

// CountSchemas implements SchemaCounter.
func (f SchemaCounterFunc) CountSchemas(ctx context.Context, tenantID string) (int, error) {
	return f(ctx, tenantID)
}

// Usage-limit vocabulary exposed to plugins. These mirror the engine's
// internal domain constants but live in the public plugin package so an
// external plugin can name a scope/unit without importing engine internals.
// The engine writer validates the incoming values, so a drift from the
// internal constants surfaces as an error rather than a silent mis-write.
const (
	UsageScopeTenant = "tenant"
	UsageScopeUser   = "per_user"
	UsageUnitTurns   = "turns"
	UsageUnitSteps   = "steps"
)

// Tenant-policy vocabulary exposed to plugins. The key constants mirror the
// engine's internal domain constants but live in the public plugin package so
// an external plugin can name a policy without importing engine internals.
// The engine writer validates the incoming key, so a drift from the internal
// constants surfaces as an error rather than a silent mis-write.
const (
	PolicySystemPromptPrefix      = "system_prompt_prefix"
	PolicyWidgetAttribution       = "widget_attribution"
	PolicyActiveUsersLimit        = "active_users_limit"
	PolicyActiveUsersMode         = "active_users_mode"
	PolicyKnowledgeDocumentsLimit = "knowledge_documents_limit"
	PolicySchemasLimit            = "schemas_limit"
)

// Canonical values for on/off toggle policies and for gate-mode policies.
const (
	PolicyValueOn     = "on"
	PolicyValueOff    = "off"
	PolicyModeEnforce = "enforce"
	PolicyModeMonitor = "monitor"
)

// TenantPolicyWriter writes protected per-tenant policy entries. The engine
// wires a concrete writer over its tenant-scoped policy repository; a plugin
// calls it to install or update policy values for a tenant.
//
// SetPolicy is a full upsert — unlike UsageLimitWriter.EnsureLimit, which is
// write-once, SetPolicy overwrites an existing value for the key so a plugin
// can move a tenant between policy states at any time. DeletePolicy removes
// the entry; deleting an absent key is not an error.
//
// tenantID is passed explicitly rather than read from ctx so the plugin does
// not need to know CE's internal tenant context key — the engine-side writer
// applies its own tenant scoping.
type TenantPolicyWriter interface {
	SetPolicy(ctx context.Context, tenantID, key, value string) error
	DeletePolicy(ctx context.Context, tenantID, key string) error
}

// TenantPolicyReader reads protected per-tenant policy entries in bulk. The
// engine wires a concrete reader over its tenant-scoped policy repository.
//
// GetPolicies returns a key→value map for the requested keys; keys with no
// configured entry are simply absent from the map. An empty tenantID yields
// (nil, nil) — CE / single-tenant mode has no policy surface.
type TenantPolicyReader interface {
	GetPolicies(ctx context.Context, tenantID string, keys []string) (map[string]string, error)
}

// KnowledgeDocumentCounter returns the number of knowledge documents
// belonging to tenantID. The engine wires a concrete counter over its
// knowledge repository, mirroring SchemaCounter: an in-process count instead
// of an internal HTTP sub-request.
//
// tenantID is passed explicitly rather than read from ctx so the plugin does
// not need to know about CE's internal tenant context key — the engine-side
// counter applies its own tenant scoping.
//
// A non-nil error means "counting failed" — the plugin decides whether to
// fail-open or fail-closed depending on policy. Empty tenantID should yield
// (0, nil) — CE / single-tenant mode has no enforcement surface.
type KnowledgeDocumentCounter interface {
	CountKnowledgeDocuments(ctx context.Context, tenantID string) (int, error)
}

// KnowledgeDocumentCounterFunc adapts a plain function to the
// KnowledgeDocumentCounter interface so callers can wire an inline closure
// without declaring a new type.
type KnowledgeDocumentCounterFunc func(ctx context.Context, tenantID string) (int, error)

// CountKnowledgeDocuments implements KnowledgeDocumentCounter.
func (f KnowledgeDocumentCounterFunc) CountKnowledgeDocuments(ctx context.Context, tenantID string) (int, error) {
	return f(ctx, tenantID)
}

// UsageLimitWriter installs a default usage limit on a tenant. The engine
// wires a concrete writer over its usage-limit repository; a provisioning
// plugin calls it inside the request handler so the write runs in the new
// tenant's context.
//
// tenantID is passed explicitly rather than read from ctx so the plugin does
// not need to know CE's internal tenant context key — the engine-side writer
// applies its own tenant scoping.
type UsageLimitWriter interface {
	// EnsureLimit writes a usage limit for tenantID's scope ONLY when none is
	// configured yet — it never overwrites an existing limit, so re-provisioning
	// a tenant (or one whose limit an operator has since changed) is safe.
	// Returns whether a row was written. A non-nil error means the write failed;
	// the caller decides whether that should fail provisioning.
	EnsureLimit(ctx context.Context, tenantID, scope, unit string, limitValue, intervalSeconds int64) (bool, error)
}

// EmbeddingModelWriter installs a default embedding model on a tenant. The
// engine wires a concrete writer over its model repository; a provisioning
// plugin calls it inside the request handler so the write runs in the new
// tenant's context.
//
// tenantID is passed explicitly rather than read from ctx so the plugin does
// not need to know CE's internal tenant context key — the engine-side writer
// applies its own tenant scoping. baseURL carries the plugin's routing marker
// (the engine stores it verbatim and never interprets it); the row is written
// with no API key.
type EmbeddingModelWriter interface {
	// EnsureEmbeddingModel writes an embedding model for tenantID ONLY when the
	// tenant has no embedding-kind model yet — it never overwrites or duplicates,
	// so re-provisioning a tenant (or one that has since configured its own
	// embedding model) is safe. Returns whether a row was written. A non-nil
	// error means the write failed; the caller decides whether that should fail
	// provisioning. name is the plugin-supplied row display name — the engine
	// does not choose it, so no use-case vocabulary lives in the generic writer.
	EnsureEmbeddingModel(ctx context.Context, tenantID, name, modelName, baseURL string, dim int) (bool, error)
}

// TenantSeeder populates a freshly-created tenant with default data.
//
// The engine constructs a concrete seeder over its config repositories (schema,
// agent, model) and hands it to the plugin via Plugin.SetTenantSeeder at
// startup. Plugins that accept provisioning requests call SeedTenant inside
// the request handler so seeding runs in the tenant's context, using the
// engine's real code paths rather than reimplementing them.
type TenantSeeder interface {
	// SeedTenant creates the minimum viable tenant bootstrap (typically a
	// default schema + entry agent) so the new tenant can use the product
	// immediately after sign-up. Returns a descriptive error on failure —
	// provisioning callers are expected to propagate it back to the client
	// rather than silently continue with an empty tenant.
	SeedTenant(ctx context.Context, tenantID, plan string) error
}
