package mcp

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// stubMCPRepo is a minimal in-memory MCPServerReader for refresher tests.
type stubMCPRepo struct {
	mu   sync.Mutex
	rows map[string]map[string]models.MCPServerModel // tenantID -> name -> row
}

func newStubMCPRepo() *stubMCPRepo {
	return &stubMCPRepo{rows: make(map[string]map[string]models.MCPServerModel)}
}

func (r *stubMCPRepo) ListForTenant(_ context.Context, tenantID string) ([]models.MCPServerModel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.rows[tenantID]
	out := make([]models.MCPServerModel, 0, len(t))
	for _, v := range t {
		out = append(out, v)
	}
	return out, nil
}

func (r *stubMCPRepo) GetByName(_ context.Context, tenantID, name string) (*models.MCPServerModel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.rows[tenantID]
	if v, ok := t[name]; ok {
		return &v, nil
	}
	return nil, nil
}

func (r *stubMCPRepo) put(tenantID string, srv models.MCPServerModel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rows[tenantID] == nil {
		r.rows[tenantID] = make(map[string]models.MCPServerModel)
	}
	srv.TenantID = tenantID
	r.rows[tenantID][srv.Name] = srv
}

// scriptedTransport returns a different tools/list result on each call,
// driven by a counter against a slice of tool name slices. The initialize
// handshake is fixed; tools/list returns scripts[i] where i = (call - 1).
type scriptedTransport struct {
	mu      sync.Mutex
	scripts [][]string
	calls   int64
	closed  atomic.Bool
}

func newScriptedTransport(scripts ...[]string) *scriptedTransport {
	return &scriptedTransport{scripts: scripts}
}

func (s *scriptedTransport) Start(_ context.Context) error { return nil }

func (s *scriptedTransport) Send(_ context.Context, req *Request) (*Response, error) {
	switch req.Method {
	case "initialize":
		return makeInitResponse(), nil
	case "tools/list":
		s.mu.Lock()
		idx := s.calls
		if int(idx) >= len(s.scripts) {
			idx = int64(len(s.scripts) - 1)
		}
		names := s.scripts[idx]
		s.calls++
		s.mu.Unlock()

		tools := make([]MCPTool, 0, len(names))
		for _, n := range names {
			tools = append(tools, MCPTool{Name: n, Description: n + " desc", InputSchema: json.RawMessage(`{"type":"object"}`)})
		}
		return makeToolsResponse(tools), nil
	default:
		return nil, nil
	}
}

func (s *scriptedTransport) Notify(_ context.Context, _ *Request) {}

func (s *scriptedTransport) Close() error {
	s.closed.Store(true)
	return nil
}

func (s *scriptedTransport) callCount() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// dialClient is a test helper: connects a Client with a scripted transport.
func dialClient(t *testing.T, name string, transport Transport) *Client {
	t.Helper()
	c := NewClient(name, transport)
	require.NoError(t, c.Connect(context.Background()))
	return c
}

// newManagerWithRegistry seeds a Manager whose tenant cache already contains
// a registry pre-loaded with one client. Lets refresher tests skip the dial
// machinery and focus on tick behaviour.
func newManagerWithRegistry(perTenant bool, tenantID, serverName string, client *Client) *Manager {
	repo := newStubMCPRepo()
	repo.put(tenantID, models.MCPServerModel{Name: serverName, Type: "http", URL: "http://stub"})

	m := NewManager(repo, plugin.PermissiveTransportPolicy{}, perTenant)

	if perTenant {
		reg := NewClientRegistry()
		reg.Register(serverName, client)
		m.tenants[tenantID] = reg
	} else {
		m.single.Register(serverName, client)
	}

	return m
}

// TestRefresher_LifecycleStopOnCtxCancel verifies the goroutine exits when
// the parent ctx supplied to NewRefresher is cancelled. We intentionally
// schedule with a short interval so the ticker fires at least once.
func TestRefresher_LifecycleStopOnCtxCancel(t *testing.T) {
	transport := newScriptedTransport([]string{"tool_a"})
	client := dialClient(t, "srv", transport)

	manager := newManagerWithRegistry(false, domain.CETenantID, "srv", client)

	rootCtx, cancel := context.WithCancel(context.Background())
	rf := NewRefresher(manager, rootCtx)

	rf.Schedule(domain.CETenantID, "srv", 50*time.Millisecond)
	require.Equal(t, 1, rf.active())

	cancel()

	// Give the goroutine a brief window to observe the cancel and exit.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		// The task entry stays in the map until Stop/StopAll is called — only
		// the goroutine exits on ctx cancel. Verify by counting transport
		// calls plateauing.
		startCalls := transport.callCount()
		time.Sleep(80 * time.Millisecond)
		if transport.callCount() == startCalls {
			return
		}
	}
	t.Fatal("transport calls kept increasing after ctx cancel — goroutine did not exit")
}

// TestRefresher_Reschedule verifies that a second Schedule with the same key
// replaces (not duplicates) the active task.
func TestRefresher_Reschedule(t *testing.T) {
	transport := newScriptedTransport([]string{"tool_a"})
	client := dialClient(t, "srv", transport)

	manager := newManagerWithRegistry(false, domain.CETenantID, "srv", client)

	rf := NewRefresher(manager, context.Background())
	defer rf.StopAll()

	rf.Schedule(domain.CETenantID, "srv", 1*time.Second)
	require.Equal(t, 1, rf.active())

	rf.Schedule(domain.CETenantID, "srv", 2*time.Second)
	require.Equal(t, 1, rf.active(), "second Schedule with same key should replace, not add")
}

// TestRefresher_StopRemovesTask verifies Stop removes the entry from the
// task map and is idempotent (Stop on missing key is no-op).
func TestRefresher_StopRemovesTask(t *testing.T) {
	transport := newScriptedTransport([]string{"tool_a"})
	client := dialClient(t, "srv", transport)

	manager := newManagerWithRegistry(false, domain.CETenantID, "srv", client)

	rf := NewRefresher(manager, context.Background())

	rf.Schedule(domain.CETenantID, "srv", 1*time.Second)
	require.Equal(t, 1, rf.active())

	rf.Stop(domain.CETenantID, "srv")
	require.Equal(t, 0, rf.active())

	// Idempotent: second Stop is a no-op.
	rf.Stop(domain.CETenantID, "srv")
	require.Equal(t, 0, rf.active())
}

// TestRefresher_DiffLogging verifies the tool list actually changes after
// tick — a proxy for "diff logging would have fired added/removed entries"
// without coupling the test to slog handler internals.
func TestRefresher_DiffLogging(t *testing.T) {
	transport := newScriptedTransport(
		[]string{"device.list"},
		[]string{"device_list"},
		[]string{"device_list"},
	)
	client := dialClient(t, "srv", transport)

	manager := newManagerWithRegistry(false, domain.CETenantID, "srv", client)
	rf := NewRefresher(manager, context.Background())
	defer rf.StopAll()

	// Initial connect already pulled "device.list".
	require.Len(t, client.ListTools(), 1)
	assert.Equal(t, "device.list", client.ListTools()[0].Name)

	// Drive one tick directly — assert the tool name swapped.
	rf.tick(context.Background(), refreshKey{tenantID: domain.CETenantID, serverName: "srv"})

	tools := client.ListTools()
	require.Len(t, tools, 1)
	assert.Equal(t, "device_list", tools[0].Name,
		"tick must call RefreshTools and swap the cached tool list")
}

// TestClient_RefreshTools_SwapsAtomically verifies serial RefreshTools calls
// always return a consistent slice (no torn read where tools is briefly empty
// between transport.Send return and the slice swap).
func TestClient_RefreshTools_SwapsAtomically(t *testing.T) {
	transport := newScriptedTransport(
		[]string{"a", "b"},
		[]string{"c", "d", "e"},
		[]string{"f"},
	)
	client := dialClient(t, "srv", transport)

	require.Len(t, client.ListTools(), 2)

	require.NoError(t, client.RefreshTools(context.Background()))
	require.Len(t, client.ListTools(), 3)

	require.NoError(t, client.RefreshTools(context.Background()))
	require.Len(t, client.ListTools(), 1)
}

// TestRefresher_TenantIsolation verifies a Schedule for tenant A never
// triggers a refresh against tenant B's same-name server.
func TestRefresher_TenantIsolation(t *testing.T) {
	transportA := newScriptedTransport([]string{"a_v1"}, []string{"a_v2"})
	transportB := newScriptedTransport([]string{"b_v1"})

	clientA := dialClient(t, "shared-name", transportA)
	clientB := dialClient(t, "shared-name", transportB)

	repo := newStubMCPRepo()
	repo.put("tenant-a", models.MCPServerModel{Name: "shared-name", Type: "http", URL: "http://stubA"})
	repo.put("tenant-b", models.MCPServerModel{Name: "shared-name", Type: "http", URL: "http://stubB"})

	manager := NewManager(repo, plugin.PermissiveTransportPolicy{}, true)

	regA := NewClientRegistry()
	regA.Register("shared-name", clientA)
	manager.tenants["tenant-a"] = regA

	regB := NewClientRegistry()
	regB.Register("shared-name", clientB)
	manager.tenants["tenant-b"] = regB

	rf := NewRefresher(manager, context.Background())
	defer rf.StopAll()

	// Drive a tick only for tenant A.
	rf.tick(context.Background(), refreshKey{tenantID: "tenant-a", serverName: "shared-name"})

	assert.Equal(t, "a_v2", clientA.ListTools()[0].Name,
		"tenant A's client must be refreshed")

	// Tenant B's client must be untouched — its transport was never called
	// after the initial connect.
	assert.Equal(t, int64(1), transportB.callCount(),
		"tenant B's transport must only have been called once (initial connect)")
	assert.Equal(t, "b_v1", clientB.ListTools()[0].Name,
		"tenant B's tool list must remain unchanged")
}

// TestRefresher_StopAllClearsAllTasks verifies StopAll removes every task
// regardless of tenant.
func TestRefresher_StopAllClearsAllTasks(t *testing.T) {
	repo := newStubMCPRepo()
	manager := NewManager(repo, plugin.PermissiveTransportPolicy{}, true)
	rf := NewRefresher(manager, context.Background())

	rf.Schedule("tenant-a", "srv1", 1*time.Second)
	rf.Schedule("tenant-a", "srv2", 1*time.Second)
	rf.Schedule("tenant-b", "srv1", 1*time.Second)
	require.Equal(t, 3, rf.active())

	rf.StopAll()
	require.Equal(t, 0, rf.active())
}

// TestRefresher_ScheduleZeroIntervalIsNoop guards against accidentally
// starting a tight-loop ticker if the operator misconfigures the interval.
func TestRefresher_ScheduleZeroIntervalIsNoop(t *testing.T) {
	repo := newStubMCPRepo()
	manager := NewManager(repo, plugin.PermissiveTransportPolicy{}, false)
	rf := NewRefresher(manager, context.Background())
	defer rf.StopAll()

	rf.Schedule(domain.CETenantID, "srv", 0)
	require.Equal(t, 0, rf.active())
}
