package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/syntheticinc/bytebrew/engine/internal/domain"
	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/persistence/models"
	"github.com/syntheticinc/bytebrew/engine/pkg/plugin"
)

// MCPServerReader exposes the read paths needed by the Manager to (re)load
// per-tenant MCP server configurations. Defined consumer-side; the GORM
// repository implements it.
type MCPServerReader interface {
	ListForTenant(ctx context.Context, tenantID string) ([]models.MCPServerModel, error)
	GetByName(ctx context.Context, tenantID, name string) (*models.MCPServerModel, error)
}

// Manager owns ClientRegistry instances scoped per tenant.
//
// In single-tenant mode (perTenant=false, used by CE) it holds one pre-loaded
// ClientRegistry shared by all callers — Init eagerly loads it. In multi-tenant
// mode (perTenant=true, used when RequireTenant=true) it lazily creates and
// caches a ClientRegistry per tenant, loading only that tenant's MCP servers
// on first access. Tenant A never observes or mutates tenant B's clients.
type Manager struct {
	perTenant bool
	single    *ClientRegistry            // used when !perTenant
	mu        sync.RWMutex
	tenants   map[string]*ClientRegistry // used when perTenant
	repo      MCPServerReader
	policy    plugin.TransportPolicy

	// tenantLocks serialises lifecycle operations (create / replace / Reconnect
	// / Close) for a single tenant without serialising different tenants
	// against each other. m.mu (RWMutex) still guards the tenants map's own
	// reads/writes — fast lookup of *ClientRegistry — but the per-tenant
	// mutex below guards the heavier work that follows the lookup so two
	// concurrent ReconnectTenant calls for the same tenant do not race
	// CloseAll against each other or against a fresh Load.
	tenantLocks   map[string]*sync.Mutex
	tenantLocksMu sync.Mutex

	// fwd is the per-tenant forward-headers cache. Wired by server.go via
	// SetForwardHeadersStore right after construction. The Manager refreshes
	// the entry for a tenant on every successful Init / GetForContext load /
	// ReconnectTenant so ChatHandler closures (`store.GetForContext(reqCtx)`)
	// see the freshest list for that tenant only — tenant A's reload no
	// longer overwrites tenant B's headers.
	fwd *ForwardHeadersStore

	// refresher is the per-server TTL tools/list refresher. Wired via
	// SetRefresher right after construction in server.go. The Manager calls
	// Schedule / Stop on it from Init / GetForContext / ReconnectServer /
	// DisconnectServer so the goroutine count tracks the live set of
	// servers with catalog_refresh_interval_seconds set.
	refresher *Refresher
}

// NewManager creates a Manager. Pass perTenant=sc.RequireTenant.
// In single-tenant mode an empty ClientRegistry is created immediately;
// Init populates it. In multi-tenant mode no per-tenant registries exist
// until GetForContext is called for that tenant.
func NewManager(repo MCPServerReader, policy plugin.TransportPolicy, perTenant bool) *Manager {
	m := &Manager{
		perTenant:   perTenant,
		tenants:     make(map[string]*ClientRegistry),
		tenantLocks: make(map[string]*sync.Mutex),
		repo:        repo,
		policy:      policy,
	}
	if !perTenant {
		m.single = NewClientRegistry()
	}
	return m
}

// lockForTenant returns the per-tenant lifecycle mutex, creating it on first
// use. Callers serialise create / replace / CloseAll for one tenant by
// holding tl while m.mu remains free for other tenants' fast-path lookups.
func (m *Manager) lockForTenant(tenantID string) *sync.Mutex {
	m.tenantLocksMu.Lock()
	defer m.tenantLocksMu.Unlock()
	if l, ok := m.tenantLocks[tenantID]; ok {
		return l
	}
	l := &sync.Mutex{}
	m.tenantLocks[tenantID] = l
	return l
}

// Init loads MCP clients at startup.
//
// In single-tenant mode it eagerly connects every server for the CE sentinel
// tenant — preserves existing CE boot behaviour and logs.
// In multi-tenant mode loading is deferred per tenant to the first request.
func (m *Manager) Init(ctx context.Context) error {
	if m.perTenant {
		return nil
	}
	servers, err := m.repo.ListForTenant(ctx, domain.CETenantID)
	if err != nil {
		return fmt.Errorf("load mcp servers for ce tenant: %w", err)
	}
	if loadErr := m.single.Load(ctx, servers, m.policy); loadErr != nil {
		return loadErr
	}
	if m.fwd != nil {
		m.fwd.Set(domain.CETenantID, CollectForwardHeaders(servers))
	}
	m.scheduleRefreshers(domain.CETenantID, servers)
	return nil
}

// Single returns the singleton ClientRegistry used in single-tenant mode.
// Panics if called in multi-tenant mode — callers must use GetForContext.
func (m *Manager) Single() *ClientRegistry {
	if m.perTenant {
		panic("mcp.Manager.Single called in multi-tenant mode — use GetForContext")
	}
	return m.single
}

// GetForContext returns the ClientRegistry for the tenant in the context.
//
// In single-tenant mode it always returns the pre-loaded singleton.
// In multi-tenant mode it lazy-loads the tenant's registry on first access:
// fast-path checks m.mu (RWMutex) for an already-loaded registry; cold path
// takes the per-tenant lifecycle mutex so concurrent first-callers for the
// same tenant share one Load while different tenants load in parallel.
func (m *Manager) GetForContext(ctx context.Context) (*ClientRegistry, error) {
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

	tl := m.lockForTenant(tenantID)
	tl.Lock()
	defer tl.Unlock()

	// Re-check under the per-tenant lock — another goroutine may have loaded.
	m.mu.RLock()
	if r, ok := m.tenants[tenantID]; ok {
		m.mu.RUnlock()
		return r, nil
	}
	m.mu.RUnlock()

	r := NewClientRegistry()
	servers, err := m.repo.ListForTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("load mcp servers for tenant %s: %w", tenantID, err)
	}
	if err := r.Load(ctx, servers, m.policy); err != nil {
		return nil, fmt.Errorf("connect mcp clients for tenant %s: %w", tenantID, err)
	}
	m.mu.Lock()
	m.tenants[tenantID] = r
	m.mu.Unlock()
	if m.fwd != nil {
		m.fwd.Set(tenantID, CollectForwardHeaders(servers))
	}
	m.scheduleRefreshers(tenantID, servers)
	return r, nil
}

// ReconnectTenant closes every client of the given tenant and reloads from DB.
// Scope is strictly the requested tenant — never affects other tenants.
//
// In single-tenant mode tenantID is ignored and the singleton is reloaded
// against the CE sentinel; this matches the historical /config/reload semantics.
func (m *Manager) ReconnectTenant(ctx context.Context, tenantID string) error {
	if !m.perTenant {
		servers, err := m.repo.ListForTenant(ctx, domain.CETenantID)
		if err != nil {
			return fmt.Errorf("load mcp servers for ce tenant: %w", err)
		}
		m.single.CloseAll()
		if loadErr := m.single.Load(ctx, servers, m.policy); loadErr != nil {
			return loadErr
		}
		if m.fwd != nil {
			m.fwd.Set(domain.CETenantID, CollectForwardHeaders(servers))
		}
		m.scheduleRefreshers(domain.CETenantID, servers)
		return nil
	}

	if tenantID == "" {
		return fmt.Errorf("tenant_id required for multi-tenant ReconnectTenant")
	}

	tl := m.lockForTenant(tenantID)
	tl.Lock()
	defer tl.Unlock()

	servers, err := m.repo.ListForTenant(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("load mcp servers for tenant %s: %w", tenantID, err)
	}

	m.mu.Lock()
	r, ok := m.tenants[tenantID]
	if !ok {
		r = NewClientRegistry()
		m.tenants[tenantID] = r
	}
	m.mu.Unlock()

	r.CloseAll()
	if err := r.Load(ctx, servers, m.policy); err != nil {
		return fmt.Errorf("connect mcp clients for tenant %s: %w", tenantID, err)
	}
	if m.fwd != nil {
		m.fwd.Set(tenantID, CollectForwardHeaders(servers))
	}
	m.scheduleRefreshers(tenantID, servers)
	return nil
}

// SetForwardHeadersStore wires the per-tenant forward-headers cache. Called
// once at boot from server.go right after NewManager. The Manager owns
// updates from then on — Init / GetForContext / ReconnectTenant each
// refresh the entry for the affected tenant after a successful load.
func (m *Manager) SetForwardHeadersStore(s *ForwardHeadersStore) {
	m.fwd = s
}

// SetRefresher wires the per-server TTL refresher. Called once at boot from
// server.go right after NewManager. The Manager owns Schedule / Stop calls
// from then on — Init / GetForContext / ReconnectServer schedule tasks for
// servers whose CatalogRefreshIntervalSeconds is set; DisconnectServer and
// ReconnectServer-with-NULL-interval stop them.
func (m *Manager) SetRefresher(rf *Refresher) {
	m.refresher = rf
}

// scheduleRefreshers (re)schedules per-server refresh tasks for every server
// in the slice that declares a non-nil CatalogRefreshIntervalSeconds. No-op
// when no refresher is wired (legacy boot path or test stub). Schedule is
// idempotent — an existing task with the same key is cancelled and replaced
// so interval changes propagate from a fresh load.
func (m *Manager) scheduleRefreshers(tenantID string, servers []models.MCPServerModel) {
	if m.refresher == nil {
		return
	}
	for _, srv := range servers {
		if srv.CatalogRefreshIntervalSeconds == nil {
			continue
		}
		m.refresher.Schedule(tenantID, srv.Name,
			time.Duration(*srv.CatalogRefreshIntervalSeconds)*time.Second)
	}
}

// getRegistryForTenant returns the per-tenant ClientRegistry for a per-server
// operation. In single-tenant mode (CE) the singleton is returned and
// tenantID is ignored. In multi-tenant mode the registry is lazy-loaded if
// not already present so an MCP CRUD before any chat traffic still has
// somewhere to register the freshly-reconnected client.
func (m *Manager) getRegistryForTenant(ctx context.Context, tenantID string) (*ClientRegistry, error) {
	if !m.perTenant {
		return m.single, nil
	}
	if tenantID == "" {
		return nil, fmt.Errorf("tenant_id required in multi-tenant mode")
	}
	return m.GetForContext(domain.WithTenantID(ctx, tenantID))
}

// lookupOrLoadLocked is the per-tenant-locked variant of GetForContext used
// by ReconnectServer / DisconnectServer when they already hold the per-tenant
// lock. Skips the lifecycle-lock acquisition that GetForContext does so the
// caller does not deadlock on a non-reentrant sync.Mutex.
//
// In single-tenant mode it returns the singleton and ignores tenantID.
// In multi-tenant mode the caller MUST hold m.lockForTenant(tenantID).Lock()
// before calling — the function asserts only via the m.mu RWMutex.
func (m *Manager) lookupOrLoadLocked(ctx context.Context, tenantID string) (*ClientRegistry, error) {
	if !m.perTenant {
		return m.single, nil
	}

	m.mu.RLock()
	if r, ok := m.tenants[tenantID]; ok {
		m.mu.RUnlock()
		return r, nil
	}
	m.mu.RUnlock()

	r := NewClientRegistry()
	servers, err := m.repo.ListForTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("load mcp servers for tenant %s: %w", tenantID, err)
	}
	if err := r.Load(ctx, servers, m.policy); err != nil {
		return nil, fmt.Errorf("connect mcp clients for tenant %s: %w", tenantID, err)
	}
	m.mu.Lock()
	m.tenants[tenantID] = r
	m.mu.Unlock()
	if m.fwd != nil {
		m.fwd.Set(tenantID, CollectForwardHeaders(servers))
	}
	m.scheduleRefreshers(tenantID, servers)
	return r, nil
}

// ReconnectServer closes any stale client for (tenantID, name), dials a fresh
// one from the DB row, and refreshes the tenant's forward-headers cache.
// Per-server granularity — sibling servers of the same tenant are untouched.
//
// Used as a hook after successful MCP CRUD (Create / Update / Patch) so the
// runtime catalog catches up without a manual /config/reload. On error the
// caller should warn-log only — the DB write has already committed and any
// subsequent ReconnectTenant or restart will pick up the row.
func (m *Manager) ReconnectServer(ctx context.Context, tenantID, name string) error {
	if m.perTenant && tenantID == "" {
		return fmt.Errorf("tenant_id required for multi-tenant ReconnectServer")
	}

	effectiveTenant := tenantID
	if !m.perTenant {
		effectiveTenant = domain.CETenantID
	}

	srv, err := m.repo.GetByName(ctx, effectiveTenant, name)
	if err != nil {
		return fmt.Errorf("get mcp server %s/%s: %w", effectiveTenant, name, err)
	}
	if srv == nil {
		return fmt.Errorf("mcp server %s/%s not found", effectiveTenant, name)
	}

	// Serialise per-server lifecycle through the per-tenant lock so a
	// concurrent ReconnectTenant for the same tenant cannot wipe the
	// freshly-dialled client mid-flight. Acquired BEFORE getRegistryForTenant
	// so the lazy-load path inside GetForContext (which takes the same lock)
	// does not deadlock — sync.Mutex is non-reentrant.
	if m.perTenant {
		tl := m.lockForTenant(effectiveTenant)
		tl.Lock()
		defer tl.Unlock()
	}

	registry, err := m.lookupOrLoadLocked(ctx, effectiveTenant)
	if err != nil {
		return fmt.Errorf("get registry for tenant %s: %w", effectiveTenant, err)
	}

	if err := registry.Reconnect(ctx, *srv, m.policy); err != nil {
		return fmt.Errorf("reconnect mcp server %s/%s: %w", effectiveTenant, name, err)
	}

	if m.refresher != nil {
		if srv.CatalogRefreshIntervalSeconds != nil {
			m.refresher.Schedule(effectiveTenant, srv.Name,
				time.Duration(*srv.CatalogRefreshIntervalSeconds)*time.Second)
		} else {
			m.refresher.Stop(effectiveTenant, srv.Name)
		}
	}

	if m.fwd != nil {
		servers, listErr := m.repo.ListForTenant(ctx, effectiveTenant)
		if listErr != nil {
			slog.WarnContext(ctx, "mcp manager: refresh forward headers after reconnect failed",
				"tenant_id", effectiveTenant, "server", name, "error", listErr)
		} else {
			m.fwd.Set(effectiveTenant, CollectForwardHeaders(servers))
		}
	}

	toolsCount := 0
	if tools, _ := registry.GetMCPTools(name); tools != nil {
		toolsCount = len(tools)
	}
	slog.InfoContext(ctx, "mcp server reconnected",
		"tenant_id", effectiveTenant, "server", name, "tools_count", toolsCount)
	return nil
}

// DisconnectServer closes the client for (tenantID, name) and removes it from
// the tenant's registry. Per-server granularity. After successful DELETE of
// an MCP server row the runtime catalog drops the client so subsequent chats
// no longer see its tools.
//
// No-op (returns nil) when the tenant has no cached registry yet, or when
// the server is not registered — DELETE is idempotent.
func (m *Manager) DisconnectServer(ctx context.Context, tenantID, name string) error {
	if m.perTenant && tenantID == "" {
		return fmt.Errorf("tenant_id required for multi-tenant DisconnectServer")
	}

	effectiveTenant := tenantID
	if !m.perTenant {
		effectiveTenant = domain.CETenantID
	}

	var registry *ClientRegistry
	if !m.perTenant {
		registry = m.single
	} else {
		// Serialise per-server lifecycle through the per-tenant lock —
		// concurrent ReconnectTenant must not race with DisconnectServer
		// for the same tenant.
		tl := m.lockForTenant(effectiveTenant)
		tl.Lock()
		defer tl.Unlock()

		m.mu.RLock()
		registry = m.tenants[effectiveTenant]
		m.mu.RUnlock()
		if registry == nil {
			return nil
		}
	}

	if err := registry.Disconnect(name); err != nil {
		return fmt.Errorf("disconnect mcp server %s/%s: %w", effectiveTenant, name, err)
	}

	if m.refresher != nil {
		m.refresher.Stop(effectiveTenant, name)
	}

	if m.fwd != nil {
		servers, listErr := m.repo.ListForTenant(ctx, effectiveTenant)
		if listErr != nil {
			slog.WarnContext(ctx, "mcp manager: refresh forward headers after disconnect failed",
				"tenant_id", effectiveTenant, "server", name, "error", listErr)
		} else {
			m.fwd.Set(effectiveTenant, CollectForwardHeaders(servers))
		}
	}

	slog.InfoContext(ctx, "mcp server disconnected",
		"tenant_id", effectiveTenant, "server", name)
	return nil
}

// RefreshServer re-queries tools/list for one server without reconnecting the
// transport. Returns the new tool count after the refresh swap.
//
// Lighter than ReconnectServer (which closes + redials the underlying
// transport): the existing client stays open and only the cached tools slice
// is replaced atomically. Used for the operator-triggered "Refresh now"
// surface from Admin SPA when downstream renamed/added tools but the session
// is alive. Returns an error tagged via pkgerrors.NotFound when the server
// is not registered (caller should ReconnectServer instead).
func (m *Manager) RefreshServer(ctx context.Context, tenantID, name string) (int, error) {
	if m.perTenant && tenantID == "" {
		return 0, fmt.Errorf("tenant_id required for multi-tenant RefreshServer")
	}

	effectiveTenant := tenantID
	if !m.perTenant {
		effectiveTenant = domain.CETenantID
	}

	var registry *ClientRegistry
	if !m.perTenant {
		registry = m.single
	} else {
		tl := m.lockForTenant(effectiveTenant)
		tl.Lock()
		defer tl.Unlock()
		m.mu.RLock()
		registry = m.tenants[effectiveTenant]
		m.mu.RUnlock()
		if registry == nil {
			return 0, fmt.Errorf("mcp server %q not registered for tenant %s", name, effectiveTenant)
		}
	}

	client, ok := registry.client(name)
	if !ok {
		return 0, fmt.Errorf("mcp server %q not registered for tenant %s", name, effectiveTenant)
	}
	if err := client.RefreshTools(ctx); err != nil {
		return 0, fmt.Errorf("refresh mcp server %q: %w", name, err)
	}
	tools := client.ListTools()
	slog.InfoContext(ctx, "mcp server tools refreshed (manual)",
		"tenant_id", effectiveTenant, "server", name, "tools_count", len(tools))
	return len(tools), nil
}

// InvalidateTenant drops the cached registry for a tenant after closing all
// of its clients. The next GetForContext for the tenant triggers a fresh
// load. No-op in single-tenant mode.
func (m *Manager) InvalidateTenant(tenantID string) {
	if !m.perTenant {
		return
	}
	tl := m.lockForTenant(tenantID)
	tl.Lock()
	defer tl.Unlock()

	m.mu.Lock()
	r, ok := m.tenants[tenantID]
	if ok {
		delete(m.tenants, tenantID)
	}
	m.mu.Unlock()
	if ok {
		r.CloseAll()
	}
}

// Shutdown closes every client across every tenant. Admin-only — invoked by
// server shutdown sequence. After Shutdown the manager state is empty;
// subsequent GetForContext calls in multi-tenant mode trigger fresh loads.
//
// Stops the per-server Refresher first so no goroutine ticks against a
// client that is mid-Close. Symmetric with SetRefresher ownership: the
// Manager owns the Refresher lifecycle.
func (m *Manager) Shutdown() {
	if m.refresher != nil {
		m.refresher.StopAll()
	}
	if !m.perTenant {
		if m.single != nil {
			m.single.CloseAll()
		}
		return
	}
	m.mu.Lock()
	tenants := m.tenants
	m.tenants = make(map[string]*ClientRegistry)
	m.mu.Unlock()
	for _, r := range tenants {
		r.CloseAll()
	}
}
