package mcp

import (
	"context"
	"log/slog"
	"strings"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// Consumer-side interface — the repository shape CatalogService needs. The
// actual implementation lives in
// `internal/infrastructure/persistence/configrepo.GORMMCPCatalogRepository`.
type catalogRepository interface {
	List(ctx context.Context) ([]domain.MCPCatalogRecord, error)
	GetByName(ctx context.Context, name string) (*domain.MCPCatalogRecord, error)
}

// CatalogVersion is the version string returned by CatalogService.Version().
// V2 Commit Group C (§5.5): the catalog moved from runtime YAML reads to the
// `mcp_catalog` DB table seeded from `mcp-catalog.yaml` at engine startup.
// The YAML `catalog_version` tag is no longer load-bearing — the DB is the
// source of truth and individual rows are addressable by `name`. We still
// publish a top-level version string on `GET /api/v1/mcp/catalog` for the
// admin UI.
const CatalogVersion = "1.0"

// CatalogService loads and manages the MCP server catalog.
//
// All reads hit the DB via the injected repository. No filesystem access at
// query time.
type CatalogService struct {
	repo catalogRepository
}

// NewCatalogService creates a catalog service backed by the supplied
// repository.
func NewCatalogService(repo catalogRepository) *CatalogService {
	return &CatalogService{repo: repo}
}

// List returns all catalog entries as domain.MCPCatalogEntry (the shape the
// admin UI and `GET /api/v1/mcp/catalog` already consume).
func (s *CatalogService) List() []domain.MCPCatalogEntry {
	records, err := s.repo.List(context.Background())
	if err != nil {
		slog.WarnContext(context.Background(), "[MCPCatalog] list failed", "error", err)
		return []domain.MCPCatalogEntry{}
	}
	return toEntries(records)
}

// ListByCategory returns catalog entries filtered by category.
func (s *CatalogService) ListByCategory(category domain.MCPCatalogCategory) []domain.MCPCatalogEntry {
	records, err := s.repo.List(context.Background())
	if err != nil {
		slog.WarnContext(context.Background(), "[MCPCatalog] list-by-category failed", "error", err)
		return []domain.MCPCatalogEntry{}
	}
	out := make([]domain.MCPCatalogEntry, 0, len(records))
	for _, r := range records {
		if r.Category == category {
			out = append(out, toEntry(r))
		}
	}
	return out
}

// Search returns catalog entries matching the query (name / display / description).
func (s *CatalogService) Search(query string) []domain.MCPCatalogEntry {
	records, err := s.repo.List(context.Background())
	if err != nil {
		slog.WarnContext(context.Background(), "[MCPCatalog] search failed", "error", err)
		return []domain.MCPCatalogEntry{}
	}
	q := strings.ToLower(query)
	out := make([]domain.MCPCatalogEntry, 0, len(records))
	for _, r := range records {
		if strings.Contains(strings.ToLower(r.Name), q) ||
			strings.Contains(strings.ToLower(r.Display), q) ||
			strings.Contains(strings.ToLower(r.Description), q) {
			out = append(out, toEntry(r))
		}
	}
	return out
}

// GetByName returns a specific catalog entry by name.
// ctx is accepted for interface uniformity (ctx-doctrine); the MCP catalog is a
// process-global shared table — there is no per-tenant dispatch here.
func (s *CatalogService) GetByName(_ context.Context, name string) (*domain.MCPCatalogEntry, bool) {
	rec, err := s.repo.GetByName(context.Background(), name)
	if err != nil {
		slog.WarnContext(context.Background(), "[MCPCatalog] get-by-name failed", "name", name, "error", err)
		return nil, false
	}
	if rec == nil {
		return nil, false
	}
	entry := toEntry(*rec)
	return &entry, true
}

// Version returns the catalog version string.
func (s *CatalogService) Version() string { return CatalogVersion }

// toEntry projects the DB-backed record into the wire entry shape consumed by
// the admin UI (`MCPCatalogEntry` is the YAML-shaped representation).
func toEntry(r domain.MCPCatalogRecord) domain.MCPCatalogEntry {
	return domain.MCPCatalogEntry{
		Name:          r.Name,
		Display:       r.Display,
		Description:   r.Description,
		Category:      r.Category,
		Verified:      r.Verified,
		Packages:      r.Packages,
		ProvidedTools: r.ProvidedTools,
	}
}

func toEntries(records []domain.MCPCatalogRecord) []domain.MCPCatalogEntry {
	out := make([]domain.MCPCatalogEntry, len(records))
	for i, r := range records {
		out[i] = toEntry(r)
	}
	return out
}
