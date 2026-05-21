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

// schemaTemplatesTableDDL mirrors the V2 schema_templates shape on SQLite.
// SQLite has no jsonb, but SchemaTemplateDefinitionJSON Scan/Value handle
// text payloads transparently.
const schemaTemplatesTableDDL = `
CREATE TABLE schema_templates (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    display     TEXT NOT NULL,
    description TEXT,
    category    TEXT NOT NULL,
    icon        TEXT,
    version     TEXT NOT NULL DEFAULT '1.0',
    definition  TEXT NOT NULL,
    created_at  DATETIME,
    updated_at  DATETIME
)`

func setupSchemaTemplateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)
	require.NoError(t, db.Exec(schemaTemplatesTableDDL).Error)
	return db
}

const testSchemaTemplatesYAML = `catalog_version: "1.0"
templates:
  - name: "customer-support-basic"
    display: "Customer Support (Basic)"
    description: "Triage + resolver"
    category: "support"
    icon: "headset"
    version: "1.0"
    definition:
      entry_agent_name: "triage"
      agents:
        - name: "triage"
          system_prompt: "You are the triage agent."
          capabilities:
            - type: "memory"
              config: {}
        - name: "resolver"
          system_prompt: "You resolve tickets."
          capabilities:
            - type: "knowledge"
              config: {}
      relations:
        - source: "triage"
          target: "resolver"
      triggers:
        - type: "chat"
          title: "Main Chat"
          enabled: true
          config: {}
  - name: "generic-hello-world"
    display: "Generic Assistant"
    description: "Single agent hello world."
    category: "generic"
    icon: "sparkles"
    version: "1.0"
    definition:
      entry_agent_name: "assistant"
      agents:
        - name: "assistant"
          system_prompt: "You are helpful."
          capabilities: []
      relations: []
      triggers:
        - type: "chat"
          title: "Chat"
          enabled: true
          config: {}
`

// writeSchemaTemplatesYAMLInDir writes the YAML into a temp dir and chdir's
// the process there for the lifetime of the test.
func writeSchemaTemplatesYAMLInDir(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, schemaTemplatesYAMLFilename)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(orig) })
	return path
}

// TestSeedSchemaTemplates_Idempotent verifies re-running the seeder keeps
// the row count constant — upsert updates in place, never inserts a
// duplicate.
func TestSeedSchemaTemplates_Idempotent(t *testing.T) {
	db := setupSchemaTemplateTestDB(t)
	writeSchemaTemplatesYAMLInDir(t, testSchemaTemplatesYAML)

	ctx := context.Background()
	seedSchemaTemplates(ctx, db)

	repo := configrepo.NewGORMSchemaTemplateRepository(db)
	n1, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n1, "after first seed row count must match YAML entries")

	seedSchemaTemplates(ctx, db)
	n2, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, n1, n2, "seedSchemaTemplates must be idempotent")

	// Verify content round-trips cleanly through jsonb.
	tmpl, err := repo.GetByName(ctx, "customer-support-basic")
	require.NoError(t, err)
	require.NotNil(t, tmpl)
	assert.Equal(t, "Customer Support (Basic)", tmpl.Display)
	assert.Equal(t, domain.SchemaTemplateCategorySupport, tmpl.Category)
	assert.Equal(t, "headset", tmpl.Icon)
	assert.Equal(t, "1.0", tmpl.Version)
	require.Len(t, tmpl.Definition.Agents, 2)
	assert.Equal(t, "triage", tmpl.Definition.EntryAgentName)
	require.Len(t, tmpl.Definition.Relations, 1)
	assert.Equal(t, "triage", tmpl.Definition.Relations[0].Source)
	assert.Equal(t, "resolver", tmpl.Definition.Relations[0].Target)
}

// TestSeedSchemaTemplates_SkipsMissingYAML confirms a missing YAML is a
// warning, not a crash — engine boot must never block on an optional seed.
func TestSeedSchemaTemplates_SkipsMissingYAML(t *testing.T) {
	db := setupSchemaTemplateTestDB(t)

	// Deliberately do not write the YAML; chdir into a fresh tempdir.
	dir := t.TempDir()
	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(orig) })

	ctx := context.Background()
	seedSchemaTemplates(ctx, db) // must not panic

	repo := configrepo.NewGORMSchemaTemplateRepository(db)
	n, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n, "no YAML means no rows seeded")
}

// TestGORMSchemaTemplateRepository_ListByCategory exercises category
// filtering directly against the repo.
func TestGORMSchemaTemplateRepository_ListByCategory(t *testing.T) {
	db := setupSchemaTemplateTestDB(t)
	writeSchemaTemplatesYAMLInDir(t, testSchemaTemplatesYAML)

	ctx := context.Background()
	seedSchemaTemplates(ctx, db)

	repo := configrepo.NewGORMSchemaTemplateRepository(db)
	supportOnly, err := repo.ListByCategory(ctx, domain.SchemaTemplateCategorySupport)
	require.NoError(t, err)
	require.Len(t, supportOnly, 1)
	assert.Equal(t, "customer-support-basic", supportOnly[0].Name)

	genericOnly, err := repo.ListByCategory(ctx, domain.SchemaTemplateCategoryGeneric)
	require.NoError(t, err)
	require.Len(t, genericOnly, 1)
	assert.Equal(t, "generic-hello-world", genericOnly[0].Name)

	salesOnly, err := repo.ListByCategory(ctx, domain.SchemaTemplateCategorySales)
	require.NoError(t, err)
	assert.Empty(t, salesOnly)
}

// TestGORMSchemaTemplateRepository_Search exercises substring matching.
func TestGORMSchemaTemplateRepository_Search(t *testing.T) {
	db := setupSchemaTemplateTestDB(t)
	writeSchemaTemplatesYAMLInDir(t, testSchemaTemplatesYAML)

	ctx := context.Background()
	seedSchemaTemplates(ctx, db)

	repo := configrepo.NewGORMSchemaTemplateRepository(db)

	hits, err := repo.Search(ctx, "triage")
	require.NoError(t, err)
	require.Len(t, hits, 1, "Search matches description text")
	assert.Equal(t, "customer-support-basic", hits[0].Name)

	// Empty query returns everything.
	all, err := repo.Search(ctx, "")
	require.NoError(t, err)
	assert.Len(t, all, 2)
}
