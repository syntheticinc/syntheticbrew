package configrepo

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// GORMMCPCatalogRepository implements CRUD for the system-wide MCP catalog
// table. See domain.MCPCatalogRecord and docs/architecture/agent-first-runtime.md
// §5.5.
type GORMMCPCatalogRepository struct {
	db *gorm.DB
}

// NewGORMMCPCatalogRepository creates a new GORMMCPCatalogRepository.
func NewGORMMCPCatalogRepository(db *gorm.DB) *GORMMCPCatalogRepository {
	return &GORMMCPCatalogRepository{db: db}
}

// List returns all catalog entries ordered by display name.
func (r *GORMMCPCatalogRepository) List(ctx context.Context) ([]domain.MCPCatalogRecord, error) {
	var rows []models.MCPCatalogModel
	if err := r.db.WithContext(ctx).Order("display").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list mcp catalog: %w", err)
	}
	out := make([]domain.MCPCatalogRecord, len(rows))
	for i, row := range rows {
		out[i] = toCatalogRecord(row)
	}
	return out, nil
}

// GetByName returns a single catalog entry by its stable `name` key.
// Returns (nil, nil) when the entry is absent (ErrRecordNotFound).
func (r *GORMMCPCatalogRepository) GetByName(ctx context.Context, name string) (*domain.MCPCatalogRecord, error) {
	var row models.MCPCatalogModel
	err := r.db.WithContext(ctx).Where("name = ?", name).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get mcp catalog %s: %w", name, err)
	}
	rec := toCatalogRecord(row)
	return &rec, nil
}

// Upsert inserts or updates a catalog entry, keyed by `name`. Used by the
// startup seeder that parses `mcp-catalog.yaml` and synchronises rows into
// the table.
func (r *GORMMCPCatalogRepository) Upsert(ctx context.Context, rec domain.MCPCatalogRecord) error {
	row := fromCatalogRecord(rec)
	// ON CONFLICT (name) DO UPDATE — keep the seed table idempotent across
	// restarts and cover YAML edits without requiring a new Liquibase
	// changeset.
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"display",
			"description",
			"category",
			"verified",
			"packages",
			"provided_tools",
			"updated_at",
		}),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("upsert mcp catalog %s: %w", rec.Name, err)
	}
	return nil
}

// Count returns the number of rows in the catalog table. Used by tests to
// verify seed idempotency.
func (r *GORMMCPCatalogRepository) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.WithContext(ctx).Model(&models.MCPCatalogModel{}).Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count mcp catalog: %w", err)
	}
	return n, nil
}

func toCatalogRecord(m models.MCPCatalogModel) domain.MCPCatalogRecord {
	return domain.MCPCatalogRecord{
		ID:            m.ID,
		Name:          m.Name,
		Display:       m.Display,
		Description:   m.Description,
		Category:      domain.MCPCatalogCategory(m.Category),
		Verified:      m.Verified,
		Packages:      []domain.MCPCatalogPackage(m.Packages),
		ProvidedTools: []domain.MCPCatalogTool(m.ProvidedTools),
	}
}

func fromCatalogRecord(r domain.MCPCatalogRecord) models.MCPCatalogModel {
	return models.MCPCatalogModel{
		ID:            r.ID,
		Name:          r.Name,
		Display:       r.Display,
		Description:   r.Description,
		Category:      string(r.Category),
		Verified:      r.Verified,
		Packages:      models.MCPCatalogPackages(r.Packages),
		ProvidedTools: models.MCPCatalogTools(r.ProvidedTools),
	}
}
