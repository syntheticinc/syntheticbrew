package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	pluginpkg "github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// capturingPlugin records the tenantID passed to UsageExtras.
type capturingPlugin struct {
	pluginpkg.Noop
	gotTenantID string
}

func (p *capturingPlugin) UsageExtras(_ context.Context, tenantID string) map[string]any {
	p.gotTenantID = tenantID
	return nil
}

// setupUsageDB creates an in-memory SQLite DB with the minimal columns the
// usage counters query: agents/schemas/messages, all tenant-scoped.
func setupUsageDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)
	for _, ddl := range []string{
		`CREATE TABLE agents (id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL)`,
		`CREATE TABLE schemas (id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL)`,
		`CREATE TABLE messages (id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, session_id TEXT NOT NULL)`,
	} {
		require.NoError(t, db.Exec(ddl).Error)
	}
	return db
}

func seedTenantRows(t *testing.T, db *gorm.DB, tenantID string, agents, schemas int, sessions []string) {
	t.Helper()
	for i := 0; i < agents; i++ {
		require.NoError(t, db.Exec(`INSERT INTO agents (id, tenant_id) VALUES (?, ?)`,
			tenantID+"-agent-"+string(rune('a'+i)), tenantID).Error)
	}
	for i := 0; i < schemas; i++ {
		require.NoError(t, db.Exec(`INSERT INTO schemas (id, tenant_id) VALUES (?, ?)`,
			tenantID+"-schema-"+string(rune('a'+i)), tenantID).Error)
	}
	for i, sess := range sessions {
		// Two messages per session: DISTINCT must collapse them.
		for j := 0; j < 2; j++ {
			require.NoError(t, db.Exec(`INSERT INTO messages (id, tenant_id, session_id) VALUES (?, ?, ?)`,
				tenantID+"-msg-"+string(rune('a'+i))+string(rune('0'+j)), tenantID, sess).Error)
		}
	}
}

type usageResponse struct {
	Plan    string `json:"plan"`
	Metrics []struct {
		Name  string `json:"name"`
		Label string `json:"label"`
		Used  int64  `json:"used"`
		Limit int64  `json:"limit"`
	} `json:"metrics"`
}

func metricUsed(t *testing.T, resp usageResponse, name string) int64 {
	t.Helper()
	for _, m := range resp.Metrics {
		if m.Name == name {
			return m.Used
		}
	}
	t.Fatalf("metric %q not found in response", name)
	return 0
}

// Regression: counters must be tenant-scoped. A global COUNT(*) would return
// tenant A + tenant B aggregates to tenant A's admin.
func TestUsageHandler_GetUsage_TenantIsolation(t *testing.T) {
	db := setupUsageDB(t)
	tenantA := "aaaaaaaa-0000-0000-0000-000000000001"
	tenantB := "bbbbbbbb-0000-0000-0000-000000000002"
	seedTenantRows(t, db, tenantA, 2, 1, []string{"sess-a1", "sess-a2"})
	seedTenantRows(t, db, tenantB, 3, 2, []string{"sess-b1", "sess-b2", "sess-b3"})

	plug := &capturingPlugin{}
	h := NewUsageHandler(db, plug)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage", nil)
	req = req.WithContext(domain.WithTenantID(req.Context(), tenantA))
	rec := httptest.NewRecorder()
	h.GetUsage(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp usageResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	assert.Equal(t, "Community Edition", resp.Plan)
	assert.Equal(t, int64(2), metricUsed(t, resp, "agents"), "agents count must include only tenant A rows")
	assert.Equal(t, int64(1), metricUsed(t, resp, "schemas"), "schemas count must include only tenant A rows")
	assert.Equal(t, int64(2), metricUsed(t, resp, "sessions"), "sessions count must include only tenant A rows")

	assert.Equal(t, tenantA, plug.gotTenantID, "plugin must receive the request tenant, not an empty string")
}

// CE single-tenant mode carries an empty tenant claim; the handler must resolve
// it to the sentinel and count rows written under CETenantID. A raw empty-string
// query returns zeros for a populated self-hosted install (the regression this
// guards). Seeding a second real tenant proves the resolve stays isolated.
func TestUsageHandler_GetUsage_CEDefaultResolvesToSentinel(t *testing.T) {
	db := setupUsageDB(t)
	seedTenantRows(t, db, domain.CETenantID, 5, 3, []string{"sess-1", "sess-2"})
	seedTenantRows(t, db, "cccccccc-0000-0000-0000-000000000003", 4, 4, []string{"sess-c1", "sess-c2", "sess-c3"})

	plug := &capturingPlugin{}
	h := NewUsageHandler(db, plug)

	// No WithTenantID on the context — empty tenant, as a CE local session yields.
	rec := httptest.NewRecorder()
	h.GetUsage(rec, httptest.NewRequest(http.MethodGet, "/api/v1/usage", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp usageResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, int64(5), metricUsed(t, resp, "agents"), "CE empty tenant resolves to the sentinel — only sentinel rows counted")
	assert.Equal(t, int64(3), metricUsed(t, resp, "schemas"))
	assert.Equal(t, int64(2), metricUsed(t, resp, "sessions"))
	assert.Equal(t, domain.CETenantID, plug.gotTenantID, "plugin must receive the resolved sentinel tenant, not an empty string")
}

// Response shape is a public API: plan string, metric names/labels, -1 limits.
func TestUsageHandler_GetUsage_ResponseShape(t *testing.T) {
	db := setupUsageDB(t)
	h := NewUsageHandler(db, &capturingPlugin{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage", nil)
	req = req.WithContext(domain.WithTenantID(req.Context(), domain.CETenantID))
	rec := httptest.NewRecorder()
	h.GetUsage(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp usageResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Metrics, 3)
	assert.Equal(t, "Community Edition", resp.Plan)
	wantNames := []string{"agents", "schemas", "sessions"}
	wantLabels := []string{"Agents", "Schemas", "Sessions"}
	for i, m := range resp.Metrics {
		assert.Equal(t, wantNames[i], m.Name)
		assert.Equal(t, wantLabels[i], m.Label)
		assert.Equal(t, int64(-1), m.Limit)
	}
}
