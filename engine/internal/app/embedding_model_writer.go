package app

import (
	"context"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// embeddingModelConfigStore is the subset of the model repository the writer
// needs: list the tenant's models (to detect an existing embedding model) and
// insert a new one. The GORM LLM-provider repository satisfies it.
type embeddingModelConfigStore interface {
	List(ctx context.Context) ([]models.LLMProviderModel, error)
	Create(ctx context.Context, model *models.LLMProviderModel) error
}

// engineEmbeddingModelWriter is the concrete plugin.EmbeddingModelWriter the
// engine wires into the plugin at startup. It writes through the engine's own
// tenant-scoped model repository so a provisioning plugin can install a default
// embedding model without reimplementing the write or knowing the tenant
// context key — the same "use the engine's real code path" contract as the
// usage-limit writer and the tenant seeder.
type engineEmbeddingModelWriter struct {
	models embeddingModelConfigStore
}

// newEngineEmbeddingModelWriter constructs the writer over a model store.
func newEngineEmbeddingModelWriter(store embeddingModelConfigStore) *engineEmbeddingModelWriter {
	return &engineEmbeddingModelWriter{models: store}
}

// EnsureEmbeddingModel satisfies plugin.EmbeddingModelWriter. It writes the
// embedding model for tenantID only when the tenant has no embedding-kind model
// yet, never overwriting or duplicating one, so re-provisioning a tenant — or
// one that has since configured its own embedding model — is safe. The row
// carries the plugin-supplied base URL verbatim (a routing marker the engine
// never interprets) and no API key. Returns whether a row was written.
func (w *engineEmbeddingModelWriter) EnsureEmbeddingModel(ctx context.Context, tenantID, name, modelName, baseURL string, dim int) (bool, error) {
	if tenantID == "" {
		return false, fmt.Errorf("tenant_id is required")
	}

	// Scope the context to the tenant so the repository filters/stamps tenant_id.
	ctx = domain.WithTenantID(ctx, tenantID)

	existing, err := w.models.List(ctx)
	if err != nil {
		return false, fmt.Errorf("list tenant models: %w", err)
	}
	for i := range existing {
		if existing[i].Kind == "embedding" {
			return false, nil
		}
	}

	row := &models.LLMProviderModel{
		Name:            name,
		Type:            "openai_compatible",
		Kind:            "embedding",
		BaseURL:         baseURL,
		ModelName:       modelName,
		APIKeyEncrypted: "",
	}
	row.SetConfig(models.ModelConfig{EmbeddingDim: dim})

	if err := w.models.Create(ctx, row); err != nil {
		return false, fmt.Errorf("create default embedding model: %w", err)
	}
	return true, nil
}

var _ plugin.EmbeddingModelWriter = (*engineEmbeddingModelWriter)(nil)
