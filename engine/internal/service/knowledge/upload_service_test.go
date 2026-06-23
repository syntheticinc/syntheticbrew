package knowledge

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// mockDocumentRepository is an in-memory DocumentRepository that tolerates the
// async indexing goroutine's status-update calls (GetDocumentByID +
// SaveDocument) running concurrently with the synchronous assertions.
type mockDocumentRepository struct {
	mu sync.Mutex

	docs map[string]*models.KnowledgeDocument

	// firstSaved captures a copy of the document at its first SaveDocument call
	// (the synchronous upload write), insulating assertions from later async
	// status flips.
	firstSaved   *models.KnowledgeDocument
	firstSaveSet bool
}

func newMockDocumentRepository() *mockDocumentRepository {
	return &mockDocumentRepository{docs: make(map[string]*models.KnowledgeDocument)}
}

func (m *mockDocumentRepository) SaveDocument(_ context.Context, doc *models.KnowledgeDocument) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.firstSaveSet {
		cp := *doc
		m.firstSaved = &cp
		m.firstSaveSet = true
	}
	stored := *doc
	m.docs[doc.ID] = &stored
	return nil
}

func (m *mockDocumentRepository) firstSavedDoc() *models.KnowledgeDocument {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.firstSaved
}

func (m *mockDocumentRepository) SaveChunks(_ context.Context, _ []models.KnowledgeChunk) error {
	return nil
}

func (m *mockDocumentRepository) DeleteChunksByDocument(_ context.Context, _ string) error {
	return nil
}

func (m *mockDocumentRepository) DeleteDocument(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.docs, id)
	return nil
}

func (m *mockDocumentRepository) GetDocumentByID(_ context.Context, id string) (*models.KnowledgeDocument, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.docs[id]
	if !ok {
		return nil, nil
	}
	cp := *doc
	return &cp, nil
}

func (m *mockDocumentRepository) ListDocumentsByKB(_ context.Context, _ string) ([]models.KnowledgeDocument, error) {
	return nil, nil
}

func (m *mockDocumentRepository) DeleteDocumentsByKB(_ context.Context, _ string) error {
	return nil
}

func (m *mockDocumentRepository) DeleteChunksByKB(_ context.Context, _ string) error {
	return nil
}

// stubKBEmbeddingResolver returns a valid EmbeddingModelInfo so UploadFileToKB
// passes its embedding-model guard. The BaseURL is unreachable on purpose — the
// async indexing goroutine's real HTTP call will fail and update status, which
// the mock repo tolerates; the synchronous assertions don't depend on it.
type stubKBEmbeddingResolver struct{}

func (stubKBEmbeddingResolver) ResolveByModelID(_ context.Context, _ string) (*EmbeddingModelInfo, error) {
	return &EmbeddingModelInfo{
		BaseURL:      "http://127.0.0.1:0",
		APIKey:       "test",
		ModelName:    "test-embed",
		EmbeddingDim: 8,
	}, nil
}

func newTestUploadService(t *testing.T) (*UploadService, *mockDocumentRepository) {
	t.Helper()
	repo := newMockDocumentRepository()
	svc := NewUploadService(repo)
	svc.SetKBEmbeddingResolver(stubKBEmbeddingResolver{})
	return svc, repo
}

func uploadToKB(t *testing.T, svc *UploadService, fileName string) {
	t.Helper()
	content := []byte("hello world")
	_, err := svc.UploadFileToKB(
		context.Background(),
		"00000000-0000-0000-0000-000000000001",
		"kb-1",
		"embed-model-1",
		fileName,
		"txt",
		int64(len(content)),
		"hash-1",
		content,
	)
	require.NoError(t, err)
}

// TestUploadFileToKB_StatelessStoresName is the regression guard for the
// filename-display bug: in the stateless model no raw file is persisted
// (FilePath==""), so the displayed name MUST come from the stored file_name
// metadata, not filepath.Base(FilePath) (which returns "." for an empty path).
func TestUploadFileToKB_StatelessStoresName(t *testing.T) {
	svc, repo := newTestUploadService(t)

	uploadToKB(t, svc, "notes.txt")

	saved := repo.firstSavedDoc()
	require.NotNil(t, saved)
	// No raw file path is recorded — storage is stateless.
	assert.Equal(t, "", saved.FilePath)
	// The original name is kept as metadata and is what the API/UI displays.
	assert.Equal(t, "notes.txt", saved.OriginalName)
	assert.Equal(t, "notes.txt", saved.FileName(), `displayed name must be the original file name, not "."`)
	assert.Equal(t, "indexing", saved.Status, "upload queues the document for automatic indexing")
}

// TestUploadFileToKB_UnicodeName verifies non-ASCII file names round-trip intact.
func TestUploadFileToKB_UnicodeName(t *testing.T) {
	svc, repo := newTestUploadService(t)

	const name = "résumé—naïve_Ω.txt"
	uploadToKB(t, svc, name)

	saved := repo.firstSavedDoc()
	require.NotNil(t, saved)
	assert.Equal(t, name, saved.FileName())
}

// TestUploadFileToKB_OverlongNameClamped verifies a file name longer than the
// file_name column width (varchar(255)) is truncated to fit rather than left to
// blow up the DB insert with a 500 (SCC-03: invalid input must not 500).
func TestUploadFileToKB_OverlongNameClamped(t *testing.T) {
	svc, repo := newTestUploadService(t)

	longName := strings.Repeat("a", 300) + ".txt"
	uploadToKB(t, svc, longName)

	saved := repo.firstSavedDoc()
	require.NotNil(t, saved)
	assert.LessOrEqual(t, len([]rune(saved.OriginalName)), 255,
		"stored name must fit varchar(255)")
	assert.NotEmpty(t, saved.FileName())
}

// TestKnowledgeDocumentFileName_PrefersStored verifies FileName() returns the
// stored original name when present.
func TestKnowledgeDocumentFileName_PrefersStored(t *testing.T) {
	doc := &models.KnowledgeDocument{OriginalName: "report.pdf", FilePath: ""}
	assert.Equal(t, "report.pdf", doc.FileName())
}

// TestKnowledgeDocumentFileName_LegacyFallback verifies FileName() falls back to
// the FilePath basename for legacy rows that predate the file_name column.
func TestKnowledgeDocumentFileName_LegacyFallback(t *testing.T) {
	doc := &models.KnowledgeDocument{
		OriginalName: "",
		FilePath:     "/data/knowledge/tenant/kb/8f3a_report.pdf",
	}
	assert.Equal(t, "8f3a_report.pdf", doc.FileName())
}
