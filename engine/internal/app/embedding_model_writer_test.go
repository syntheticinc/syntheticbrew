package app

import (
	"context"
	"errors"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// The writer treats baseURL as an opaque pass-through — it never interprets the
// value, so the test uses a generic marker rather than any plugin-specific
// routing convention (that convention lives entirely outside CE).
const (
	testEmbedName  = "test-embedder"
	testEmbedModel = "some/embedding-model"
	testEmbedURL   = "opaque://marker"
)

// fakeEmbeddingModelStore records the List/Create calls and the tenant stamped
// into the context so the writer's idempotency check, scoping and row shape can
// be asserted without a database. `existing` models what the repository returns
// for the tenant's current models.
type fakeEmbeddingModelStore struct {
	existing  []models.LLMProviderModel
	listErr   error
	createErr error

	listTenant   string
	created      *models.LLMProviderModel
	createTenant string
}

func (f *fakeEmbeddingModelStore) List(ctx context.Context) ([]models.LLMProviderModel, error) {
	f.listTenant = domain.TenantIDFromContext(ctx)
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.existing, nil
}

func (f *fakeEmbeddingModelStore) Create(ctx context.Context, model *models.LLMProviderModel) error {
	f.createTenant = domain.TenantIDFromContext(ctx)
	if f.createErr != nil {
		return f.createErr
	}
	f.created = model
	return nil
}

func TestEngineEmbeddingModelWriter_WritesWhenAbsent(t *testing.T) {
	store := &fakeEmbeddingModelStore{
		existing: []models.LLMProviderModel{{Kind: "chat", ModelName: "gpt-4o"}},
	}
	w := newEngineEmbeddingModelWriter(store)

	wrote, err := w.EnsureEmbeddingModel(context.Background(), "tenant-a",
		testEmbedName, testEmbedModel, testEmbedURL, 1536)
	if err != nil {
		t.Fatalf("EnsureEmbeddingModel: unexpected error: %v", err)
	}
	if !wrote {
		t.Fatal("expected wrote=true when no embedding model exists")
	}
	if store.created == nil {
		t.Fatal("expected Create to be called")
	}
	if store.created.Name != testEmbedName {
		t.Errorf("name = %q, want the caller-supplied display name %q", store.created.Name, testEmbedName)
	}
	if store.created.Kind != "embedding" {
		t.Errorf("kind = %q, want embedding", store.created.Kind)
	}
	if store.created.Type != "openai_compatible" {
		t.Errorf("type = %q, want openai_compatible", store.created.Type)
	}
	if store.created.BaseURL != testEmbedURL {
		t.Errorf("base URL = %q, want the marker passed in verbatim", store.created.BaseURL)
	}
	if store.created.ModelName != testEmbedModel {
		t.Errorf("model name = %q, want %q", store.created.ModelName, testEmbedModel)
	}
	if store.created.APIKeyEncrypted != "" {
		t.Errorf("api key = %q, want empty (no key stored on the row)", store.created.APIKeyEncrypted)
	}
	if dim := store.created.GetConfig().EmbeddingDim; dim != 1536 {
		t.Errorf("embedding_dim = %d, want 1536", dim)
	}
	// Both reads and the write must be scoped to the tenant.
	if store.listTenant != "tenant-a" || store.createTenant != "tenant-a" {
		t.Fatalf("expected tenant scoping, got list=%q create=%q", store.listTenant, store.createTenant)
	}
}

func TestEngineEmbeddingModelWriter_IdempotentWhenExists(t *testing.T) {
	store := &fakeEmbeddingModelStore{
		existing: []models.LLMProviderModel{{Kind: "embedding", ModelName: "own-embedder"}},
	}
	w := newEngineEmbeddingModelWriter(store)

	wrote, err := w.EnsureEmbeddingModel(context.Background(), "tenant-a",
		testEmbedName, testEmbedModel, testEmbedURL, 1536)
	if err != nil {
		t.Fatalf("EnsureEmbeddingModel: unexpected error: %v", err)
	}
	if wrote {
		t.Fatal("expected wrote=false when the tenant already has an embedding model")
	}
	if store.created != nil {
		t.Fatal("Create must not run when an embedding model already exists")
	}
}

func TestEngineEmbeddingModelWriter_RequiresTenant(t *testing.T) {
	store := &fakeEmbeddingModelStore{}
	w := newEngineEmbeddingModelWriter(store)

	_, err := w.EnsureEmbeddingModel(context.Background(), "",
		testEmbedName, testEmbedModel, testEmbedURL, 1536)
	if err == nil {
		t.Fatal("expected error for empty tenant_id")
	}
	if store.created != nil {
		t.Fatal("no repository write expected without a tenant")
	}
}

func TestEngineEmbeddingModelWriter_PropagatesListError(t *testing.T) {
	store := &fakeEmbeddingModelStore{listErr: errors.New("db down")}
	w := newEngineEmbeddingModelWriter(store)

	_, err := w.EnsureEmbeddingModel(context.Background(), "tenant-a",
		testEmbedName, testEmbedModel, testEmbedURL, 1536)
	if err == nil {
		t.Fatal("expected the list error to propagate")
	}
	if store.created != nil {
		t.Fatal("Create must not run when the existence check fails")
	}
}

func TestEngineEmbeddingModelWriter_PropagatesCreateError(t *testing.T) {
	store := &fakeEmbeddingModelStore{createErr: errors.New("db down")}
	w := newEngineEmbeddingModelWriter(store)

	_, err := w.EnsureEmbeddingModel(context.Background(), "tenant-a",
		testEmbedName, testEmbedModel, testEmbedURL, 1536)
	if err == nil {
		t.Fatal("expected the create error to propagate")
	}
}
