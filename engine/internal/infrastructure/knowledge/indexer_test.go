package knowledge

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// mockEmbeddingProvider returns fixed embeddings.
type mockEmbeddingProvider struct {
	embeddings [][]float32
	err        error
	callCount  int
}

func (m *mockEmbeddingProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	m.callCount++
	if m.err != nil {
		return nil, m.err
	}
	if m.embeddings != nil {
		return m.embeddings, nil
	}
	// Generate dummy embeddings
	result := make([][]float32, len(texts))
	for i := range texts {
		result[i] = []float32{0.1, 0.2, 0.3}
	}
	return result, nil
}

// mockKnowledgeRepository stores documents and chunks in memory.
type mockKnowledgeRepository struct {
	documents     map[string]*models.KnowledgeDocument // key: kbID+filePath
	chunks        map[string][]models.KnowledgeChunk   // key: documentID
	savedDocs     []*models.KnowledgeDocument
	savedChunks   []models.KnowledgeChunk
	deletedChunks []string // documentIDs
}

func newMockRepo() *mockKnowledgeRepository {
	return &mockKnowledgeRepository{
		documents: make(map[string]*models.KnowledgeDocument),
		chunks:    make(map[string][]models.KnowledgeChunk),
	}
}

func (m *mockKnowledgeRepository) GetDocumentByPath(_ context.Context, kbID, filePath string) (*models.KnowledgeDocument, error) {
	key := kbID + ":" + filePath
	return m.documents[key], nil
}

func (m *mockKnowledgeRepository) SaveDocument(_ context.Context, doc *models.KnowledgeDocument) error {
	key := doc.KnowledgeBaseID + ":" + doc.FilePath
	m.documents[key] = doc
	m.savedDocs = append(m.savedDocs, doc)
	return nil
}

func (m *mockKnowledgeRepository) SaveChunks(_ context.Context, chunks []models.KnowledgeChunk) error {
	for _, c := range chunks {
		m.chunks[c.DocumentID] = append(m.chunks[c.DocumentID], c)
	}
	m.savedChunks = append(m.savedChunks, chunks...)
	return nil
}

func (m *mockKnowledgeRepository) DeleteChunksByDocument(_ context.Context, documentID string) error {
	delete(m.chunks, documentID)
	m.deletedChunks = append(m.deletedChunks, documentID)
	return nil
}

func (m *mockKnowledgeRepository) ListDocumentsByKB(_ context.Context, kbID string) ([]models.KnowledgeDocument, error) {
	var result []models.KnowledgeDocument
	for _, doc := range m.documents {
		if doc.KnowledgeBaseID == kbID {
			result = append(result, *doc)
		}
	}
	return result, nil
}

func TestIndexer_IndexFolder_NewFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# Hello\n\nThis is a test."), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("Some plain text notes."), 0644))

	repo := newMockRepo()
	embedder := &mockEmbeddingProvider{}
	indexer := NewIndexer(embedder, repo, slog.Default())

	err := indexer.IndexFolder(context.Background(), "kb-test-1", dir)
	require.NoError(t, err)

	assert.Equal(t, 2, len(repo.savedDocs), "should index 2 documents")
	assert.True(t, len(repo.savedChunks) > 0, "should create chunks")
	assert.True(t, embedder.callCount > 0, "should call embedder")
}

func TestIndexer_IndexFolder_SkipsUnchanged(t *testing.T) {
	dir := t.TempDir()
	content := []byte("# Test\n\nContent here.")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "doc.md"), content, 0644))

	repo := newMockRepo()
	embedder := &mockEmbeddingProvider{}
	indexer := NewIndexer(embedder, repo, slog.Default())

	// First index
	err := indexer.IndexFolder(context.Background(), "kb-1", dir)
	require.NoError(t, err)
	assert.Equal(t, 1, embedder.callCount)

	// Second index — same content, should skip
	err = indexer.IndexFolder(context.Background(), "kb-1", dir)
	require.NoError(t, err)
	assert.Equal(t, 1, embedder.callCount, "should not re-embed unchanged file")
}

func TestIndexer_IndexFolder_ReindexesChanged(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "doc.md")
	require.NoError(t, os.WriteFile(filePath, []byte("# Version 1"), 0644))

	repo := newMockRepo()
	embedder := &mockEmbeddingProvider{}
	indexer := NewIndexer(embedder, repo, slog.Default())

	// First index
	err := indexer.IndexFolder(context.Background(), "kb-1", dir)
	require.NoError(t, err)
	assert.Equal(t, 1, embedder.callCount)

	// Change file content
	require.NoError(t, os.WriteFile(filePath, []byte("# Version 2 with more content"), 0644))

	// Second index — content changed, should re-embed
	err = indexer.IndexFolder(context.Background(), "kb-1", dir)
	require.NoError(t, err)
	assert.Equal(t, 2, embedder.callCount, "should re-embed changed file")
	assert.True(t, len(repo.deletedChunks) > 0, "should delete old chunks")
}

func TestIndexer_IndexFolder_IgnoresUnsupportedFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "code.go"), []byte("package main"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "image.png"), []byte{0x89, 0x50}, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# Hello"), 0644))

	repo := newMockRepo()
	embedder := &mockEmbeddingProvider{}
	indexer := NewIndexer(embedder, repo, slog.Default())

	err := indexer.IndexFolder(context.Background(), "kb-1", dir)
	require.NoError(t, err)

	assert.Equal(t, 1, len(repo.savedDocs), "should only index .md file")
}

func TestIndexer_IndexFolder_SubdirectoryFiles(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "sub")
	require.NoError(t, os.MkdirAll(subdir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "root.md"), []byte("# Root"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(subdir, "nested.md"), []byte("# Nested"), 0644))

	repo := newMockRepo()
	embedder := &mockEmbeddingProvider{}
	indexer := NewIndexer(embedder, repo, slog.Default())

	err := indexer.IndexFolder(context.Background(), "kb-1", dir)
	require.NoError(t, err)

	assert.Equal(t, 2, len(repo.savedDocs), "should index files in subdirectories")
}

func TestIndexer_IndexFolder_EmptyKBID(t *testing.T) {
	indexer := NewIndexer(&mockEmbeddingProvider{}, newMockRepo(), slog.Default())
	err := indexer.IndexFolder(context.Background(), "", "/tmp")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "knowledge base ID")
}

func TestIndexer_IndexFolder_InvalidPath(t *testing.T) {
	indexer := NewIndexer(&mockEmbeddingProvider{}, newMockRepo(), slog.Default())
	err := indexer.IndexFolder(context.Background(), "kb-1", "/nonexistent/path/abc123")
	assert.Error(t, err)
}

func TestIndexer_IndexFolder_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()

	repo := newMockRepo()
	indexer := NewIndexer(&mockEmbeddingProvider{}, repo, slog.Default())

	err := indexer.IndexFolder(context.Background(), "kb-1", dir)
	require.NoError(t, err)
	assert.Equal(t, 0, len(repo.savedDocs))
}
