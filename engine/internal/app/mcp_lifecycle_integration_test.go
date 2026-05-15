//go:build integration

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/bytebrew/engine/internal/domain"
	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/mcp"
	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/persistence/models"
	pluginpkg "github.com/syntheticinc/bytebrew/engine/pkg/plugin"
)

// fakeMCPRepo is an in-memory MCPServerReader used by the lifecycle tests.
// Stores rows keyed by (tenantID, name); list/get/upsert/delete cover the
// surface mcp.Manager invokes via ReconnectServer / DisconnectServer.
type fakeMCPRepo struct {
	mu   sync.Mutex
	rows map[string]map[string]models.MCPServerModel // tenantID -> name -> row
}

func newFakeMCPRepo() *fakeMCPRepo {
	return &fakeMCPRepo{rows: make(map[string]map[string]models.MCPServerModel)}
}

func (r *fakeMCPRepo) ListForTenant(_ context.Context, tenantID string) ([]models.MCPServerModel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.rows[tenantID]
	out := make([]models.MCPServerModel, 0, len(t))
	for _, v := range t {
		out = append(out, v)
	}
	return out, nil
}

func (r *fakeMCPRepo) GetByName(_ context.Context, tenantID, name string) (*models.MCPServerModel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.rows[tenantID]
	v, ok := t[name]
	if !ok {
		return nil, fmt.Errorf("not found: %s/%s", tenantID, name)
	}
	return &v, nil
}

func (r *fakeMCPRepo) put(tenantID string, srv models.MCPServerModel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rows[tenantID] == nil {
		r.rows[tenantID] = make(map[string]models.MCPServerModel)
	}
	srv.TenantID = tenantID
	r.rows[tenantID][srv.Name] = srv
}

func (r *fakeMCPRepo) remove(tenantID, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t := r.rows[tenantID]; t != nil {
		delete(t, name)
	}
}

// mcpHandlerFunc serves a canned MCP JSON-RPC `initialize` + `tools/list`.
// `toolName` is what the fake reports for tools/list — flipping it between
// PATCH calls verifies the runtime registry picked up the new endpoint.
type mcpHandlerFunc struct {
	toolName atomic.Value // string
	hits     int64
}

func newMCPHandlerFunc(initialTool string) *mcpHandlerFunc {
	h := &mcpHandlerFunc{}
	h.toolName.Store(initialTool)
	return h
}

func (h *mcpHandlerFunc) setTool(name string) {
	h.toolName.Store(name)
}

func (h *mcpHandlerFunc) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&h.hits, 1)
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     interface{} `json:"id"`
			Method string      `json:"method"`
		}
		_ = json.Unmarshal(body, &req)

		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			res, _ := json.Marshal(map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{},
			})
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  json.RawMessage(res),
			})
		case "tools/list":
			tool := h.toolName.Load().(string)
			res, _ := json.Marshal(map[string]interface{}{
				"tools": []map[string]interface{}{{
					"name":        tool,
					"description": "fake tool",
					"inputSchema": map[string]interface{}{"type": "object"},
				}},
			})
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  json.RawMessage(res),
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error":   map[string]interface{}{"code": -32601, "message": "method not found: " + req.Method},
			})
		}
	}
}

// firstToolName returns the first tool name from the registry's GetMCPTools
// for the given server, or "" when none are registered.
func firstToolName(t *testing.T, reg *mcp.ClientRegistry, server string) string {
	t.Helper()
	tools, err := reg.GetMCPTools(server)
	if err != nil {
		return ""
	}
	if len(tools) == 0 {
		return ""
	}
	info, err := tools[0].Info(context.Background())
	require.NoError(t, err)
	return info.Name
}

// TestMCPLifecycle_PatchAutoReconnects verifies that PATCH on an MCP server
// triggers Manager.ReconnectServer so the runtime registry sees the new
// downstream tools/list WITHOUT any /config/reload call.
func TestMCPLifecycle_PatchAutoReconnects(t *testing.T) {
	tenant := domain.CETenantID

	v1 := newMCPHandlerFunc("device.list")
	srv1 := httptest.NewServer(v1.handler())
	defer srv1.Close()

	v2 := newMCPHandlerFunc("device_list")
	srv2 := httptest.NewServer(v2.handler())
	defer srv2.Close()

	repo := newFakeMCPRepo()
	repo.put(tenant, models.MCPServerModel{
		ID:   "id-1",
		Name: "test-server",
		Type: "http",
		URL:  srv1.URL,
	})

	manager := mcp.NewManager(repo, pluginpkg.PermissiveTransportPolicy{}, false)
	require.NoError(t, manager.Init(context.Background()))

	registry := manager.Single()
	assert.Equal(t, "device.list", firstToolName(t, registry, "test-server"),
		"singleton should serve v1 tool name after Init")

	// Simulate PATCH: DB row updated to the new URL (v2 endpoint).
	repo.put(tenant, models.MCPServerModel{
		ID:   "id-1",
		Name: "test-server",
		Type: "http",
		URL:  srv2.URL,
	})

	// The hook the adapter would invoke after a successful PATCH:
	require.NoError(t, manager.ReconnectServer(context.Background(), tenant, "test-server"))

	assert.Equal(t, "device_list", firstToolName(t, registry, "test-server"),
		"after ReconnectServer the registry must serve the renamed tool — no /config/reload was called")
}

// TestMCPLifecycle_TTLRefresh verifies that a server with
// catalog_refresh_interval_seconds set causes the per-server refresh task
// to refetch tools/list at the configured interval, and that the registry
// observes the renamed tool via the live downstream change.
//
// Skipped under -short because the refresh interval floor is 30s. In a
// normal `go test` run (no -short) this exercises the full goroutine path
// for ~32s. Option (a) per the plan; an injected ticker source is the
// alternative if/when test wall time becomes a concern.
func TestMCPLifecycle_TTLRefresh(t *testing.T) {
	if testing.Short() {
		t.Skip("ttl refresh integration test takes ~30s; skipping in -short mode")
	}

	tenant := domain.CETenantID

	v1 := newMCPHandlerFunc("device.list")
	srv1 := httptest.NewServer(v1.handler())
	defer srv1.Close()

	const refreshSec = 30
	interval := refreshSec
	repo := newFakeMCPRepo()
	repo.put(tenant, models.MCPServerModel{
		ID:                            "id-1",
		Name:                          "test-server",
		Type:                          "http",
		URL:                           srv1.URL,
		CatalogRefreshIntervalSeconds: &interval,
	})

	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	manager := mcp.NewManager(repo, pluginpkg.PermissiveTransportPolicy{}, false)
	refresher := mcp.NewRefresher(manager, rootCtx)
	manager.SetRefresher(refresher)
	defer refresher.StopAll()

	require.NoError(t, manager.Init(context.Background()))

	registry := manager.Single()
	assert.Equal(t, "device.list", firstToolName(t, registry, "test-server"),
		"singleton should serve v1 tool name immediately after Init")

	// Flip the downstream tool name to simulate an upstream rename without a
	// /config/reload — only the periodic refresher should pick this up.
	v1.setTool("device_list")

	// Wait one tick + small slack so the goroutine has a chance to fire.
	time.Sleep(time.Duration(refreshSec+5) * time.Second)

	assert.Equal(t, "device_list", firstToolName(t, registry, "test-server"),
		"after one TTL tick the registry must reflect the renamed downstream tool")
}

// TestMCPLifecycle_DeleteRemovesClient verifies DELETE on an MCP server
// triggers Manager.DisconnectServer so the runtime registry no longer
// reports tools for that server.
func TestMCPLifecycle_DeleteRemovesClient(t *testing.T) {
	tenant := domain.CETenantID

	v1 := newMCPHandlerFunc("device.list")
	srv1 := httptest.NewServer(v1.handler())
	defer srv1.Close()

	repo := newFakeMCPRepo()
	repo.put(tenant, models.MCPServerModel{
		ID:   "id-1",
		Name: "test-server",
		Type: "http",
		URL:  srv1.URL,
	})

	manager := mcp.NewManager(repo, pluginpkg.PermissiveTransportPolicy{}, false)
	require.NoError(t, manager.Init(context.Background()))

	registry := manager.Single()
	assert.Equal(t, "device.list", firstToolName(t, registry, "test-server"),
		"server should be reachable before delete")

	// Simulate DELETE: row dropped, hook invoked.
	repo.remove(tenant, "test-server")
	require.NoError(t, manager.DisconnectServer(context.Background(), tenant, "test-server"))

	tools, err := registry.GetMCPTools("test-server")
	require.Error(t, err, "after DisconnectServer the server must no longer be registered")
	assert.Contains(t, err.Error(), "not registered")
	assert.Nil(t, tools)
}
