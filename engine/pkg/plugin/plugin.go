// Package plugin defines the runtime extension point for SyntheticBrew engine.
//
// CE (Community Edition) builds use plugin.Noop{} by default — all extension
// points are silently skipped. External modules (shipped separately) can
// implement Plugin and pass it to pkg/app.ServerRun to extend CE behavior
// without modifying CE source.
package plugin

import (
	"context"
	"net/http"

	"github.com/cloudwego/eino/components/model"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
)

// ModelSelectorConfigurator is a minimal interface for registering per-agent models.
// Implemented by *llm.ModelSelector (internal CE type).
type ModelSelectorConfigurator interface {
	SetModel(agentName string, m model.ToolCallingChatModel, displayName string)
}

// stepsLimitKey is the private context key used to propagate the per-tenant
// step limit from EE entitlements middleware to the step callback.
type stepsLimitKey struct{}

// WithStepsLimit returns ctx with the monthly step limit attached. Called by
// EE's entitlementsMiddleware so the CE step callback can read the limit
// without importing EE types.
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
	// use it to report usage for billing/metering and to enforce quotas.
	// stepsLimit is the monthly cap read from context by the CE callback
	// (0 means no enforcement). Returns ErrStepsQuotaExceeded when the
	// tenant's monthly budget is exhausted; the caller cancels the request
	// context so Eino aborts subsequent steps.
	//
	// An empty tenantID means the call is outside any tenant scope
	// (CE/self-hosted); implementations should no-op and return nil.
	OnAgentStep(ctx context.Context, tenantID string, stepsLimit int) error

	// SetTenantSeeder installs a callback the plugin can invoke when it
	// accepts a tenant-provisioning request. The engine wires a seeder backed
	// by its schema/agent repositories so that EE provisioning can populate a
	// freshly-created tenant with default data without importing engine
	// internals. CE's Noop ignores the seeder.
	SetTenantSeeder(seeder TenantSeeder)

	// SetSchemaCounter installs a callback the plugin can call to count
	// schemas visible to the tenant in the current request context. EE's
	// quota middleware uses it to enforce SchemasLimit without issuing an
	// internal HTTP sub-request to itself — the earlier sub-request design
	// hard-coded the loopback port and silently failed (fail-open) whenever
	// the engine ran on a non-default port (Cloud containers bind 8443, the
	// sub-request targeted 9555). CE's Noop ignores the counter because it
	// has no quota middleware.
	SetSchemaCounter(counter SchemaCounter)

	// TransportPolicy returns the MCP transport policy for this deployment.
	// CE / bare-metal deployments return PermissiveTransportPolicy (all
	// transports allowed). Cloud / managed deployments return
	// RestrictedTransportPolicy (stdio blocked to prevent host code execution).
	TransportPolicy() TransportPolicy

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

	// Stop releases any background resources held by the plugin
	// (watchers, tickers, etc.).
	Stop()
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
