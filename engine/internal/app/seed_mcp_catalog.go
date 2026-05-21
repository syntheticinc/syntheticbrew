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

// mcpCatalogYAMLFilename is the on-disk source for catalog seeding. It sits
// alongside the engine binary in production deployments (Docker, bare metal)
// and in the repo root during development.
const mcpCatalogYAMLFilename = "mcp-catalog.yaml"

// mcpCatalogYAMLShape mirrors the top-level shape of mcp-catalog.yaml so we
// can parse it without pulling in the deprecated runtime-read helpers.
type mcpCatalogYAMLShape struct {
	CatalogVersion string                   `yaml:"catalog_version"`
	Servers        []domain.MCPCatalogEntry `yaml:"servers"`
}

// seedMCPCatalog upserts every row from `mcp-catalog.yaml` into the
// `mcp_catalog` table. Called on every engine boot so that edits to the
// shipped YAML propagate without writing a new Liquibase changeset per
// update. Idempotent — re-runs change nothing for unchanged rows and the row
// count stays equal to the YAML entry count.
//
// V2 Commit Group C (§5.5): catalog data is now DB-backed; YAML is the seed
// format only. See docs/architecture/agent-first-runtime.md §5.5 and
// docs/plan/v2-cleanup-checklist.md "Commit Group C".
func seedMCPCatalog(ctx context.Context, db *gorm.DB) {
	if db == nil {
		return
	}

	data, path, err := readMCPCatalogYAML()
	if err != nil {
		slog.WarnContext(ctx, "seed mcp catalog: YAML not found, skipping",
			"error", err)
		return
	}

	var parsed mcpCatalogYAMLShape
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		slog.ErrorContext(ctx, "seed mcp catalog: parse YAML failed",
			"path", path, "error", err)
		return
	}
	if len(parsed.Servers) == 0 {
		slog.InfoContext(ctx, "seed mcp catalog: YAML has no servers, skipping", "path", path)
		return
	}

	repo := configrepo.NewGORMMCPCatalogRepository(db)
	upserted := 0
	for _, entry := range parsed.Servers {
		if entry.Name == "" {
			continue
		}
		rec := domain.MCPCatalogRecord{
			Name:          entry.Name,
			Display:       entry.Display,
			Description:   entry.Description,
			Category:      entry.Category,
			Verified:      entry.Verified,
			Packages:      entry.Packages,
			ProvidedTools: entry.ProvidedTools,
		}
		if err := repo.Upsert(ctx, rec); err != nil {
			slog.WarnContext(ctx, "seed mcp catalog: upsert failed",
				"name", entry.Name, "error", err)
			continue
		}
		upserted++
	}

	slog.InfoContext(ctx, "seeded mcp catalog",
		"path", path,
		"yaml_version", parsed.CatalogVersion,
		"upserted", upserted,
		"total", len(parsed.Servers))
}

// readMCPCatalogYAML locates and reads mcp-catalog.yaml. Search order:
// 1. Alongside the running binary (production / Docker deployment).
// 2. Current working directory (dev workflow via `go run ./cmd/ce`).
// Returns the raw bytes plus the resolved path used (for logging).
func readMCPCatalogYAML() ([]byte, string, error) {
	candidates := make([]string, 0, 2)
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), mcpCatalogYAMLFilename))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, mcpCatalogYAMLFilename))
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
	return nil, "", fmt.Errorf("mcp-catalog.yaml not found in %v: %w", candidates, lastErr)
}
