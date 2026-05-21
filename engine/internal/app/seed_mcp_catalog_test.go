package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
)

func catalogFixtureA() domain.MCPCatalogRecord {
	return domain.MCPCatalogRecord{
		Name:        "tavily-web-search",
		Display:     "Tavily Web Search",
		Description: "AI-optimized web search",
		Category:    domain.MCPCategorySearch,
		Verified:    true,
		Packages: []domain.MCPCatalogPackage{{
			Type:    "stdio",
			Command: "npx",
			Args:    []string{"-y", "@mcptools/mcp-tavily"},
		}},
	}
}

func catalogFixtureB() domain.MCPCatalogRecord {
	return domain.MCPCatalogRecord{
		Name:        "github",
		Display:     "GitHub",
		Description: "Create issues, PRs, search code",
		Category:    domain.MCPCategoryDevTools,
		Verified:    true,
	}
}

const testCatalogYAML = `catalog_version: "1.0"
servers:
  - name: "tavily-web-search"
    display: "Tavily Web Search"
    description: "AI-optimized web search"
    category: "search"
    verified: true
    packages:
      - type: "stdio"
        command: "npx"
        args: ["-y", "@mcptools/mcp-tavily"]
        env_vars:
          - name: "TAVILY_API_KEY"
            description: "Get key at tavily.com"
            required: true
            secret: true
    provided_tools:
      - name: "tavily_search"
        description: "Search the web"

  - name: "github"
    display: "GitHub"
    description: "Create issues, PRs, search code"
    category: "dev-tools"
    verified: true
    packages:
      - type: "stdio"
        command: "npx"
        args: ["-y", "@modelcontextprotocol/server-github"]
`

// mcpCatalogTableDDL mirrors the V2 mcp_catalog shape on SQLite. SQLite has no
// jsonb, but the MCPCatalogPackages / MCPCatalogTools Scan/Value implementations
// handle text payloads transparently.
const mcpCatalogTableDDL = `
CREATE TABLE mcp_catalog (
	id              TEXT PRIMARY KEY,
	name            TEXT NOT NULL UNIQUE,
	display         TEXT NOT NULL,
	description     TEXT,
	category        TEXT NOT NULL,
	verified        INTEGER NOT NULL DEFAULT 0,
	packages        TEXT NOT NULL DEFAULT '[]',
	provided_tools  TEXT,
	created_at      DATETIME,
	updated_at      DATETIME
)`

func setupCatalogTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)
	require.NoError(t, db.Exec(mcpCatalogTableDDL).Error)
	return db
}

// writeCatalogYAMLInDir writes `content` to mcp-catalog.yaml inside dir and
// chdirs the process into dir for the duration of the test. Returns the path.
func writeCatalogYAMLInDir(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, mcpCatalogYAMLFilename)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(orig) })
	return path
}

// TestSeedMCPCatalog_Idempotent verifies that running the seeder twice keeps
// the row count equal to the YAML entry count — the ON CONFLICT (name) path
// updates in place, it never inserts duplicates.
func TestSeedMCPCatalog_Idempotent(t *testing.T) {
	db := setupCatalogTestDB(t)
	writeCatalogYAMLInDir(t, testCatalogYAML)

	ctx := context.Background()
	seedMCPCatalog(ctx, db)

	repo := configrepo.NewGORMMCPCatalogRepository(db)
	n1, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n1, "after first seed row count must match YAML entries")

	// Second call — row count must not change.
	seedMCPCatalog(ctx, db)
	n2, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, n1, n2, "seedMCPCatalog must be idempotent")

	// Verify content round-trips.
	rec, err := repo.GetByName(ctx, "tavily-web-search")
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, "Tavily Web Search", rec.Display)
	assert.True(t, rec.Verified)
	require.Len(t, rec.Packages, 1)
	assert.Equal(t, "stdio", rec.Packages[0].Type)
	require.Len(t, rec.Packages[0].EnvVars, 1)
	assert.Equal(t, "TAVILY_API_KEY", rec.Packages[0].EnvVars[0].Name)
	assert.True(t, rec.Packages[0].EnvVars[0].Required)
	require.Len(t, rec.ProvidedTools, 1)
	assert.Equal(t, "tavily_search", rec.ProvidedTools[0].Name)
}

// TestSeedMCPCatalog_SkipsMissingYAML confirms a missing YAML is a warning,
// not a crash — engine boot must never block on an optional seed.
func TestSeedMCPCatalog_SkipsMissingYAML(t *testing.T) {
	db := setupCatalogTestDB(t)
	// Deliberately do not write the YAML; chdir into a fresh tempdir.
	dir := t.TempDir()
	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(orig) })

	ctx := context.Background()
	seedMCPCatalog(ctx, db) // must not panic

	repo := configrepo.NewGORMMCPCatalogRepository(db)
	n, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n, "no YAML means no rows seeded")
}

// TestGORMMCPCatalogRepository_UpsertAndList exercises the repo surface in
// isolation.
func TestGORMMCPCatalogRepository_UpsertAndList(t *testing.T) {
	db := setupCatalogTestDB(t)
	repo := configrepo.NewGORMMCPCatalogRepository(db)
	ctx := context.Background()

	// Initial upsert.
	require.NoError(t, repo.Upsert(ctx, catalogFixtureA()))
	require.NoError(t, repo.Upsert(ctx, catalogFixtureB()))

	list, err := repo.List(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 2)

	// Upsert with same name changes fields, not row count.
	updated := catalogFixtureA()
	updated.Display = "Tavily (updated)"
	require.NoError(t, repo.Upsert(ctx, updated))

	list2, err := repo.List(ctx)
	require.NoError(t, err)
	assert.Len(t, list2, 2, "upsert on existing name must not create a duplicate row")

	rec, err := repo.GetByName(ctx, updated.Name)
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, "Tavily (updated)", rec.Display)

	// GetByName on missing row → (nil, nil).
	missing, err := repo.GetByName(ctx, "nonexistent")
	require.NoError(t, err)
	assert.Nil(t, missing)
}
