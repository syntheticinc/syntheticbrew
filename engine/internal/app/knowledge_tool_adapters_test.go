package app

import (
	"context"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// embeddingModelsTestDB returns an in-memory sqlite DB with just the columns
// ResolveSingleEmbeddingModel reads from the "models" table.
func embeddingModelsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	err = db.Exec(`CREATE TABLE models (
		id TEXT PRIMARY KEY,
		kind VARCHAR(20) NOT NULL DEFAULT 'chat',
		tenant_id TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'
	)`).Error
	require.NoError(t, err)
	return db
}

// seedEmbeddingModel inserts an embedding-kind model for the CE tenant (the
// tenant the adapter derives from an empty context).
func seedEmbeddingModel(t *testing.T, db *gorm.DB, id string) {
	t.Helper()
	err := db.Exec(`INSERT INTO models (id, kind, tenant_id)
		VALUES (?, 'embedding', '00000000-0000-0000-0000-000000000001')`, id).Error
	require.NoError(t, err)
}

func TestResolveSingleEmbeddingModel_ZeroModels(t *testing.T) {
	a := &knowledgeToolAdapter{db: embeddingModelsTestDB(t)}

	_, err := a.ResolveSingleEmbeddingModel(context.Background())
	if err == nil {
		t.Fatal("expected an error when no embedding model is configured")
	}
	if !strings.Contains(err.Error(), "no embedding model configured") {
		t.Fatalf("expected no-model error, got: %v", err)
	}
}

func TestResolveSingleEmbeddingModel_ExactlyOne(t *testing.T) {
	db := embeddingModelsTestDB(t)
	seedEmbeddingModel(t, db, "emb-only")
	a := &knowledgeToolAdapter{db: db}

	got, err := a.ResolveSingleEmbeddingModel(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "emb-only" {
		t.Fatalf("resolved id = %q, want emb-only", got)
	}
}

func TestResolveSingleEmbeddingModel_MoreThanOne(t *testing.T) {
	db := embeddingModelsTestDB(t)
	seedEmbeddingModel(t, db, "emb-1")
	seedEmbeddingModel(t, db, "emb-2")
	a := &knowledgeToolAdapter{db: db}

	_, err := a.ResolveSingleEmbeddingModel(context.Background())
	if err == nil {
		t.Fatal("expected an error when multiple embedding models are configured")
	}
	if !strings.Contains(err.Error(), "specify the embedding model explicitly") {
		t.Fatalf("expected ambiguity error, got: %v", err)
	}
}
