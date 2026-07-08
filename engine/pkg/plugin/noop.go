package plugin

import (
	"context"
	"net/http"

	"github.com/cloudwego/eino/components/model"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
)

// Noop is the default CE Plugin: it adds nothing to the server.
//
// All extension points return zero values, so the server uses its built-in
// defaults (HMAC JWT, no extra middleware, no extra routes, no extra gRPC
// options, and no session admission rule).
type Noop struct{}

// JWTVerifier returns nil — the server uses the default HMAC verifier.
func (Noop) JWTVerifier() JWTVerifier { return nil }

// HTTPMiddleware returns no extra middleware.
func (Noop) HTTPMiddleware() []func(http.Handler) http.Handler { return nil }

// RegisterHTTP mounts no extra routes.
func (Noop) RegisterHTTP(chi.Router, chi.Router) {}

// GRPCServerOptions returns no extra gRPC options.
func (Noop) GRPCServerOptions() []grpc.ServerOption { return nil }

// CheckSessionAllowed always allows the session.
func (Noop) CheckSessionAllowed(context.Context) string { return "" }

// OnAgentStep is a no-op. CE has no billing/metering surface.
func (Noop) OnAgentStep(context.Context, string, int) error { return nil }

// SetTenantSeeder is a no-op. CE has no provisioning endpoint, so there is
// nothing to wire the seeder into.
func (Noop) SetTenantSeeder(TenantSeeder) {}

// SetSchemaCounter is a no-op. CE has no quota middleware, so the counter
// has no consumer. Defined for Plugin-interface completeness.
func (Noop) SetSchemaCounter(SchemaCounter) {}

// SetUsageLimitWriter is a no-op. CE has no provisioning endpoint, so there is
// no writer to wire.
func (Noop) SetUsageLimitWriter(UsageLimitWriter) {}

// SetTenantPolicyWriter is a no-op. Nothing writes tenant policies in CE.
func (Noop) SetTenantPolicyWriter(TenantPolicyWriter) {}

// SetTenantPolicyReader is a no-op. Nothing reads tenant policies in CE.
func (Noop) SetTenantPolicyReader(TenantPolicyReader) {}

// SetKnowledgeDocumentCounter is a no-op. CE has no consumer for the counter.
func (Noop) SetKnowledgeDocumentCounter(KnowledgeDocumentCounter) {}

// TransportPolicy returns PermissiveTransportPolicy — CE allows all transports.
func (Noop) TransportPolicy() TransportPolicy { return PermissiveTransportPolicy{} }

// PrepareModelSelector is a no-op. CE has no per-agent model selection — all
// agents use the default BYOK model.
func (Noop) PrepareModelSelector(_ ModelSelectorConfigurator, _ model.ToolCallingChatModel) {}

// UsageExtras returns nil. CE exposes only the built-in counters via GET
// /api/v1/usage; there are no extra billing fields to surface.
func (Noop) UsageExtras(_ context.Context, _ string) map[string]any { return nil }

// DocsMCPEndpoint returns "". CE seed data does not include a Docs MCP entry.
func (Noop) DocsMCPEndpoint() string { return "" }

// KGEnforcer returns nil — CE has no quota enforcement for Knowledge Graphs.
// EE and Cloud plugins override to enforce plan limits.
func (Noop) KGEnforcer() KGEnforcer { return nil }

// KGCounter returns nil — CE reads counts directly from the database.
func (Noop) KGCounter() KGCounter { return nil }

// Stop is a no-op.
func (Noop) Stop() {}
