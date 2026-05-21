package configrepo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// GORMSchemaTemplateRepository implements CRUD for the system-wide schema
// template catalog (V2 Commit Group L, §2.2). See domain.SchemaTemplate and
// docs/architecture/agent-first-runtime.md §2.2.
type GORMSchemaTemplateRepository struct {
	db *gorm.DB
}

// NewGORMSchemaTemplateRepository creates a new GORMSchemaTemplateRepository.
func NewGORMSchemaTemplateRepository(db *gorm.DB) *GORMSchemaTemplateRepository {
	return &GORMSchemaTemplateRepository{db: db}
}

// List returns all catalog templates ordered by display name.
func (r *GORMSchemaTemplateRepository) List(ctx context.Context) ([]domain.SchemaTemplate, error) {
	var rows []models.SchemaTemplateModel
	if err := r.db.WithContext(ctx).Order("display").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list schema templates: %w", err)
	}
	out := make([]domain.SchemaTemplate, len(rows))
	for i, row := range rows {
		out[i] = toSchemaTemplate(row)
	}
	return out, nil
}

// ListByCategory returns templates filtered by category, ordered by display.
func (r *GORMSchemaTemplateRepository) ListByCategory(ctx context.Context, category domain.SchemaTemplateCategory) ([]domain.SchemaTemplate, error) {
	var rows []models.SchemaTemplateModel
	if err := r.db.WithContext(ctx).
		Where("category = ?", string(category)).
		Order("display").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list schema templates by category %s: %w", category, err)
	}
	out := make([]domain.SchemaTemplate, len(rows))
	for i, row := range rows {
		out[i] = toSchemaTemplate(row)
	}
	return out, nil
}

// GetByName returns a single template by its stable `name` key. Returns
// (nil, nil) when absent.
func (r *GORMSchemaTemplateRepository) GetByName(ctx context.Context, name string) (*domain.SchemaTemplate, error) {
	var row models.SchemaTemplateModel
	err := r.db.WithContext(ctx).Where("name = ?", name).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get schema template %s: %w", name, err)
	}
	t := toSchemaTemplate(row)
	return &t, nil
}

// Search returns templates whose name, display, or description contain the
// (case-insensitive) query substring. Mirrors the MCP catalog surface.
func (r *GORMSchemaTemplateRepository) Search(ctx context.Context, query string) ([]domain.SchemaTemplate, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return r.List(ctx)
	}
	like := "%" + q + "%"
	var rows []models.SchemaTemplateModel
	if err := r.db.WithContext(ctx).
		Where("LOWER(name) LIKE ? OR LOWER(display) LIKE ? OR LOWER(description) LIKE ?", like, like, like).
		Order("display").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("search schema templates %q: %w", query, err)
	}
	out := make([]domain.SchemaTemplate, len(rows))
	for i, row := range rows {
		out[i] = toSchemaTemplate(row)
	}
	return out, nil
}

// Upsert inserts or updates a template keyed by `name`. Used by the startup
// seeder that parses `schema-templates.yaml` and synchronises rows.
func (r *GORMSchemaTemplateRepository) Upsert(ctx context.Context, t domain.SchemaTemplate) error {
	row := fromSchemaTemplate(t)
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"display",
			"description",
			"category",
			"icon",
			"version",
			"definition",
			"updated_at",
		}),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("upsert schema template %s: %w", t.Name, err)
	}
	return nil
}

// Count returns the number of rows in the catalog table. Used by tests to
// verify seed idempotency.
func (r *GORMSchemaTemplateRepository) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.WithContext(ctx).Model(&models.SchemaTemplateModel{}).Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count schema templates: %w", err)
	}
	return n, nil
}

func toSchemaTemplate(m models.SchemaTemplateModel) domain.SchemaTemplate {
	return domain.SchemaTemplate{
		ID:          m.ID,
		Name:        m.Name,
		Display:     m.Display,
		Description: m.Description,
		Category:    domain.SchemaTemplateCategory(m.Category),
		Icon:        m.Icon,
		Version:     m.Version,
		Definition:  domain.SchemaTemplateDefinition(m.Definition),
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

func fromSchemaTemplate(t domain.SchemaTemplate) models.SchemaTemplateModel {
	return models.SchemaTemplateModel{
		ID:          t.ID,
		Name:        t.Name,
		Display:     t.Display,
		Description: t.Description,
		Category:    string(t.Category),
		Icon:        t.Icon,
		Version:     t.Version,
		Definition:  models.SchemaTemplateDefinitionJSON(t.Definition),
	}
}
