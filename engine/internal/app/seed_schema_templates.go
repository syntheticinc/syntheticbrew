package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
)

// schemaTemplatesYAMLFilename is the on-disk source for template seeding. It
// sits alongside the engine binary in production deployments (Docker, bare
// metal) and in the repo root during development. Same search order as
// mcp-catalog.yaml.
const schemaTemplatesYAMLFilename = "schema-templates.yaml"

// schemaTemplatesYAMLShape mirrors the top-level shape of
// schema-templates.yaml. A flat list of entries is held under `templates`
// (distinct from `servers` in the MCP catalog YAML) so the two files stay
// visually distinguishable.
type schemaTemplatesYAMLShape struct {
	CatalogVersion string               `yaml:"catalog_version"`
	Templates      []schemaTemplateYAML `yaml:"templates"`
}

// schemaTemplateYAML mirrors one entry on disk. Separate from
// domain.SchemaTemplate because the wire shape carries a string category +
// a raw Definition that we pass through to the domain layer.
type schemaTemplateYAML struct {
	Name        string                          `yaml:"name"`
	Display     string                          `yaml:"display"`
	Description string                          `yaml:"description"`
	Category    string                          `yaml:"category"`
	Icon        string                          `yaml:"icon"`
	Version     string                          `yaml:"version"`
	Definition  domain.SchemaTemplateDefinition `yaml:"definition"`
}

// seedSchemaTemplates upserts every row from `schema-templates.yaml` into
// the `schema_templates` table. Called on every engine boot so edits to the
// shipped YAML propagate without writing a new Liquibase changeset.
// Idempotent — re-runs change nothing for unchanged rows and the row count
// stays equal to the YAML entry count.
//
// V2 Commit Group L (§2.2): catalog data is DB-backed; YAML is the seed
// format only. See docs/architecture/agent-first-runtime.md §2.2 and
// docs/plan/v2-cleanup-checklist.md "Commit Group L". Mirrors the
// seedMCPCatalog shape so the two catalogs behave identically at boot.
func seedSchemaTemplates(ctx context.Context, db *gorm.DB) {
	if db == nil {
		return
	}

	data, path, err := readSchemaTemplatesYAML()
	if err != nil {
		slog.WarnContext(ctx, "seed schema templates: YAML not found, skipping",
			"error", err)
		return
	}

	var parsed schemaTemplatesYAMLShape
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		slog.ErrorContext(ctx, "seed schema templates: parse YAML failed",
			"path", path, "error", err)
		return
	}
	if len(parsed.Templates) == 0 {
		slog.InfoContext(ctx, "seed schema templates: YAML has no templates, skipping", "path", path)
		return
	}

	repo := configrepo.NewGORMSchemaTemplateRepository(db)
	upserted := 0
	for _, entry := range parsed.Templates {
		if entry.Name == "" {
			continue
		}
		version := entry.Version
		if version == "" {
			version = "1.0"
		}
		t := domain.SchemaTemplate{
			Name:        entry.Name,
			Display:     entry.Display,
			Description: entry.Description,
			Category:    domain.SchemaTemplateCategory(entry.Category),
			Icon:        entry.Icon,
			Version:     version,
			Definition:  entry.Definition,
		}
		if err := repo.Upsert(ctx, t); err != nil {
			slog.WarnContext(ctx, "seed schema templates: upsert failed",
				"name", entry.Name, "error", err)
			continue
		}
		upserted++
	}

	slog.InfoContext(ctx, "seeded schema templates",
		"path", path,
		"yaml_version", parsed.CatalogVersion,
		"upserted", upserted,
		"total", len(parsed.Templates))
}

// readSchemaTemplatesYAML locates and reads schema-templates.yaml. Same
// search order as mcp-catalog.yaml:
//  1. Alongside the running binary (production / Docker deployment).
//  2. Current working directory (dev workflow via `go run ./cmd/ce`).
//
// Returns the raw bytes plus the resolved path (for logging).
func readSchemaTemplatesYAML() ([]byte, string, error) {
	candidates := make([]string, 0, 2)
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), schemaTemplatesYAMLFilename))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, schemaTemplatesYAMLFilename))
	}

	var lastErr error
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err == nil {
			return data, p, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no candidate paths")
	}
	return nil, "", fmt.Errorf("schema-templates.yaml not found in %v: %w", candidates, lastErr)
}
