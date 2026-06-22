package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
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

func newTestUploadService(t *testing.T, persistRaw bool) (*UploadService, *mockDocumentRepository, string) {
	t.Helper()
	repo := newMockDocumentRepository()
	dataDir := t.TempDir()
	svc := NewUploadService(repo, dataDir, persistRaw)
	svc.SetKBEmbeddingResolver(stubKBEmbeddingResolver{})
	return svc, repo, dataDir
}

// countFiles returns the number of regular files under root.
func countFiles(t *testing.T, root string) int {
	t.Helper()
	count := 0
	err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			count++
		}
		return nil
	})
	require.NoError(t, err)
	return count
}

func TestUploadFileToKB_StatelessWritesNoFile(t *testing.T) {
	svc, repo, dataDir := newTestUploadService(t, false)

	resp, err := svc.UploadFileToKB(
		context.Background(),
		"00000000-0000-0000-0000-000000000001",
		"kb-1",
		"embed-model-1",
		"notes.txt",
		"txt",
		int64(len("hello world")),
		"hash-1",
		[]byte("hello world"),
	)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// No raw file persisted anywhere under the temp data dir.
	assert.Equal(t, 0, countFiles(t, dataDir), "stateless upload must not write any raw file")

	// The synchronously-saved document has an empty FilePath and is queued for indexing.
	saved := repo.firstSavedDoc()
	require.NotNil(t, saved)
	assert.Equal(t, "", saved.FilePath)
	assert.Equal(t, "indexing", saved.Status)
}

func TestUploadFileToKB_LocalWritesFile(t *testing.T) {
	svc, repo, dataDir := newTestUploadService(t, true)

	tenantID := "00000000-0000-0000-0000-000000000001"
	kbID := "kb-1"
	resp, err := svc.UploadFileToKB(
		context.Background(),
		tenantID,
		kbID,
		"embed-model-1",
		"notes.txt",
		"txt",
		int64(len("hello world")),
		"hash-1",
		[]byte("hello world"),
	)
	require.NoError(t, err)
	require.NotNil(t, resp)

	saved := repo.firstSavedDoc()
	require.NotNil(t, saved)
	assert.NotEmpty(t, saved.FilePath, "local storage must record a non-empty FilePath")

	// File lives under <dataDir>/knowledge/<tenant>/<kb>/.
	expectedDir := filepath.Join(dataDir, "knowledge", tenantID, kbID)
	assert.True(t, strings.HasPrefix(saved.FilePath, expectedDir),
		"file %q must be under %q", saved.FilePath, expectedDir)
	_, statErr := os.Stat(saved.FilePath)
	require.NoError(t, statErr, "the raw file must exist on disk")
	assert.GreaterOrEqual(t, countFiles(t, dataDir), 1)
}

func TestReindexFileByKB_StatelessReturnsReuploadError(t *testing.T) {
	svc, repo, _ := newTestUploadService(t, false)

	tenantID := "00000000-0000-0000-0000-000000000001"
	kbID := "kb-1"
	ctx := context.Background()

	// Seed a stored doc with empty FilePath (as a stateless upload would).
	require.NoError(t, repo.SaveDocument(ctx, &models.KnowledgeDocument{
		ID:              "doc-1",
		KnowledgeBaseID: kbID,
		TenantID:        tenantID,
		FilePath:        "",
		Status:          "ready",
	}))

	err := svc.ReindexFileByKB(ctx, kbID, "embed-model-1", "doc-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "re-upload")

	// Must map to HTTP 400 (InvalidInput), not 500 — re-index-without-raw is a
	// client-actionable condition (re-upload), not a server error (SCC-03).
	var de *pkgerrors.DomainError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, pkgerrors.CodeInvalidInput, de.Code)

	// Status must NOT have been flipped to "error".
	doc, getErr := repo.GetDocumentByID(ctx, "doc-1")
	require.NoError(t, getErr)
	require.NotNil(t, doc)
	assert.Equal(t, "ready", doc.Status)
}
