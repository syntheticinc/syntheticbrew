package agentregistry

import (
	"context"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// TenantAwareFlowProvider adapts a Manager to the turnexecutor.FlowProvider
// interface by resolving the per-tenant AgentRegistry from the request context.
// In single-tenant (CE) mode the Manager returns its singleton Registry, so
// this type works transparently there as well.
type TenantAwareFlowProvider struct {
	mgr *Manager
}

// NewTenantAwareFlowProvider creates a FlowProvider backed by a Manager.
func NewTenantAwareFlowProvider(mgr *Manager) *TenantAwareFlowProvider {
	return &TenantAwareFlowProvider{mgr: mgr}
}

// GetFlow looks up the flow for agentName in the registry associated with
// the tenant carried by ctx. Returns an error if the registry cannot be
// resolved or the agent is not registered for the tenant.
func (p *TenantAwareFlowProvider) GetFlow(ctx context.Context, agentName string) (*domain.Flow, error) {
	if p.mgr == nil {
		return nil, fmt.Errorf("tenant-aware flow provider: manager is nil")
	}
	reg, err := p.mgr.GetForContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve tenant registry: %w", err)
	}
	return reg.GetFlow(ctx, agentName)
}

// ResolveAgentUUID implements turnexecutorfactory.AgentUUIDResolver and
// lifecycle.AgentUUIDResolver by dispatching to the tenant registry.
// Returns "" when the tenant context is missing or the agent is unknown.
func (p *TenantAwareFlowProvider) ResolveAgentUUID(ctx context.Context, agentName string) string {
	if p.mgr == nil {
		return ""
	}
	reg, err := p.mgr.GetForContext(ctx)
	if err != nil {
		return ""
	}
	return reg.ResolveAgentUUID(ctx, agentName)
}

// ResolveModelID implements turnexecutorfactory.AgentModelResolver by
// dispatching to the tenant registry. Returns nil when the tenant context is
// missing or the agent is unknown.
func (p *TenantAwareFlowProvider) ResolveModelID(ctx context.Context, agentName string) *string {
	if p.mgr == nil {
		return nil
	}
	reg, err := p.mgr.GetForContext(ctx)
	if err != nil {
		return nil
	}
	return reg.ResolveModelID(ctx, agentName)
}
