//go:build integration

package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	admintools "github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools/admin"
)

// TestAdminMCPServerAdapter_UpdatePreservesAuthType is the F3 regression guard.
//
// The admin update surface exposes only Name/Type/Command/URL/Args/EnvVars/
// Enabled — none of the auth_* columns. Because the underlying GORM repo write
// is a full-row replace (Select("*")), building a fresh model from the admin
// DTO alone blanked auth_type to ” and tripped the chk_mcp_servers_auth_type
// CHECK (SQLSTATE 23514). The fix loads the current row first and overlays only
// the admin-managed columns.
//
// Case A: a row whose auth_type defaulted to 'none' survives an admin url-only
// update (RED pre-fix: 23514).
// Case B: a row with a real non-default auth field keeps it after an admin
// url-only update.
func TestAdminMCPServerAdapter_UpdatePreservesAuthType(t *testing.T) {
	db := requireAppTestDB(t)
	ctx := context.Background()

	repo := configrepo.NewGORMMCPServerRepository(db)
	adapter := newAdminMCPServerRepoAdapter(repo)

	t.Run("Case A: default auth_type=none survives url-only update", func(t *testing.T) {
		const name = "f3-case-a"
		t.Cleanup(func() { db.Exec("DELETE FROM mcp_servers WHERE name = ?", name) })

		// Create through the admin adapter — it omits auth_type so the DB
		// default 'none' applies on INSERT (mirrors admin_create_mcp_server).
		createRec := &admintools.MCPServerRecord{
			Name:    name,
			Type:    "http",
			URL:     "http://example.test/v1",
			Enabled: true,
		}
		require.NoError(t, adapter.Create(ctx, createRec))
		require.NotEmpty(t, createRec.ID)

		before, err := adapter.GetByID(ctx, createRec.ID)
		require.NoError(t, err)
		require.Equal(t, "http://example.test/v1", before.URL)

		got, err := repo.GetByID(ctx, createRec.ID)
		require.NoError(t, err)
		require.Equal(t, "none", got.AuthType, "create must default auth_type to none")

		// Admin url-only update. Pre-fix this wrote auth_type='' → 23514.
		updateRec := &admintools.MCPServerRecord{
			Name:    name,
			Type:    "http",
			URL:     "http://example.test/v2",
			Enabled: true,
		}
		require.NoError(t, adapter.Update(ctx, createRec.ID, updateRec),
			"admin url-only update must not violate chk_mcp_servers_auth_type")

		after, err := repo.GetByID(ctx, createRec.ID)
		require.NoError(t, err)
		assert.Equal(t, "http://example.test/v2", after.URL, "url must change")
		assert.Equal(t, "none", after.AuthType, "auth_type must stay 'none'")
	})

	t.Run("Case B: real auth field survives url-only update", func(t *testing.T) {
		const name = "f3-case-b"
		t.Cleanup(func() { db.Exec("DELETE FROM mcp_servers WHERE name = ?", name) })

		// Represents a server created via REST with a real auth field set
		// (the REST path calls repo.Create with the auth_* columns populated).
		created := &models.MCPServerModel{
			Name:       name,
			Type:       "http",
			URL:        "http://secure.test/v1",
			AuthType:   "api_key",
			AuthKeyEnv: "F3_CASE_B_KEY",
			Enabled:    true,
		}
		require.NoError(t, repo.Create(ctx, created))
		require.NotEmpty(t, created.ID)

		// Admin url-only update — must preserve auth_type + auth_key_env.
		updateRec := &admintools.MCPServerRecord{
			Name:    name,
			Type:    "http",
			URL:     "http://secure.test/v2",
			Enabled: true,
		}
		require.NoError(t, adapter.Update(ctx, created.ID, updateRec))

		after, err := repo.GetByID(ctx, created.ID)
		require.NoError(t, err)
		assert.Equal(t, "http://secure.test/v2", after.URL, "url must change")
		assert.Equal(t, "api_key", after.AuthType, "auth_type must survive")
		assert.Equal(t, "F3_CASE_B_KEY", after.AuthKeyEnv, "auth_key_env must survive")
	})
}
