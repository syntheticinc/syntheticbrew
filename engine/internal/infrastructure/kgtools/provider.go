package kgtools

import (
	"context"
	"fmt"
	"sync"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// Provider holds per-tenant Knowledge Graph tool registries. The first call
// to ResolveToolsForBundles for a tenant lazily loads that tenant's schemas
// (just like mcp.Manager.GetForContext) so multi-tenant deployments
// pay zero cost for tenants that never use Knowledge Graphs.
//
// The Provider satisfies capabilities.KGToolResolver via CapabilityAdapter
// (see capability_adapter.go).
type Provider struct {
	mu         sync.RWMutex
	tenants    map[string]*Registry
	tenantLock map[string]*sync.Mutex
	schemaRepo SchemaReader
}

// NewProvider constructs a Provider backed by the given SchemaReader.
func NewProvider(schemaRepo SchemaReader) *Provider {
	return &Provider{
		tenants:    make(map[string]*Registry),
		tenantLock: make(map[string]*sync.Mutex),
		schemaRepo: schemaRepo,
	}
}

// GetForTenant returns the registry for a tenant, lazy-loading if necessary.
// Safe for concurrent use; multiple callers for the same tenant block on a
// per-tenant lock so the registry is initialised exactly once.
func (p *Provider) GetForTenant(ctx context.Context, tenantID string) (*Registry, error) {
	p.mu.RLock()
	r, ok := p.tenants[tenantID]
	p.mu.RUnlock()
	if ok {
		return r, nil
	}

	tl := p.lockForTenant(tenantID)
	tl.Lock()
	defer tl.Unlock()

	// Re-check under per-tenant lock.
	p.mu.RLock()
	if r, ok := p.tenants[tenantID]; ok {
		p.mu.RUnlock()
		return r, nil
	}
	p.mu.RUnlock()

	r = NewRegistry()
	p.mu.Lock()
	p.tenants[tenantID] = r
	p.mu.Unlock()
	return r, nil
}

// ResolveToolsForBundles is the consumer entry point used by
// capabilities.KnowledgeGraphsCapability. It returns the sorted, deduplicated
// list of tool names exposed to the agent across the given bundles, lazily
// loading schemas from the schema repository as needed.
//
// Tenant id is read from ctx via domain.TenantIDFromContext; bundles
// unknown to the schema repo simply contribute zero tools (forward-compatible
// against config drift).
func (p *Provider) ResolveToolsForBundles(
	ctx context.Context,
	agentID string,
	bundles []string,
) ([]string, error) {
	if len(bundles) == 0 {
		return nil, nil
	}
	tenantID := domain.TenantIDFromContext(ctx)
	if tenantID == "" {
		tenantID = domain.CETenantID
	}

	r, err := p.GetForTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("kgtools provider: get tenant registry: %w", err)
	}

	for _, bundleName := range bundles {
		if err := p.ensureBundleLoaded(ctx, r, tenantID, bundleName); err != nil {
			return nil, fmt.Errorf("ensure bundle %s: %w", bundleName, err)
		}
	}
	_ = agentID // currently not used; future per-agent overrides hook in here
	return r.ToolsForBundles(bundles), nil
}

// InvalidateTenant drops the entire per-tenant cache. Called by the apply
// usecase after a successful bundle apply so the next ResolveToolsForBundles
// rebuilds from fresh data.
func (p *Provider) InvalidateTenant(tenantID string) {
	p.mu.Lock()
	delete(p.tenants, tenantID)
	p.mu.Unlock()
}

// InvalidateBundle drops the cached entry for one bundle inside a tenant.
// Lighter touch than InvalidateTenant — used by granular CRUD endpoints.
func (p *Provider) InvalidateBundle(tenantID, bundleName string) {
	p.mu.RLock()
	r, ok := p.tenants[tenantID]
	p.mu.RUnlock()
	if !ok {
		return
	}
	r.Invalidate(bundleName)
}

func (p *Provider) ensureBundleLoaded(ctx context.Context, r *Registry, tenantID, bundleName string) error {
	r.mu.RLock()
	_, loaded := r.bundles[bundleName]
	r.mu.RUnlock()
	if loaded {
		return nil
	}
	schemas, err := p.schemaRepo.ListByBundle(ctx, tenantID, bundleName)
	if err != nil {
		return schemaFmtError("ListByBundle", err)
	}
	r.Set(bundleName, schemas)
	return nil
}

func (p *Provider) lockForTenant(tenantID string) *sync.Mutex {
	p.mu.Lock()
	defer p.mu.Unlock()
	if l, ok := p.tenantLock[tenantID]; ok {
		return l
	}
	l := &sync.Mutex{}
	p.tenantLock[tenantID] = l
	return l
}
