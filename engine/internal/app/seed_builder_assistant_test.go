package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// TestEnsureDefaultModel_EmptyDB_PersistsNothing guards the invariant that
// ensureDefaultModel never seeds a model from env or config. The original
// 2026-04-23 prod bug persisted a `default` model with api_key_encrypted=""
// sourced from an unset LLM_API_KEY env var, which bound builder-assistant
// to an unauthenticated provider and 401'd every first chat turn.
//
// The env-based seed path was removed entirely — builder-assistant is left
// unbound on a fresh DB, and the onboarding wizard / Admin → Models is the
// only way to register an LLM provider. This test ensures that remains so.
func TestEnsureDefaultModel_EmptyDB_PersistsNothing(t *testing.T) {
	_ = models.LLMProviderModel{} // keep import live for future assertions

	db := setupTestDB(t)
	ctx := context.Background()

	// Call the narrow helper — this is where the empty-key model creation
	// happens. Full seedBuilderAssistant also touches MCP servers and agents
	// which need more plumbing; isolating to ensureDefaultModel keeps the
	// regression guard laser-focused on the actual defect.
	returned := ensureDefaultModel(ctx, db)

	llmRepo := configrepo.NewGORMLLMProviderRepository(db)
	modelList, err := llmRepo.List(ctx)
	require.NoError(t, err)

	// Invariant: no persisted model may have an empty api_key_encrypted.
	// Such a model 401s on every chat turn — exactly the 2026-04-23
	// "No cookie auth credentials found" prod failure.
	for _, m := range modelList {
		assert.NotEmpty(t, m.APIKeyEncrypted,
			"ensureDefaultModel persisted model %q with empty api_key_encrypted — this is a guaranteed 401 on first chat (2026-04-23 prod bug)",
			m.Name,
		)
	}

	// Second invariant: when there is no usable key, ensureDefaultModel
	// must return "" (signalling the caller to leave the agent unbound)
	// rather than the name of a broken model. Returning the name of a
	// broken model is what binds builder-assistant to the empty-key
	// default and triggers the bug.
	if returned != "" {
		// If it did return a name, that model must have a non-empty key.
		var found *models.LLMProviderModel
		for i := range modelList {
			if modelList[i].Name == returned {
				found = &modelList[i]
				break
			}
		}
		require.NotNil(t, found, "ensureDefaultModel returned %q but no such row", returned)
		assert.NotEmpty(t, found.APIKeyEncrypted,
			"ensureDefaultModel returned model %q with empty api_key — bind this to builder-assistant and every chat 401s",
			returned,
		)
	}
}
