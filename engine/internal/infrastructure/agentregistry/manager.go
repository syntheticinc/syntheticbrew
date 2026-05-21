package agentregistry

import (
	"context"
	"fmt"
	"sync"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// Manager manages AgentRegistry instances.
//
// In single-tenant mode (perTenant=false, used by CE) it holds one pre-loaded
// Registry. In multi-tenant mode (perTenant=true, used when RequireTenant=true)
// it lazily creates and caches a Registry per tenant, loading only that
// tenant's agents on first access.
type Manager struct {
	perTenant bool
	single    *AgentRegistry        // used when !perTenant
	mu        sync.RWMutex
	tenants   map[string]*AgentRegistry // used when perTenant
	repo      AgentReader
	capRepo   CapabilityReader // optional; enables DerivedTools computation
}

// NewManager creates a Manager. Pass perTenant=sc.RequireTenant.
func NewManager(repo AgentReader, perTenant bool) *Manager {
	m := &Manager{
		perTenant: perTenant,
		tenants:   make(map[string]*AgentRegistry),
		repo:      repo,
	}
	if !perTenant {
		m.single = New(repo)
	}
	return m
}

// NewManagerWithCapabilities creates a Manager that also loads capabilities
// so each AgentRegistry populates DerivedTools on Load.
func NewManagerWithCapabilities(repo AgentReader, capRepo CapabilityReader, perTenant bool) *Manager {
	m := &Manager{
		perTenant: perTenant,
		tenants:   make(map[string]*AgentRegistry),
		repo:      repo,
		capRepo:   capRepo,
	}
	if !perTenant {
		m.single = NewWithCapabilities(repo, capRepo)
	}
	return m
}

// newRegistry creates a registry instance using the manager's configured readers.
func (m *Manager) newRegistry() *AgentRegistry {
	if m.capRepo != nil {
		return NewWithCapabilities(m.repo, m.capRepo)
	}
	return New(m.repo)
}

// Init loads agents at startup.
// In single-tenant mode it loads all agents eagerly (CE behaviour).
// In multi-tenant mode loading is deferred per tenant to the first request.
func (m *Manager) Init(ctx context.Context) error {
	if m.perTenant {
		return nil
	}
	return m.single.Load(ctx)
}

// Single returns the singleton Registry used in single-tenant mode.
// Callers that have not yet been updated to use GetForContext may call this
// in CE-only code paths. Panics if called in multi-tenant mode.
func (m *Manager) Single() *AgentRegistry {
	if m.perTenant {
		panic("agentregistry.Manager.Single called in multi-tenant mode — use GetForContext")
	}
	return m.single
}

// GetForContext returns the Registry for the tenant in the context.
// In single-tenant mode it always returns the pre-loaded singleton.
func (m *Manager) GetForContext(ctx context.Context) (*AgentRegistry, error) {
	if !m.perTenant {
		return m.single, nil
	}

	tenantID := domain.TenantIDFromContext(ctx)
	if tenantID == "" {
		return nil, fmt.Errorf("tenant_id required in context for multi-tenant mode")
	}

	m.mu.RLock()
	if r, ok := m.tenants[tenantID]; ok {
		m.mu.RUnlock()
		return r, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.tenants[tenantID]; ok {
		return r, nil // loaded by another goroutine while waiting for the write lock
	}
	r := m.newRegistry()
	if err := r.Load(ctx); err != nil {
		return nil, fmt.Errorf("load registry for tenant %s: %w", tenantID, err)
	}
	m.tenants[tenantID] = r
	return r, nil
}

// InvalidateTenant removes the cached registry for a tenant so the next
// request triggers a fresh load. No-op in single-tenant mode.
func (m *Manager) InvalidateTenant(tenantID string) {
	if !m.perTenant {
		return
	}
	m.mu.Lock()
	delete(m.tenants, tenantID)
	m.mu.Unlock()
}

// InvalidateAll reloads the singleton (single-tenant) or clears all
// per-tenant registries (multi-tenant) so they are reloaded on next access.
func (m *Manager) InvalidateAll() {
	if !m.perTenant {
		if m.single != nil {
			_ = m.single.Reload(context.Background())
		}
		return
	}
	m.mu.Lock()
	m.tenants = make(map[string]*AgentRegistry)
	m.mu.Unlock()
}
