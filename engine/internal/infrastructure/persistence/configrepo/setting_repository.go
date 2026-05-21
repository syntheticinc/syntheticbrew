package configrepo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GORMSettingRepository implements settings key-value CRUD using GORM.
//
// V2 final shape (Commit Group G, §5.8): the underlying column is jsonb
// with a (tenant_id, key) composite PK. Callers that previously stored
// plain strings get the same Set/Get strings interface (encoded as a
// jsonb string), and callers that need structured values (arrays,
// objects) use SetJSON/GetJSON which deal in raw json.RawMessage.
type GORMSettingRepository struct {
	db *gorm.DB
}

// NewGORMSettingRepository creates a new GORMSettingRepository.
func NewGORMSettingRepository(db *gorm.DB) *GORMSettingRepository {
	return &GORMSettingRepository{db: db}
}

// List returns all settings for the tenant resolved from context.
func (r *GORMSettingRepository) List(ctx context.Context) ([]models.SettingModel, error) {
	var settings []models.SettingModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Order(`"key"`).
		Find(&settings).Error; err != nil {
		return nil, fmt.Errorf("list settings: %w", err)
	}
	return settings, nil
}

// Get returns a single setting by key for the tenant resolved from context.
// Returns nil if not found.
func (r *GORMSettingRepository) Get(ctx context.Context, key string) (*models.SettingModel, error) {
	var setting models.SettingModel
	err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("key = ?", key).
		First(&setting).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("get setting %q: %w", key, err)
	}
	return &setting, nil
}

// Set upserts a string setting value by key for the tenant resolved from context.
// The string is encoded as a jsonb string (e.g. "true" → `"true"`).
func (r *GORMSettingRepository) Set(ctx context.Context, key, value string) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode setting %q: %w", key, err)
	}
	return r.SetJSON(ctx, key, encoded)
}

// SetJSON upserts a raw jsonb setting value by key for the tenant resolved from context.
// The caller is responsible for the JSON encoding — pass the marshalled
// bytes (e.g. `[]byte("true")`, `[]byte("[\"openai\",\"anthropic\"]")`).
func (r *GORMSettingRepository) SetJSON(ctx context.Context, key string, value []byte) error {
	setting := models.SettingModel{
		TenantID: tenantIDFromCtx(ctx),
		Key:      key,
		Value:    value,
	}
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(&setting).Error
	if err != nil {
		return fmt.Errorf("set setting %q: %w", key, err)
	}
	return nil
}
