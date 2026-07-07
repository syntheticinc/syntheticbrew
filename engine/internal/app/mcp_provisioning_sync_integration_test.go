//go:build integration

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/mcp"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	admintools "github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools/admin"
	pluginpkg "github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// --- admintools.MCPServerRepository surface on fakeMCPRepo (F1) ---
//
// fakeMCPRepo (mcp_lifecycle_integration_test.go) already implements
// mcp.MCPServerReader for the Manager. These methods add the admin-tool
// repository surface so a single in-memory store backs both the provisioning
// tool (writer) and the live Manager (reader) — exactly the topology the real
// wiring has (GORM repo shared by both).

func (r *fakeMCPRepo) adminTenant(ctx context.Context) string {
	tid := domain.TenantIDFromContext(ctx)
	if tid == "" {
		tid = domain.CETenantID
	}
	return tid
}

func modelToAdminRecord(m models.MCPServerModel) admintools.MCPServerRecord {
	return admintools.MCPServerRecord{
		ID:      m.ID,
		Name:    m.Name,
		Type:    m.Type,
		Command: m.Command,
		URL:     m.URL,
		Enabled: m.Enabled,
	}
}

func adminRecordToModel(rec *admintools.MCPServerRecord) models.MCPServerModel {
	return models.MCPServerModel{
		ID:      rec.ID,
		Name:    rec.Name,
		Type:    rec.Type,
		Command: rec.Command,
		URL:     rec.URL,
		Enabled: rec.Enabled,
	}
}

func (r *fakeMCPRepo) List(ctx context.Context) ([]admintools.MCPServerRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]admintools.MCPServerRecord, 0)
	for _, v := range r.rows[r.adminTenant(ctx)] {
		out = append(out, modelToAdminRecord(v))
	}
	return out, nil
}

func (r *fakeMCPRepo) GetByID(ctx context.Context, id string) (*admintools.MCPServerRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range r.rows[r.adminTenant(ctx)] {
		if v.ID == id {
			rec := modelToAdminRecord(v)
			return &rec, nil
		}
	}
	return nil, fmt.Errorf("mcp server not found: %s", id)
}

func (r *fakeMCPRepo) Create(ctx context.Context, record *admintools.MCPServerRecord) error {
	record.ID = uuid.NewString()
	r.put(r.adminTenant(ctx), adminRecordToModel(record))
	return nil
}

func (r *fakeMCPRepo) Update(ctx context.Context, id string, record *admintools.MCPServerRecord) error {
	tenant := r.adminTenant(ctx)
	oldName := ""
	r.mu.Lock()
	for _, v := range r.rows[tenant] {
		if v.ID == id {
			oldName = v.Name
			break
		}
	}
	r.mu.Unlock()
	if oldName == "" {
		return fmt.Errorf("mcp server not found: %s", id)
	}
	m := adminRecordToModel(record)
	m.ID = id
	if record.Name != oldName {
		r.remove(tenant, oldName)
	}
	r.put(tenant, m)
	return nil
}

func (r *fakeMCPRepo) Delete(ctx context.Context, id string) error {
	tenant := r.adminTenant(ctx)
	name := ""
	r.mu.Lock()
	for _, v := range r.rows[tenant] {
		if v.ID == id {
			name = v.Name
			break
		}
	}
	r.mu.Unlock()
	if name == "" {
		return fmt.Errorf("mcp server not found: %s", id)
	}
	r.remove(tenant, name)
	return nil
}

// TestMCPProvisioningTool_CreateConnectsWithoutRestart is the F1 headline
// regression guard.
//
// The provisioning tool path only invoked the agent-config reloader, never the
// mcp.Manager, so a server created mid-conversation never dialled into the
// already-cached per-tenant ClientRegistry — its tools resolved as "not
// registered" until a restart. The fix wires an MCPClientSyncer that calls
// Manager.ReconnectServer after the write.
//
// Reproduces the stale-cache scenario: the tenant registry is warmed BEFORE the
// server exists (as the builder-assistant's earlier resolve would), then the
// admin create tool runs. Post-fix the server's tools are resolvable with no
// restart; pre-fix GetMCPTools returns "not registered".
func TestMCPProvisioningTool_CreateConnectsWithoutRestart(t *testing.T) {
	const tenantT = "11111111-1111-1111-1111-111111111111"
	ctx := domain.WithTenantID(context.Background(), tenantT)

	v1 := newMCPHandlerFunc("device_list")
	srv := httptest.NewServer(v1.handler())
	defer srv.Close()

	repo := newFakeMCPRepo()
	manager := mcp.NewManager(repo, pluginpkg.PermissiveTransportPolicy{}, true)

	// Pre-warm tenant T's registry while it has NO servers — this is the stale
	// cache the fix must sync into.
	prewarm, err := manager.GetForContext(ctx)
	require.NoError(t, err)
	_, notYet := prewarm.GetMCPTools("prov-server")
	require.Error(t, notYet, "server must not exist before creation")

	// Invoke the real admin create tool, wired with the real syncer over the
	// pre-warmed manager (mirrors server.go wiring).
	createTool := admintools.NewAdminCreateMCPServerTool(
		repo,
		nil, // reloader — irrelevant to this test (agent-config only)
		pluginpkg.PermissiveTransportPolicy{},
		newMCPClientSyncAdapter(manager),
	)
	args, _ := json.Marshal(map[string]any{
		"name": "prov-server",
		"type": "http",
		"url":  srv.URL,
	})
	out, err := createTool.InvokableRun(ctx, string(args))
	require.NoError(t, err)
	require.Contains(t, out, "created")

	// GREEN: the freshly created server is resolvable WITHOUT any restart or
	// /config/reload. Pre-fix this fails with "not registered".
	reg, err := manager.GetForContext(ctx)
	require.NoError(t, err)
	tools, err := reg.GetMCPTools("prov-server")
	require.NoError(t, err, "created MCP server must be registered in the live per-tenant registry")
	require.NotEmpty(t, tools)

	info, err := tools[0].Info(ctx)
	require.NoError(t, err)
	assert.Equal(t, "device_list", info.Name)
}

// TestMCPProvisioningTool_DeleteDisconnects is the F1 delete-side guard: the
// admin delete tool must drop the live client so its tools stop resolving.
func TestMCPProvisioningTool_DeleteDisconnects(t *testing.T) {
	const tenantT = "11111111-1111-1111-1111-111111111111"
	ctx := domain.WithTenantID(context.Background(), tenantT)

	v1 := newMCPHandlerFunc("device_list")
	srv := httptest.NewServer(v1.handler())
	defer srv.Close()

	repo := newFakeMCPRepo()
	serverID := uuid.NewString()
	repo.put(tenantT, models.MCPServerModel{
		ID:      serverID,
		Name:    "prov-server",
		Type:    "http",
		URL:     srv.URL,
		Enabled: true,
	})

	manager := mcp.NewManager(repo, pluginpkg.PermissiveTransportPolicy{}, true)

	// Warm the registry so the server is dialled and resolvable.
	reg, err := manager.GetForContext(ctx)
	require.NoError(t, err)
	tools, err := reg.GetMCPTools("prov-server")
	require.NoError(t, err)
	require.NotEmpty(t, tools, "server must be reachable before delete")

	deleteTool := admintools.NewAdminDeleteMCPServerTool(
		repo,
		nil, // reloader
		newMCPClientSyncAdapter(manager),
	)
	args, _ := json.Marshal(map[string]any{"server_id": serverID})
	out, err := deleteTool.InvokableRun(ctx, string(args))
	require.NoError(t, err)
	require.Contains(t, out, "deleted")

	// GREEN: the deleted server no longer resolves. Pre-fix it stays registered.
	reg2, err := manager.GetForContext(ctx)
	require.NoError(t, err)
	_, gone := reg2.GetMCPTools("prov-server")
	require.Error(t, gone, "deleted MCP server must be dropped from the live registry")
	assert.Contains(t, gone.Error(), "not registered")
}

// TestMCPProvisioningTool_UpdateRedialsNewConfig is the F1 update-side guard.
// admin_update_mcp_server calls the syncer's ReconnectServer with the post-write
// name (the same redial mechanism admin_set_mcp_server_enabled uses), so a config
// change (here: a new URL) must go live WITHOUT a restart. Pre-fix the tool path
// never touched the mcp.Manager, so the registry kept the stale client.
func TestMCPProvisioningTool_UpdateRedialsNewConfig(t *testing.T) {
	const tenantT = "11111111-1111-1111-1111-111111111111"
	ctx := domain.WithTenantID(context.Background(), tenantT)

	before := httptest.NewServer(newMCPHandlerFunc("tool_before").handler())
	defer before.Close()
	after := httptest.NewServer(newMCPHandlerFunc("tool_after").handler())
	defer after.Close()

	repo := newFakeMCPRepo()
	serverID := uuid.NewString()
	repo.put(tenantT, models.MCPServerModel{
		ID: serverID, Name: "prov-server", Type: "http", URL: before.URL, Enabled: true,
	})

	manager := mcp.NewManager(repo, pluginpkg.PermissiveTransportPolicy{}, true)

	// Warm: the server dials `before` and exposes tool_before.
	reg, err := manager.GetForContext(ctx)
	require.NoError(t, err)
	tools, err := reg.GetMCPTools("prov-server")
	require.NoError(t, err)
	require.NotEmpty(t, tools)
	info, err := tools[0].Info(ctx)
	require.NoError(t, err)
	require.Equal(t, "tool_before", info.Name)

	// Update the URL via the admin tool — must redial the new config live.
	updateTool := admintools.NewAdminUpdateMCPServerTool(
		repo,
		nil, // reloader — agent-config only
		pluginpkg.PermissiveTransportPolicy{},
		newMCPClientSyncAdapter(manager),
	)
	args, _ := json.Marshal(map[string]any{"server_id": serverID, "url": after.URL})
	out, err := updateTool.InvokableRun(ctx, string(args))
	require.NoError(t, err)
	require.Contains(t, out, "updated")

	// GREEN: the updated config is live WITHOUT restart — now exposes tool_after.
	reg2, err := manager.GetForContext(ctx)
	require.NoError(t, err)
	tools2, err := reg2.GetMCPTools("prov-server")
	require.NoError(t, err, "updated MCP server must redial and resolve without restart")
	require.NotEmpty(t, tools2)
	info2, err := tools2[0].Info(ctx)
	require.NoError(t, err)
	assert.Equal(t, "tool_after", info2.Name, "update must redial the new URL live")
}
