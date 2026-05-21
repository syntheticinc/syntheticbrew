package configrepo

import (
	"context"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"gorm.io/gorm"
)

// GORMLLMProviderRepository implements LLM provider CRUD using GORM.
// Note: `models` (model configurations) ARE tenant-scoped — they carry
// per-tenant API keys, base URLs, etc. Global provider-kind enumerations are
// not persisted in this table.
type GORMLLMProviderRepository struct {
	db *gorm.DB
}

// NewGORMLLMProviderRepository creates a new GORMLLMProviderRepository.
func NewGORMLLMProviderRepository(db *gorm.DB) *GORMLLMProviderRepository {
	return &GORMLLMProviderRepository{db: db}
}

// List returns all LLM provider models for the current tenant.
func (r *GORMLLMProviderRepository) List(ctx context.Context) ([]models.LLMProviderModel, error) {
	var providers []models.LLMProviderModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Order("name").
		Find(&providers).Error; err != nil {
		return nil, fmt.Errorf("list llm providers: %w", err)
	}
	return providers, nil
}

// GetByID returns a single LLM provider model by ID (tenant-scoped).
func (r *GORMLLMProviderRepository) GetByID(ctx context.Context, id string) (*models.LLMProviderModel, error) {
	var provider models.LLMProviderModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("id = ?", id).
		First(&provider).Error; err != nil {
		return nil, fmt.Errorf("get llm provider %s: %w", id, err)
	}
	return &provider, nil
}

// Create inserts a new LLM provider model, stamping tenant from context.
func (r *GORMLLMProviderRepository) Create(ctx context.Context, model *models.LLMProviderModel) error {
	model.TenantID = tenantIDFromCtx(ctx)
	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		return fmt.Errorf("create llm provider: %w", err)
	}
	return nil
}

// Update updates an LLM provider model by ID (tenant-scoped).
func (r *GORMLLMProviderRepository) Update(ctx context.Context, id string, model *models.LLMProviderModel) error {
	result := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Model(&models.LLMProviderModel{}).
		Where("id = ?", id).
		Updates(model)
	if result.Error != nil {
		return fmt.Errorf("update llm provider: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("llm provider not found: %s", id)
	}
	return nil
}

// Delete removes an LLM provider model by ID (tenant-scoped).
func (r *GORMLLMProviderRepository) Delete(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Delete(&models.LLMProviderModel{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("delete llm provider: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("llm provider not found: %s", id)
	}
	return nil
}

// GetModelKind returns the kind ('chat' or 'embedding') for the given model ID (tenant-scoped).
// Returns an empty string and an error when the model does not exist in the tenant.
func (r *GORMLLMProviderRepository) GetModelKind(ctx context.Context, id string) (string, error) {
	var kind string
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Model(&models.LLMProviderModel{}).
		Where("id = ?", id).
		Pluck("kind", &kind).Error; err != nil {
		return "", fmt.Errorf("get model kind %s: %w", id, err)
	}
	if kind == "" {
		return "", fmt.Errorf("model not found: %s", id)
	}
	return kind, nil
}

// GetDefault returns the tenant's default model for the given kind (usually "chat").
// Returns (nil, nil) when no default has been set — callers should treat this as
// "no usable default" rather than an error. Kind is stored as a plain discriminator
// (no parameterisation beyond the string); the partial unique index keyed on
// `(tenant_id) WHERE is_default=TRUE AND kind='chat'` means at most one row
// can match for chat today.
func (r *GORMLLMProviderRepository) GetDefault(ctx context.Context, kind string) (*models.LLMProviderModel, error) {
	if kind == "" {
		kind = "chat"
	}
	var provider models.LLMProviderModel
	err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("is_default = ? AND kind = ?", true, kind).
		First(&provider).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("get default %s model: %w", kind, err)
	}
	return &provider, nil
}

// SetDefault atomically promotes the given model to the tenant's default chat
// model. In a single transaction: clears is_default=false on every other
// chat model in the tenant, then flips the target to true. The partial unique
// index idx_models_tenant_default_chat enforces the invariant at the DB level
// — if two concurrent callers race, exactly one commits and the other gets
// the underlying pq error back unwrapped so callers can detect the 23505
// unique-violation code and retry.
func (r *GORMLLMProviderRepository) SetDefault(ctx context.Context, modelID string) error {
	tenantID := tenantIDFromCtx(ctx)
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. Clear the current default(s). Using WHERE is_default=TRUE keeps the
		//    index happy: no row is ever simultaneously old-true and new-true.
		if err := tx.Model(&models.LLMProviderModel{}).
			Where("tenant_id = ? AND kind = 'chat' AND is_default = TRUE AND id != ?", tenantID, modelID).
			Update("is_default", false).Error; err != nil {
			return err
		}
		// 2. Promote the target row. Scoping by tenant prevents cross-tenant
		//    hijacking via crafted modelID.
		result := tx.Model(&models.LLMProviderModel{}).
			Where("id = ? AND tenant_id = ?", modelID, tenantID).
			Update("is_default", true)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("model not found: %s", modelID)
		}
		return nil
	})
}

// AgentsUsingModel returns the names of agents that reference the given model ID (tenant-scoped).
func (r *GORMLLMProviderRepository) AgentsUsingModel(ctx context.Context, modelID string) ([]string, error) {
	var names []string
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Model(&models.AgentModel{}).
		Where("model_id = ?", modelID).
		Order("name").
		Pluck("name", &names).Error; err != nil {
		return nil, fmt.Errorf("list agents using model %s: %w", modelID, err)
	}
	return names, nil
}
