package llm

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	err = db.Exec(`CREATE TABLE models (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		type VARCHAR(30) NOT NULL,
		kind VARCHAR(20) NOT NULL DEFAULT 'chat',
		is_default BOOLEAN NOT NULL DEFAULT FALSE,
		base_url VARCHAR(500),
		model_name VARCHAR(255) NOT NULL,
		api_key_encrypted VARCHAR(1000),
		api_version VARCHAR(30) DEFAULT '',
		config TEXT NOT NULL DEFAULT '{}',
		tenant_id TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
		created_at DATETIME,
		updated_at DATETIME
	)`).Error
	require.NoError(t, err)

	return db
}

func seedModel(t *testing.T, db *gorm.DB, name, providerType, baseURL, modelName, apiKey string) string {
	t.Helper()
	m := &models.LLMProviderModel{
		ID:              "test-" + name,
		Name:            name,
		Type:            providerType,
		BaseURL:         baseURL,
		ModelName:       modelName,
		APIKeyEncrypted: apiKey,
	}
	err := db.Create(m).Error
	require.NoError(t, err)
	return m.ID
}

func TestModelCache_Get_NotFound(t *testing.T) {
	db := setupTestDB(t)
	cache := NewModelCache(db, nil)

	_, _, err := cache.Get(context.Background(), "nonexistent-999")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model ID nonexistent-999 not found")
}

func TestModelCache_Get_UnsupportedType(t *testing.T) {
	db := setupTestDB(t)
	id := seedModel(t, db, "bad-model", "unknown_provider", "http://localhost", "test", "")

	cache := NewModelCache(db, nil)
	_, _, err := cache.Get(context.Background(), id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported provider type")
}

func TestModelCache_Invalidate(t *testing.T) {
	db := setupTestDB(t)
	cache := NewModelCache(db, nil)

	// Pre-populate cache manually to avoid needing a real LLM server.
	mockClient := &mockChatModel{id: "cached"}
	cache.mu.Lock()
	cache.clients["test-42"] = &cachedModel{resolved: &ResolvedModel{Client: mockClient, Name: "test-model"}}
	cache.mu.Unlock()

	// Verify it's cached.
	cache.mu.RLock()
	_, exists := cache.clients["test-42"]
	cache.mu.RUnlock()
	assert.True(t, exists)

	// Invalidate.
	cache.Invalidate("test-42")

	// Verify removed.
	cache.mu.RLock()
	_, exists = cache.clients["test-42"]
	cache.mu.RUnlock()
	assert.False(t, exists)
}

func TestModelCache_InvalidateAll(t *testing.T) {
	cache := NewModelCache(nil, nil)

	cache.mu.Lock()
	cache.clients["1"] = &cachedModel{resolved: &ResolvedModel{Client: &mockChatModel{id: "a"}, Name: "a"}}
	cache.clients["2"] = &cachedModel{resolved: &ResolvedModel{Client: &mockChatModel{id: "b"}, Name: "b"}}
	cache.mu.Unlock()

	cache.InvalidateAll()

	cache.mu.RLock()
	assert.Empty(t, cache.clients)
	cache.mu.RUnlock()
}

func TestModelCache_Get_CacheHit(t *testing.T) {
	cache := NewModelCache(nil, nil)
	mockClient := &mockChatModel{id: "cached-model"}

	cache.mu.Lock()
	cache.clients["10"] = &cachedModel{resolved: &ResolvedModel{Client: mockClient, Name: "gpt-4"}}
	cache.mu.Unlock()

	client, name, err := cache.Get(context.Background(), "10")
	require.NoError(t, err)
	assert.Equal(t, mockClient, client)
	assert.Equal(t, "gpt-4", name)
}

func TestModelCache_ConcurrentAccess(t *testing.T) {
	cache := NewModelCache(nil, nil)
	mockClient := &mockChatModel{id: "concurrent"}

	cache.mu.Lock()
	cache.clients["1"] = &cachedModel{resolved: &ResolvedModel{Client: mockClient, Name: "test"}}
	cache.mu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client, name, err := cache.Get(context.Background(), "1")
			assert.NoError(t, err)
			assert.NotNil(t, client)
			assert.Equal(t, "test", name)
		}()
	}

	// Also invalidate concurrently.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache.Invalidate("999") // non-existent, but should not panic
		}()
	}

	wg.Wait()
}

func TestCreateClientFromDBModel_UnsupportedType(t *testing.T) {
	m := models.LLMProviderModel{
		Type:      "grpc_custom",
		BaseURL:   "http://localhost",
		ModelName: "test",
	}
	_, err := CreateClientFromDBModel(m, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported provider type: grpc_custom")
}
