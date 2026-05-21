package persistence

import (
	"context"
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"gorm.io/gorm"
)

func setupMemoryDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE memories (
		id TEXT PRIMARY KEY,
		schema_id TEXT NOT NULL,
		user_sub TEXT NOT NULL,
		content TEXT NOT NULL,
		metadata TEXT,
		tenant_id TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
		created_at DATETIME,
		updated_at DATETIME
	)`).Error)
	return db
}

func TestMemoryStorage_StoreAndList(t *testing.T) {
	db := setupMemoryDB(t)
	storage := NewMemoryStorage(db)
	ctx := context.Background()

	mem, err := domain.NewMemory("1", "user-1", "remember: user prefers dark mode")
	require.NoError(t, err)

	require.NoError(t, storage.Store(ctx, mem, 0))
	assert.NotEmpty(t, mem.ID)

	// List by schema
	memories, err := storage.ListBySchema(ctx, "1")
	require.NoError(t, err)
	assert.Len(t, memories, 1)
	assert.Equal(t, "remember: user prefers dark mode", memories[0].Content)
	assert.Equal(t, "1", memories[0].SchemaID)
	assert.Equal(t, "user-1", memories[0].UserSub)
}

func TestMemoryStorage_SchemaIsolation(t *testing.T) {
	// AC-MEM-02: Memory in Schema A NOT visible to Schema B
	db := setupMemoryDB(t)
	storage := NewMemoryStorage(db)
	ctx := context.Background()

	memA, _ := domain.NewMemory("1", "user-1", "schema A memory")
	memB, _ := domain.NewMemory("2", "user-1", "schema B memory")
	require.NoError(t, storage.Store(ctx, memA, 0))
	require.NoError(t, storage.Store(ctx, memB, 0))

	// Schema 1 sees only its memory
	memoriesA, err := storage.ListBySchema(ctx, "1")
	require.NoError(t, err)
	assert.Len(t, memoriesA, 1)
	assert.Equal(t, "schema A memory", memoriesA[0].Content)

	// Schema 2 sees only its memory
	memoriesB, err := storage.ListBySchema(ctx, "2")
	require.NoError(t, err)
	assert.Len(t, memoriesB, 1)
	assert.Equal(t, "schema B memory", memoriesB[0].Content)
}

func TestMemoryStorage_CrossSession(t *testing.T) {
	// AC-MEM-01: Memory persists across sessions (same schema+user)
	db := setupMemoryDB(t)
	storage := NewMemoryStorage(db)
	ctx := context.Background()

	// "Session 1": store memory
	mem, _ := domain.NewMemory("1", "user-1", "user likes cats")
	require.NoError(t, storage.Store(ctx, mem, 0))

	// "Session 2": recall memory (same schema+user, different "session")
	memories, err := storage.ListBySchemaAndUser(ctx, "1", "user-1")
	require.NoError(t, err)
	assert.Len(t, memories, 1)
	assert.Equal(t, "user likes cats", memories[0].Content)
}

func TestMemoryStorage_FIFOEviction(t *testing.T) {
	// AC-MEM-RET-03: FIFO eviction when max_entries reached
	db := setupMemoryDB(t)
	storage := NewMemoryStorage(db)
	ctx := context.Background()

	maxEntries := 3

	// Store 3 entries
	for i := 1; i <= 3; i++ {
		mem, _ := domain.NewMemory("1", "user-1", fmt.Sprintf("memory %d", i))
		require.NoError(t, storage.Store(ctx, mem, maxEntries))
	}

	memories, err := storage.ListBySchemaAndUser(ctx, "1", "user-1")
	require.NoError(t, err)
	assert.Len(t, memories, 3)

	// Store 4th entry — should evict the oldest (memory 1)
	mem4, _ := domain.NewMemory("1", "user-1", "memory 4")
	require.NoError(t, storage.Store(ctx, mem4, maxEntries))

	memories, err = storage.ListBySchemaAndUser(ctx, "1", "user-1")
	require.NoError(t, err)
	assert.Len(t, memories, 3)

	// Verify oldest was evicted: "memory 1" gone, "memory 2,3,4" present
	contents := make([]string, len(memories))
	for i, m := range memories {
		contents[i] = m.Content
	}
	assert.NotContains(t, contents, "memory 1")
	assert.Contains(t, contents, "memory 2")
	assert.Contains(t, contents, "memory 3")
	assert.Contains(t, contents, "memory 4")
}

func TestMemoryStorage_UnlimitedRetention(t *testing.T) {
	// AC-MEM-RET-01: Default retention = Unlimited (maxEntries=0 means no eviction)
	db := setupMemoryDB(t)
	storage := NewMemoryStorage(db)
	ctx := context.Background()

	// Store 10 entries with maxEntries=0 (unlimited)
	for i := 1; i <= 10; i++ {
		mem, _ := domain.NewMemory("1", "user-1", fmt.Sprintf("memory %d", i))
		require.NoError(t, storage.Store(ctx, mem, 0))
	}

	memories, err := storage.ListBySchemaAndUser(ctx, "1", "user-1")
	require.NoError(t, err)
	assert.Len(t, memories, 10) // All 10 present, none evicted
}

func TestMemoryStorage_DeleteBySchema(t *testing.T) {
	// AC-MEM-03: User can clear memory
	db := setupMemoryDB(t)
	storage := NewMemoryStorage(db)
	ctx := context.Background()

	mem1, _ := domain.NewMemory("1", "user-1", "memory 1")
	mem2, _ := domain.NewMemory("1", "user-2", "memory 2")
	mem3, _ := domain.NewMemory("2", "user-1", "other schema")
	require.NoError(t, storage.Store(ctx, mem1, 0))
	require.NoError(t, storage.Store(ctx, mem2, 0))
	require.NoError(t, storage.Store(ctx, mem3, 0))

	deleted, err := storage.DeleteBySchema(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted)

	// Schema 1 empty
	memories, err := storage.ListBySchema(ctx, "1")
	require.NoError(t, err)
	assert.Len(t, memories, 0)

	// Schema 2 untouched
	memories, err = storage.ListBySchema(ctx, "2")
	require.NoError(t, err)
	assert.Len(t, memories, 1)
}

func TestMemoryStorage_DeleteByID(t *testing.T) {
	db := setupMemoryDB(t)
	storage := NewMemoryStorage(db)
	ctx := context.Background()

	mem, _ := domain.NewMemory("1", "user-1", "deletable")
	require.NoError(t, storage.Store(ctx, mem, 0))

	require.NoError(t, storage.DeleteByID(ctx, mem.ID))

	memories, err := storage.ListBySchema(ctx, "1")
	require.NoError(t, err)
	assert.Len(t, memories, 0)

	// Delete non-existent
	err = storage.DeleteByID(ctx, "999999")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "memory not found")
}

func TestMemoryStorage_Metadata(t *testing.T) {
	db := setupMemoryDB(t)
	storage := NewMemoryStorage(db)
	ctx := context.Background()

	mem, _ := domain.NewMemory("1", "user-1", "with metadata")
	mem.AddMetadata("source", "agent")
	mem.AddMetadata("tool", "memory_store")
	require.NoError(t, storage.Store(ctx, mem, 0))

	memories, err := storage.ListBySchema(ctx, "1")
	require.NoError(t, err)
	require.Len(t, memories, 1)

	val, ok := memories[0].GetMetadata("source")
	assert.True(t, ok)
	assert.Equal(t, "agent", val)

	val, ok = memories[0].GetMetadata("tool")
	assert.True(t, ok)
	assert.Equal(t, "memory_store", val)
}
