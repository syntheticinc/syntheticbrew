package agentregistry

import (
	"context"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// FlowProvider is defined here (not in turnexecutor) to avoid an import cycle:
// turnexecutor imports tools, which imports this package. The shape is
// structurally identical to turnexecutor.FlowProvider.
type FlowProvider interface {
	GetFlow(ctx context.Context, agentName string) (*domain.Flow, error)
}

// AgentModelResolver is defined here (not in turnexecutorfactory) to avoid an
// import cycle. The shape is structurally identical to
// turnexecutorfactory.AgentModelResolver.
type AgentModelResolver interface {
	ResolveModelID(ctx context.Context, agentName string) *string
}

// FlowProviderResolution bundles the active FlowProvider with its tenant-aware
// counterpart (when applicable) so the caller can wire both into downstream
// components without re-implementing the single-vs-multi-tenant decision tree.
type FlowProviderResolution struct {
	// Provider is the FlowProvider to plumb into the turnexecutor factory.
	// It is the AgentRegistry in single-tenant mode, the TenantAwareFlowProvider
	// in multi-tenant mode, or the supplied fallback when both are nil.
	Provider FlowProvider

	// TenantAware is non-nil only in multi-tenant mode (registry is nil but
	// manager exists). Callers that need the AgentUUIDResolver / AgentModelResolver
	// in multi-tenant mode pull them from this struct.
	TenantAware *TenantAwareFlowProvider
}

// ResolveFlowProvider picks the right FlowProvider for the current deployment
// shape:
//
//   - registry != nil → single-tenant CE: use the registry directly.
//   - registry == nil && manager != nil → multi-tenant: build a
//     TenantAwareFlowProvider and use it.
//   - both nil → degraded mode: return the fallback so the engine can still
//     boot against the static FlowManager (yaml flows).
func ResolveFlowProvider(
	registry *AgentRegistry,
	manager *Manager,
	fallback FlowProvider,
) FlowProviderResolution {
	if registry != nil {
		return FlowProviderResolution{Provider: registry}
	}
	if manager != nil {
		tenantAware := NewTenantAwareFlowProvider(manager)
		return FlowProviderResolution{
			Provider:    tenantAware,
			TenantAware: tenantAware,
		}
	}
	return FlowProviderResolution{Provider: fallback}
}

// ResolveAgentModelResolver returns the AgentModelResolver matching the
// FlowProvider chosen by ResolveFlowProvider:
//
//   - single-tenant: registry implements ResolveModelID directly.
//   - multi-tenant: tenantAware dispatches to the per-tenant registry.
//   - degraded: nil (factory tolerates a nil resolver).
func ResolveAgentModelResolver(
	registry *AgentRegistry,
	tenantAware *TenantAwareFlowProvider,
) AgentModelResolver {
	if registry != nil {
		return registry
	}
	if tenantAware != nil {
		return tenantAware
	}
	return nil
}
