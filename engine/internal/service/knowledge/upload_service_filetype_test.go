package knowledge

import (
	"context"
	"strings"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// fakeFileTypeDocRepo is the minimal repository indexFileAsyncKB touches: it
// records saved chunks and serves the document back for the status update.
type fakeFileTypeDocRepo struct {
	doc    *models.KnowledgeDocument
	chunks []models.KnowledgeChunk
}

func (f *fakeFileTypeDocRepo) SaveDocument(_ context.Context, doc *models.KnowledgeDocument) error {
	f.doc = doc
	return nil
}
func (f *fakeFileTypeDocRepo) SaveChunks(_ context.Context, chunks []models.KnowledgeChunk) error {
	f.chunks = append(f.chunks, chunks...)
	return nil
}
func (f *fakeFileTypeDocRepo) DeleteChunksByDocument(context.Context, string) error { return nil }
func (f *fakeFileTypeDocRepo) DeleteDocument(context.Context, string) error         { return nil }
func (f *fakeFileTypeDocRepo) GetDocumentByID(context.Context, string) (*models.KnowledgeDocument, error) {
	return f.doc, nil
}
func (f *fakeFileTypeDocRepo) ListDocumentsByKB(context.Context, string) ([]models.KnowledgeDocument, error) {
	return nil, nil
}
func (f *fakeFileTypeDocRepo) DeleteDocumentsByKB(context.Context, string) error { return nil }
func (f *fakeFileTypeDocRepo) DeleteChunksByKB(context.Context, string) error    { return nil }

type fakeFileTypeEmbedResolver struct{}

func (fakeFileTypeEmbedResolver) ResolveByModelID(context.Context, string) (*EmbeddingModelInfo, error) {
	return &EmbeddingModelInfo{ModelName: "m", EmbeddingDim: 2}, nil
}

// recordingEmbedder captures the texts it was asked to embed so the test can
// prove the content passed through the text extractor verbatim.
type recordingEmbedder struct{ gotTexts []string }

func (e *recordingEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	e.gotTexts = texts
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{0.1, 0.2}
	}
	return out, nil
}

type fixedEmbedderFactory struct{ emb EmbeddingProvider }

func (f fixedEmbedderFactory) EmbedderFor(context.Context, *EmbeddingModelInfo) (EmbeddingProvider, bool) {
	return f.emb, true
}

// TestIndexFileAsyncKB_ExtractsByValidatedTypeNotFileName pins the file_type
// security fix: indexing keys off the caller-validated fileType, never the
// fileName extension. A document named "attack.pdf" but validated as "txt" must
// be extracted as text (pass-through), so its raw bytes never reach the PDF
// binary parser. If the code re-derived the type from ".pdf", ExtractText would
// try to parse plain text as a PDF and fail — the document would land in
// "error" with a "text extraction failed" message instead of "ready".
func TestIndexFileAsyncKB_ExtractsByValidatedTypeNotFileName(t *testing.T) {
	const (
		docID    = "doc-1"
		tenant   = "00000000-0000-0000-0000-000000000001"
		kbID     = "kb-1"
		embID    = "emb-1"
		fileName = "attack.pdf" // extension DISAGREES with the validated type
		fileType = "txt"        // the type the upload path admitted
		content  = "this is plain text content that is not a valid PDF"
	)

	repo := &fakeFileTypeDocRepo{doc: &models.KnowledgeDocument{ID: docID, KnowledgeBaseID: kbID, TenantID: tenant}}
	emb := &recordingEmbedder{}
	svc := NewUploadService(repo)
	svc.SetKBEmbeddingResolver(fakeFileTypeEmbedResolver{})
	svc.SetEmbedderFactory(fixedEmbedderFactory{emb: emb})

	// Call the async worker synchronously (no goroutine) to observe its result.
	svc.indexFileAsyncKB(docID, tenant, kbID, embID, fileName, fileType, content)

	if repo.doc.Status != "ready" {
		t.Fatalf("status = %q (msg %q), want ready — a .pdf name must not route txt content to the PDF parser",
			repo.doc.Status, repo.doc.StatusMsg)
	}
	if strings.Contains(repo.doc.StatusMsg, "text extraction failed") {
		t.Fatalf("extraction must key off the validated txt type, not the .pdf name; got: %s", repo.doc.StatusMsg)
	}
	if len(emb.gotTexts) == 0 {
		t.Fatal("expected the extracted text to be embedded")
	}
	joined := strings.Join(emb.gotTexts, " ")
	if !strings.Contains(joined, "plain text content") {
		t.Fatalf("embedded text %q must be the verbatim txt pass-through, not PDF-parsed bytes", joined)
	}
	if len(repo.chunks) == 0 {
		t.Fatal("expected chunks to be persisted for the ready document")
	}
}
