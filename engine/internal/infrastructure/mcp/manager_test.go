package mcp

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// fakeMCPRepo is an in-memory MCPServerReader keyed by tenantID. It also
// counts ListForTenant calls per tenant so tests can assert the lazy-load
// path is invoked exactly once under concurrency.
type fakeMCPRepo struct {
	mu        sync.Mutex
	rows      map[string][]models.MCPServerModel
	listCalls map[string]int
}

func newFakeMCPRepo() *fakeMCPRepo {
	return &fakeMCPRepo{
		rows:      make(map[string][]models.MCPServerModel),
		listCalls: make(map[string]int),
	}
}

func (f *fakeMCPRepo) put(tenantID string, servers ...models.MCPServerModel) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[tenantID] = append([]models.MCPServerModel{}, servers...)
}

func (f *fakeMCPRepo) ListForTenant(_ context.Context, tenantID string) ([]models.MCPServerModel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls[tenantID]++
	rows, ok := f.rows[tenantID]
	if !ok {
		return nil, nil
	}
	cp := make([]models.MCPServerModel, len(rows))
	copy(cp, rows)
	return cp, nil
}

func (f *fakeMCPRepo) GetByName(_ context.Context, tenantID, name string) (*models.MCPServerModel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows[tenantID] {
		if r.Name == name {
			cp := r
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("not found: %s/%s", tenantID, name)
}

func (f *fakeMCPRepo) listCallsFor(tenantID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listCalls[tenantID]
}

// connectableMockTransport mirrors the mockTransport pattern from
// client_test.go and additionally records when Close is called so
// CloseAll/Reconnect tests can assert per-instance side-effects.
type connectableMockTransport struct {
	mu       sync.Mutex
	tools    []MCPTool
	closed   atomic.Bool
	notified []*Request
}

func newConnectableMockTransport(tools []MCPTool) *connectableMockTransport {
	return &connectableMockTransport{tools: tools}
}

func (t *connectableMockTransport) Start(_ context.Context) error { return nil }

func (t *connectableMockTransport) Send(_ context.Context, req *Request) (*Response, error) {
	switch req.Method {
	case "initialize":
		return makeInitResponse(), nil
	case "tools/list":
		t.mu.Lock()
		defer t.mu.Unlock()
		return makeToolsResponse(t.tools), nil
	}
	return nil, fmt.Errorf("unexpected method: %s", req.Method)
}

func (t *connectableMockTransport) Notify(_ context.Context, req *Request) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.notified = append(t.notified, req)
}

func (t *connectableMockTransport) Close() error {
	t.closed.Store(true)
	return nil
}

// preconnectedClient bypasses the dialServer path used by Manager.Init/Load
// (which expects a real DB row → real transport). Tests pre-register a
// connected Client built from connectableMockTransport so we can assert the
// ClientRegistry's behaviour without round-tripping through dial logic.
func preconnectedClient(t *testing.T, name string, tools []MCPTool) (*Client, *connectableMockTransport) {
	t.Helper()
	transport := newConnectableMockTransport(tools)
	client := NewClient(name, transport)
	require.NoError(t, client.Connect(context.Background()))
	return client, transport
}

func TestManager_TenantIsolation(t *testing.T) {
	repo := newFakeMCPRepo()
	mgr := NewManager(repo, plugin.PermissiveTransportPolicy{}, true)

	ctxA := domain.WithTenantID(context.Background(), "tenant-a")
	ctxB := domain.WithTenantID(context.Background(), "tenant-b")

	regA, err := mgr.GetForContext(ctxA)
	require.NoError(t, err)
	regB, err := mgr.GetForContext(ctxB)
	require.NoError(t, err)
	require.NotSame(t, regA, regB, "each tenant must own a distinct ClientRegistry")

	clientA, transportA := preconnectedClient(t, "chirp-tools", []MCPTool{{Name: "do_a"}})
	clientB, transportB := preconnectedClient(t, "chirp-tools", []MCPTool{{Name: "do_b"}})
	regA.Register("chirp-tools", clientA)
	regB.Register("chirp-tools", clientB)

	require.NoError(t, mgr.ReconnectTenant(ctxA, "tenant-a"))

	// Tenant A's underlying transport must be closed; tenant B untouched.
	assert.True(t, transportA.closed.Load(), "tenant A client should close on ReconnectTenant(A)")
	assert.False(t, transportB.closed.Load(), "tenant B client must not be touched by ReconnectTenant(A)")
	assert.True(t, clientB.IsConnected(), "tenant B client must remain connected")
}

func TestManager_LazyLoad(t *testing.T) {
	repo := newFakeMCPRepo()
	// No rows registered — tenant exists but has zero MCP servers, which
	// still lets us assert the Load path runs exactly once under concurrency.
	mgr := NewManager(repo, plugin.PermissiveTransportPolicy{}, true)

	ctxA := domain.WithTenantID(context.Background(), "tenant-a")

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	results := make([]*ClientRegistry, goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			r, err := mgr.GetForContext(ctxA)
			require.NoError(t, err)
			results[idx] = r
		}(i)
	}
	wg.Wait()

	first := results[0]
	require.NotNil(t, first)
	for _, r := range results {
		assert.Same(t, first, r, "all goroutines must observe the same ClientRegistry instance")
	}
	assert.Equal(t, 1, repo.listCallsFor("tenant-a"), "ListForTenant must run exactly once under concurrent first-access")
}

func TestManager_InvalidateTenant(t *testing.T) {
	repo := newFakeMCPRepo()
	mgr := NewManager(repo, plugin.PermissiveTransportPolicy{}, true)

	ctxA := domain.WithTenantID(context.Background(), "tenant-a")

	regBefore, err := mgr.GetForContext(ctxA)
	require.NoError(t, err)

	clientA, transportA := preconnectedClient(t, "chirp-tools", nil)
	regBefore.Register("chirp-tools", clientA)

	mgr.InvalidateTenant("tenant-a")

	// CloseAll must have run on the invalidated registry.
	assert.True(t, transportA.closed.Load(), "InvalidateTenant must close the tenant's clients")

	regAfter, err := mgr.GetForContext(ctxA)
	require.NoError(t, err)
	assert.NotSame(t, regBefore, regAfter, "InvalidateTenant must trigger a fresh load on next access")
	assert.Equal(t, 2, repo.listCallsFor("tenant-a"), "second access after invalidate must call repo again")
}

func TestManager_ShutdownVsCloseAll(t *testing.T) {
	repo := newFakeMCPRepo()
	mgr := NewManager(repo, plugin.PermissiveTransportPolicy{}, true)

	ctxA := domain.WithTenantID(context.Background(), "tenant-a")
	ctxB := domain.WithTenantID(context.Background(), "tenant-b")

	regA, err := mgr.GetForContext(ctxA)
	require.NoError(t, err)
	regB, err := mgr.GetForContext(ctxB)
	require.NoError(t, err)

	clientA, transportA := preconnectedClient(t, "srv", nil)
	clientB, transportB := preconnectedClient(t, "srv", nil)
	regA.Register("srv", clientA)
	regB.Register("srv", clientB)

	// Per-tenant CloseAll must scope to that tenant only.
	regA.CloseAll()
	assert.True(t, transportA.closed.Load(), "regA.CloseAll closes tenant A clients")
	assert.False(t, transportB.closed.Load(), "regA.CloseAll must NOT touch tenant B")

	// Manager.Shutdown fans out CloseAll across every registered tenant.
	mgr.Shutdown()
	assert.True(t, transportB.closed.Load(), "Manager.Shutdown closes every tenant's clients")
}

// TestManager_ConcurrentReconnectTenant_NoRace fires N goroutines all
// calling ReconnectTenant for the SAME tenant. The per-tenant lifecycle
// mutex must serialise them so:
//   - no panic / data race (run with -race in CI),
//   - the final state has exactly one cached registry for the tenant,
//   - the repo's ListForTenant was called exactly N times (one per
//     reconnect), proving no overlap silently dropped a Load.
func TestManager_ConcurrentReconnectTenant_NoRace(t *testing.T) {
	repo := newFakeMCPRepo()
	// Empty rows — Load handles a zero-server slice cleanly without dialling
	// any real transport, so the test exercises only the Manager-level lock
	// discipline.
	repo.put("tenant-x")

	mgr := NewManager(repo, plugin.PermissiveTransportPolicy{}, true)
	ctxX := domain.WithTenantID(context.Background(), "tenant-x")

	// Prime the cache so ReconnectTenant hits the existing-registry branch.
	_, err := mgr.GetForContext(ctxX)
	require.NoError(t, err)
	primingCalls := repo.listCallsFor("tenant-x")
	require.Equal(t, 1, primingCalls, "GetForContext must prime once")

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			require.NoError(t, mgr.ReconnectTenant(ctxX, "tenant-x"))
		}()
	}
	wg.Wait()

	// One cached registry survives — no torn writes that left the map empty
	// or with a duplicate.
	mgr.mu.RLock()
	regCount := len(mgr.tenants)
	final := mgr.tenants["tenant-x"]
	mgr.mu.RUnlock()
	assert.Equal(t, 1, regCount, "exactly one cached registry must remain")
	assert.NotNil(t, final, "tenant-x registry must be non-nil after concurrent reconnects")

	// Each ReconnectTenant calls ListForTenant exactly once, in addition to
	// the priming call.
	assert.Equal(t, primingCalls+goroutines, repo.listCallsFor("tenant-x"),
		"ListForTenant call count must equal priming + goroutines (no Load was lost)")
}

// TestManager_RefreshServer_LightweightUpdate verifies RefreshServer rotates
// the cached tools without recreating the transport. The mock transport
// returns one tool list on Connect, then a different one on subsequent
// tools/list — Manager.RefreshServer must reflect the updated count and the
// returned tool count must match.
func TestManager_RefreshServer_LightweightUpdate(t *testing.T) {
	repo := newFakeMCPRepo()
	mgr := NewManager(repo, plugin.PermissiveTransportPolicy{}, true)

	ctxA := domain.WithTenantID(context.Background(), "tenant-a")

	regA, err := mgr.GetForContext(ctxA)
	require.NoError(t, err)

	// Pre-register a connected client whose transport will swap tools list.
	transport := &swappableMockTransport{tools: []MCPTool{{Name: "do_a"}}}
	client := NewClient("chirp-tools", transport)
	require.NoError(t, client.Connect(context.Background()))
	regA.Register("chirp-tools", client)

	// First refresh: transport still returns 1 tool — count is 1.
	count, err := mgr.RefreshServer(ctxA, "tenant-a", "chirp-tools")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Swap downstream catalog: now returns 3 tools.
	transport.set([]MCPTool{{Name: "do_a"}, {Name: "do_b"}, {Name: "do_c"}})
	count, err = mgr.RefreshServer(ctxA, "tenant-a", "chirp-tools")
	require.NoError(t, err)
	assert.Equal(t, 3, count, "RefreshServer must reflect post-refresh tools count")
	assert.False(t, transport.closed.Load(), "transport must NOT be closed by lightweight refresh")

	// Unknown server → error (Manager-level fmt.Errorf, mapped to NotFound at adapter).
	_, err = mgr.RefreshServer(ctxA, "tenant-a", "unknown-server")
	assert.Error(t, err)
}

// swappableMockTransport mirrors connectableMockTransport but lets tests
// rotate the tools/list response between calls, so RefreshServer can be
// observed picking up downstream catalog changes.
type swappableMockTransport struct {
	mu     sync.Mutex
	tools  []MCPTool
	closed atomic.Bool
}

func (t *swappableMockTransport) set(tools []MCPTool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.tools = tools
}

func (t *swappableMockTransport) Start(_ context.Context) error { return nil }

func (t *swappableMockTransport) Send(_ context.Context, req *Request) (*Response, error) {
	switch req.Method {
	case "initialize":
		return makeInitResponse(), nil
	case "tools/list":
		t.mu.Lock()
		defer t.mu.Unlock()
		return makeToolsResponse(t.tools), nil
	}
	return nil, fmt.Errorf("unexpected method: %s", req.Method)
}

func (t *swappableMockTransport) Notify(_ context.Context, _ *Request) {}

func (t *swappableMockTransport) Close() error {
	t.closed.Store(true)
	return nil
}

func TestManager_CESingleton(t *testing.T) {
	repo := newFakeMCPRepo()
	// CE: perTenant=false. NewManager pre-allocates the singleton; no Init
	// needed for this test (the test does not exercise the dial path).
	mgr := NewManager(repo, plugin.PermissiveTransportPolicy{}, false)

	single := mgr.Single()
	require.NotNil(t, single, "CE Manager must expose a non-nil singleton via Single()")

	regAnyCtx, err := mgr.GetForContext(context.Background())
	require.NoError(t, err)
	assert.Same(t, single, regAnyCtx, "CE GetForContext must return the singleton regardless of ctx")

	regWithTenant, err := mgr.GetForContext(domain.WithTenantID(context.Background(), "any-tenant"))
	require.NoError(t, err)
	assert.Same(t, single, regWithTenant, "CE GetForContext ignores tenant in ctx — same singleton")

	// Sanity: multi-tenant Single() panics.
	mgrMT := NewManager(repo, plugin.PermissiveTransportPolicy{}, true)
	assert.Panics(t, func() { _ = mgrMT.Single() }, "Single() in multi-tenant mode must panic")
}
