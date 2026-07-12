package configrepo

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// setupCapabilityTestDB returns an in-memory SQLite DB holding the agents and
// capabilities tables. The capabilities table carries the same named CHECK
// constraint as production Postgres (chk_capabilities_type, enumerating the
// valid capability types) so a write that forces an empty type is rejected
// here exactly as it is on prod. That constraint is what makes this regression
// bite: the pre-fix Update wrote an empty type and tripped SQLSTATE 23514.
func setupCapabilityTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)

	require.NoError(t, db.Exec(`CREATE TABLE agents (
		id         TEXT PRIMARY KEY,
		tenant_id  TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
		name       TEXT NOT NULL,
		created_at DATETIME,
		updated_at DATETIME
	)`).Error)

	require.NoError(t, db.Exec(`CREATE TABLE capabilities (
		id         TEXT PRIMARY KEY,
		agent_id   TEXT NOT NULL,
		type       TEXT NOT NULL,
		config     TEXT,
		enabled    INTEGER NOT NULL DEFAULT 1,
		tenant_id  TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
		created_at DATETIME,
		updated_at DATETIME,
		CONSTRAINT chk_capabilities_type CHECK (type IN ('memory','knowledge','knowledge_graphs'))
	)`).Error)

	return db
}

// seedCapability inserts a minimal capabilities row for the given agent.
func seedCapability(t *testing.T, db *gorm.DB, capID, agentID, tenantID, capType string) {
	t.Helper()
	require.NoError(t, db.Exec(
		"INSERT INTO capabilities (id, agent_id, tenant_id, type, config, enabled) VALUES (?, ?, ?, ?, '{}', 1)",
		capID, agentID, tenantID, capType,
	).Error)
}

// TestCapabilityUpdate_PreservesImmutableType guards the prod bug where
// admin_update_capability failed with a chk_capabilities_type violation.
//
// The update tool builds its record with Type left empty because a
// capability's type is immutable (you remove+add to change it, you do not
// morph memory into knowledge). The repo must therefore update only config
// and enabled and never write type. Before the fix it wrote an empty type and
// the CHECK constraint rejected every update.
func TestCapabilityUpdate_PreservesImmutableType(t *testing.T) {
	tests := []struct {
		name    string
		config  map[string]interface{}
		enabled bool
	}{
		{
			name:    "disable capability and change config",
			config:  map[string]interface{}{"backend": "pgvector"},
			enabled: false,
		},
		{
			name:    "keep enabled and change config only",
			config:  map[string]interface{}{"backend": "sqlite-vec"},
			enabled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupCapabilityTestDB(t)
			repo := NewGORMCapabilityRepository(db)

			const (
				agentID = "agent-1"
				capID   = "cap-1"
			)
			seedAgent(t, db, agentID, domain.CETenantID, "cap-agent")
			seedCapability(t, db, capID, agentID, domain.CETenantID, "memory")

			ctx := domain.WithTenantID(context.Background(), domain.CETenantID)

			// Mirror exactly what adminUpdateCapabilityTool builds: Type unset.
			err := repo.Update(ctx, capID, &CapabilityRecord{
				Config:  tt.config,
				Enabled: tt.enabled,
			})
			require.NoError(t, err)

			got, err := repo.GetByID(ctx, capID)
			require.NoError(t, err)
			assert.Equal(t, "memory", got.Type, "type must be preserved across update")
			assert.Equal(t, tt.enabled, got.Enabled,
				"enabled must persist (map-form Updates writes zero-value false)")
			for k, v := range tt.config {
				assert.Equal(t, v, got.Config[k], "config[%s] must be updated", k)
			}
		})
	}
}
