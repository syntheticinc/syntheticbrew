package knowledge

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/indexing"
	infknowledge "github.com/syntheticinc/syntheticbrew/internal/infrastructure/knowledge"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// tenantFromCtx extracts tenant_id from context, falling back to CETenantID for CE mode.
func tenantFromCtx(ctx context.Context) string {
	tid := domain.TenantIDFromContext(ctx)
	if tid == "" {
		return domain.CETenantID
	}
	return tid
}

// DocumentRepository persists knowledge documents and chunks.
type DocumentRepository interface {
	SaveDocument(ctx context.Context, doc *models.KnowledgeDocument) error
	SaveChunks(ctx context.Context, chunks []models.KnowledgeChunk) error
	DeleteChunksByDocument(ctx context.Context, documentID string) error
	DeleteDocument(ctx context.Context, id string) error
	GetDocumentByID(ctx context.Context, id string) (*models.KnowledgeDocument, error)
	ListDocumentsByKB(ctx context.Context, kbID string) ([]models.KnowledgeDocument, error)
	DeleteDocumentsByKB(ctx context.Context, kbID string) error
	DeleteChunksByKB(ctx context.Context, kbID string) error
}

// EmbeddingProvider generates vector embeddings for text.
type EmbeddingProvider interface {
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// EmbeddingModelInfo holds embedding model details resolved from DB.
type EmbeddingModelInfo struct {
	BaseURL      string
	APIKey       string
	ModelName    string
	EmbeddingDim int
}

// EmbeddingModelResolver resolves the embedding model for an agent's knowledge capability.
type EmbeddingModelResolver interface {
	ResolveEmbeddingModel(ctx context.Context, agentName string) (*EmbeddingModelInfo, error)
}

// KBEmbeddingResolver resolves the embedding model from a KB's embedding_model_id.
type KBEmbeddingResolver interface {
	ResolveByModelID(ctx context.Context, modelID string) (*EmbeddingModelInfo, error)
}

// FileResponse is the API response for a knowledge file.
type FileResponse struct {
	ID              string `json:"id"`
	KnowledgeBaseID string `json:"knowledge_base_id,omitempty"`
	FileName        string `json:"file_name"`
	FileType        string `json:"file_type"`
	FileSize        int64  `json:"file_size"`
	Status          string `json:"status"`
	StatusMsg       string `json:"status_message,omitempty"`
	ChunkCount      int    `json:"chunk_count"`
	CreatedAt       string `json:"created_at"`
	IndexedAt       string `json:"indexed_at,omitempty"`
}

// UploadService handles file uploads, storage, and async indexing.
type UploadService struct {
	repo              DocumentRepository
	embeddingResolver EmbeddingModelResolver // legacy: resolves from agent capability config
	kbEmbedResolver   KBEmbeddingResolver    // resolves from KB's embedding_model_id
	dataDir           string
}

// NewUploadService creates a new knowledge upload service.
func NewUploadService(repo DocumentRepository, dataDir string) *UploadService {
	return &UploadService{
		repo:    repo,
		dataDir: dataDir,
	}
}

// SetEmbeddingResolver sets the resolver for capability-based embedding models (legacy).
func (s *UploadService) SetEmbeddingResolver(resolver EmbeddingModelResolver) {
	s.embeddingResolver = resolver
}

// SetKBEmbeddingResolver sets the resolver for KB-based embedding models.
func (s *UploadService) SetKBEmbeddingResolver(resolver KBEmbeddingResolver) {
	s.kbEmbedResolver = resolver
}

// UploadFileToKB stores a file on disk, creates a DB record, and triggers async indexing.
// Files are scoped to a KnowledgeBase, not an agent.
func (s *UploadService) UploadFileToKB(ctx context.Context, tenantID, kbID, embeddingModelID, fileName, fileType string, fileSize int64, fileHash string, content []byte) (*FileResponse, error) {
	// Guard: verify embedding model is available.
	if embeddingModelID == "" {
		return nil, fmt.Errorf("cannot upload: no embedding model configured for this knowledge base")
	}
	embedder, err := s.resolveKBEmbeddingProvider(ctx, embeddingModelID)
	if err != nil {
		return nil, fmt.Errorf("cannot upload: %w", err)
	}
	_ = embedder // validated; will re-resolve in async

	// Create storage directory: data/knowledge/{tenant_id}/{kb_id}/
	dir := filepath.Join(s.dataDir, "knowledge", tenantID, kbID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create knowledge directory: %w", err)
	}

	docID := uuid.New().String()
	storedName := docID + "_" + fileName
	filePath := filepath.Join(dir, storedName)

	if err := os.WriteFile(filePath, content, 0o644); err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}

	doc := &models.KnowledgeDocument{
		ID:              docID,
		KnowledgeBaseID: kbID,
		TenantID:        tenantID,
		FilePath:        filePath,
		FileType:        fileType,
		FileSize:        fileSize,
		FileHash:        fileHash,
		Status:          "indexing",
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	if err := s.repo.SaveDocument(ctx, doc); err != nil {
		_ = os.Remove(filePath)
		return nil, fmt.Errorf("save document record: %w", err)
	}

	go s.indexFileAsyncKB(docID, tenantID, kbID, embeddingModelID, fileName, string(content))

	return &FileResponse{
		ID:              docID,
		KnowledgeBaseID: kbID,
		FileName:        fileName,
		FileType:        fileType,
		FileSize:        fileSize,
		Status:          "indexing",
		CreatedAt:       doc.CreatedAt.Format(time.RFC3339),
	}, nil
}

// UploadFile stores a file on disk (legacy agent-scoped path, creates KB-scoped record).
func (s *UploadService) UploadFile(ctx context.Context, tenantID, agentName, fileName, fileType string, fileSize int64, fileHash string, content []byte) (*FileResponse, error) {
	if _, err := s.resolveEmbeddingProvider(ctx, agentName); err != nil {
		return nil, fmt.Errorf("cannot upload: %w", err)
	}

	dir := filepath.Join(s.dataDir, "knowledge", tenantID, agentName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create knowledge directory: %w", err)
	}

	docID := uuid.New().String()
	storedName := docID + "_" + fileName
	filePath := filepath.Join(dir, storedName)

	if err := os.WriteFile(filePath, content, 0o644); err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}

	doc := &models.KnowledgeDocument{
		ID:        docID,
		TenantID:  tenantID,
		FilePath:  filePath,
		FileType:  fileType,
		FileSize:  fileSize,
		FileHash:  fileHash,
		Status:    "indexing",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := s.repo.SaveDocument(ctx, doc); err != nil {
		_ = os.Remove(filePath)
		return nil, fmt.Errorf("save document record: %w", err)
	}

	go s.indexFileAsync(docID, tenantID, agentName, fileName, string(content))

	return &FileResponse{
		ID:        docID,
		FileName:  fileName,
		FileType:  fileType,
		FileSize:  fileSize,
		Status:    "indexing",
		CreatedAt: doc.CreatedAt.Format(time.RFC3339),
	}, nil
}

// resolveKBEmbeddingProvider resolves embedding provider from KB's model ID.
func (s *UploadService) resolveKBEmbeddingProvider(ctx context.Context, embeddingModelID string) (EmbeddingProvider, error) {
	if s.kbEmbedResolver != nil {
		info, err := s.kbEmbedResolver.ResolveByModelID(ctx, embeddingModelID)
		if err == nil && info != nil {
			slog.InfoContext(ctx, "[KnowledgeUpload] using KB embedding model",
				"model_id", embeddingModelID, "model", info.ModelName, "dim", info.EmbeddingDim)
			return indexing.NewOpenAIEmbeddingsClient(info.BaseURL, info.APIKey, info.ModelName, info.EmbeddingDim), nil
		}
		if err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("embedding model %q not found: add an embedding model in Settings > Models", embeddingModelID)
}

// resolveEmbeddingProvider picks the embedding provider for this agent from capability config (legacy).
func (s *UploadService) resolveEmbeddingProvider(ctx context.Context, agentName string) (EmbeddingProvider, error) {
	if s.embeddingResolver != nil {
		info, err := s.embeddingResolver.ResolveEmbeddingModel(ctx, agentName)
		if err == nil && info != nil {
			slog.InfoContext(ctx, "[KnowledgeUpload] using configured embedding model",
				"agent", agentName, "model", info.ModelName, "dim", info.EmbeddingDim)
			return indexing.NewOpenAIEmbeddingsClient(info.BaseURL, info.APIKey, info.ModelName, info.EmbeddingDim), nil
		}
	}
	return nil, fmt.Errorf("no embedding model configured for agent %q: add an embedding model in Settings > Models and select it in the Knowledge capability config", agentName)
}

// indexFileAsyncKB chunks, embeds, and stores vector data for KB-scoped file.
func (s *UploadService) indexFileAsyncKB(docID, tenantID, kbID, embeddingModelID, fileName, content string) {
	// Tenant must be on ctx — repo applies tenantScope, status updates would silently no-op otherwise.
	ctx := domain.WithTenantID(context.Background(), tenantID)

	fileType := strings.TrimPrefix(strings.ToLower(filepath.Ext(fileName)), ".")
	text, extractErr := infknowledge.ExtractText([]byte(content), fileType)
	if extractErr != nil {
		slog.ErrorContext(ctx, "[KnowledgeUpload] text extraction failed",
			"doc_id", docID, "file", fileName, "error", extractErr)
		s.updateDocStatus(ctx, docID, "error", fmt.Sprintf("text extraction failed: %v", extractErr), 0)
		return
	}

	chunker := infknowledge.ChunkerForFile(fileName)
	chunks := chunker.Chunk(text)

	if len(chunks) == 0 {
		s.updateDocStatus(ctx, docID, "ready", "", 0)
		return
	}

	embedder, err := s.resolveKBEmbeddingProvider(ctx, embeddingModelID)
	if err != nil {
		slog.ErrorContext(ctx, "[KnowledgeUpload] no embedding provider available",
			"doc_id", docID, "kb_id", kbID, "error", err)
		s.updateDocStatus(ctx, docID, "error", err.Error(), 0)
		return
	}

	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Content
	}

	embeddings, err := embedder.EmbedBatch(ctx, texts)
	if err != nil {
		slog.ErrorContext(ctx, "[KnowledgeUpload] embedding failed",
			"doc_id", docID, "kb_id", kbID, "error", err)
		s.updateDocStatus(ctx, docID, "error", fmt.Sprintf("embedding failed: %v", err), 0)
		return
	}

	chunkModels := make([]models.KnowledgeChunk, 0, len(chunks))
	for i, c := range chunks {
		if i >= len(embeddings) || embeddings[i] == nil {
			continue
		}
		chunkModels = append(chunkModels, models.KnowledgeChunk{
			ID:         uuid.New().String(),
			DocumentID: docID,
			TenantID:   tenantID,
			Content:    c.Content,
			ChunkOrder: c.Order,
			Embedding:  pgvector.NewVector(embeddings[i]),
		})
	}

	if len(chunkModels) == 0 && len(chunks) > 0 {
		slog.ErrorContext(ctx, "[KnowledgeUpload] no embeddings generated for any chunk",
			"doc_id", docID, "kb_id", kbID, "chunks_input", len(chunks))
		s.updateDocStatus(ctx, docID, "error",
			"no embeddings generated (embedding provider may be unavailable)", 0)
		return
	}

	if len(chunkModels) > 0 {
		if err := s.repo.SaveChunks(ctx, chunkModels); err != nil {
			slog.ErrorContext(ctx, "[KnowledgeUpload] save chunks failed",
				"doc_id", docID, "error", err)
			s.updateDocStatus(ctx, docID, "error", fmt.Sprintf("save chunks failed: %v", err), 0)
			return
		}
	}

	s.updateDocStatus(ctx, docID, "ready", "", len(chunkModels))
	slog.InfoContext(ctx, "[KnowledgeUpload] indexing complete",
		"doc_id", docID, "kb_id", kbID, "chunks", len(chunkModels))
}

// indexFileAsync chunks, embeds, and stores vector data (legacy agent-scoped).
func (s *UploadService) indexFileAsync(docID, tenantID, agentName, fileName, content string) {
	ctx := domain.WithTenantID(context.Background(), tenantID)

	fileType := strings.TrimPrefix(strings.ToLower(filepath.Ext(fileName)), ".")
	text, extractErr := infknowledge.ExtractText([]byte(content), fileType)
	if extractErr != nil {
		slog.ErrorContext(ctx, "[KnowledgeUpload] text extraction failed",
			"doc_id", docID, "file", fileName, "error", extractErr)
		s.updateDocStatus(ctx, docID, "error", fmt.Sprintf("text extraction failed: %v", extractErr), 0)
		return
	}

	chunker := infknowledge.ChunkerForFile(fileName)
	chunks := chunker.Chunk(text)

	if len(chunks) == 0 {
		s.updateDocStatus(ctx, docID, "ready", "", 0)
		return
	}

	embedder, err := s.resolveEmbeddingProvider(ctx, agentName)
	if err != nil {
		slog.ErrorContext(ctx, "[KnowledgeUpload] no embedding provider available",
			"doc_id", docID, "agent", agentName, "error", err)
		s.updateDocStatus(ctx, docID, "error", err.Error(), 0)
		return
	}

	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Content
	}

	embeddings, err := embedder.EmbedBatch(ctx, texts)
	if err != nil {
		slog.ErrorContext(ctx, "[KnowledgeUpload] embedding failed",
			"doc_id", docID, "agent", agentName, "error", err)
		s.updateDocStatus(ctx, docID, "error", fmt.Sprintf("embedding failed: %v", err), 0)
		return
	}

	chunkModels := make([]models.KnowledgeChunk, 0, len(chunks))
	for i, c := range chunks {
		if i >= len(embeddings) || embeddings[i] == nil {
			continue
		}
		chunkModels = append(chunkModels, models.KnowledgeChunk{
			ID:         uuid.New().String(),
			DocumentID: docID,
			TenantID:   tenantID,
			Content:    c.Content,
			ChunkOrder: c.Order,
			Embedding:  pgvector.NewVector(embeddings[i]),
		})
	}

	if len(chunkModels) == 0 && len(chunks) > 0 {
		slog.ErrorContext(ctx, "[KnowledgeUpload] no embeddings generated for any chunk",
			"doc_id", docID, "agent", agentName, "chunks_input", len(chunks))
		s.updateDocStatus(ctx, docID, "error",
			"no embeddings generated (embedding provider may be unavailable)", 0)
		return
	}

	if len(chunkModels) > 0 {
		if err := s.repo.SaveChunks(ctx, chunkModels); err != nil {
			slog.ErrorContext(ctx, "[KnowledgeUpload] save chunks failed",
				"doc_id", docID, "error", err)
			s.updateDocStatus(ctx, docID, "error", fmt.Sprintf("save chunks failed: %v", err), 0)
			return
		}
	}

	s.updateDocStatus(ctx, docID, "ready", "", len(chunkModels))
	slog.InfoContext(ctx, "[KnowledgeUpload] indexing complete",
		"doc_id", docID, "agent", agentName, "chunks", len(chunkModels))
}

// updateDocStatus updates a document's status, status message, and chunk count.
func (s *UploadService) updateDocStatus(ctx context.Context, docID, status, statusMsg string, chunkCount int) {
	doc, err := s.repo.GetDocumentByID(ctx, docID)
	if err != nil || doc == nil {
		slog.ErrorContext(ctx, "[KnowledgeUpload] failed to find doc for status update",
			"doc_id", docID, "error", err)
		return
	}
	doc.Status = status
	doc.StatusMsg = statusMsg
	doc.ChunkCount = chunkCount
	doc.UpdatedAt = time.Now()
	if status == "ready" {
		doc.IndexedAt = time.Now()
	}
	if err := s.repo.SaveDocument(ctx, doc); err != nil {
		slog.ErrorContext(ctx, "[KnowledgeUpload] failed to update doc status",
			"doc_id", docID, "error", err)
	}
}

// ListFilesByKB returns knowledge files for a knowledge base (tenant-scoped).
func (s *UploadService) ListFilesByKB(ctx context.Context, kbID string) ([]FileResponse, error) {
	docs, err := s.repo.ListDocumentsByKB(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}
	return docsToResponse(docs), nil
}

func docsToResponse(docs []models.KnowledgeDocument) []FileResponse {
	files := make([]FileResponse, 0, len(docs))
	for _, doc := range docs {
		f := FileResponse{
			ID:              doc.ID,
			KnowledgeBaseID: doc.KnowledgeBaseID,
			FileName:        doc.FileName(),
			FileType:        doc.FileType,
			FileSize:        doc.FileSize,
			Status:          doc.Status,
			StatusMsg:       doc.StatusMsg,
			ChunkCount:      doc.ChunkCount,
			CreatedAt:       doc.CreatedAt.Format(time.RFC3339),
		}
		if !doc.IndexedAt.IsZero() {
			f.IndexedAt = doc.IndexedAt.Format(time.RFC3339)
		}
		files = append(files, f)
	}
	return files
}

// GetFileByKB returns a single file belonging to a KB, or nil if not found.
func (s *UploadService) GetFileByKB(ctx context.Context, kbID, fileID string) (*FileResponse, error) {
	doc, err := s.repo.GetDocumentByID(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("get document: %w", err)
	}
	if doc == nil || doc.KnowledgeBaseID != kbID {
		return nil, nil
	}
	files := docsToResponse([]models.KnowledgeDocument{*doc})
	if len(files) == 0 {
		return nil, nil
	}
	return &files[0], nil
}

// DeleteFileByKB removes a file belonging to a KB.
func (s *UploadService) DeleteFileByKB(ctx context.Context, kbID, fileID string) error {
	doc, err := s.repo.GetDocumentByID(ctx, fileID)
	if err != nil {
		return fmt.Errorf("get document: %w", err)
	}
	if doc == nil || doc.KnowledgeBaseID != kbID {
		return fmt.Errorf("file not found")
	}
	tenantID := tenantFromCtx(ctx)
	if doc.TenantID != tenantID {
		return fmt.Errorf("file not found")
	}
	return s.deleteDocFull(ctx, doc)
}

func (s *UploadService) deleteDocFull(ctx context.Context, doc *models.KnowledgeDocument) error {
	if err := s.repo.DeleteChunksByDocument(ctx, doc.ID); err != nil {
		return fmt.Errorf("delete chunks: %w", err)
	}
	if doc.FilePath != "" {
		_ = os.Remove(doc.FilePath)
	}
	if err := s.repo.DeleteDocument(ctx, doc.ID); err != nil {
		return fmt.Errorf("delete document: %w", err)
	}
	return nil
}

// ReindexFileByKB re-indexes a file belonging to a KB.
func (s *UploadService) ReindexFileByKB(ctx context.Context, kbID, embeddingModelID, fileID string) error {
	doc, err := s.repo.GetDocumentByID(ctx, fileID)
	if err != nil || doc == nil || doc.KnowledgeBaseID != kbID {
		return fmt.Errorf("file not found")
	}
	tenantID := tenantFromCtx(ctx)
	if doc.TenantID != tenantID {
		return fmt.Errorf("file not found")
	}

	content, err := os.ReadFile(doc.FilePath)
	if err != nil {
		s.updateDocStatus(ctx, fileID, "error", fmt.Sprintf("read file failed: %v", err), 0)
		return fmt.Errorf("read file: %w", err)
	}

	if err := s.repo.DeleteChunksByDocument(ctx, fileID); err != nil {
		return fmt.Errorf("delete old chunks: %w", err)
	}

	s.updateDocStatus(ctx, fileID, "indexing", "", 0)
	go s.indexFileAsyncKB(fileID, doc.TenantID, kbID, embeddingModelID, doc.FileName(), string(content))
	return nil
}

// DeleteAllByKB removes all documents, chunks, and files for a knowledge base.
func (s *UploadService) DeleteAllByKB(ctx context.Context, kbID string) error {
	docs, err := s.repo.ListDocumentsByKB(ctx, kbID)
	if err != nil {
		return fmt.Errorf("list documents for cleanup: %w", err)
	}
	// Remove physical files
	for _, doc := range docs {
		if doc.FilePath != "" {
			_ = os.Remove(doc.FilePath)
		}
	}
	if err := s.repo.DeleteChunksByKB(ctx, kbID); err != nil {
		return fmt.Errorf("delete chunks: %w", err)
	}
	if err := s.repo.DeleteDocumentsByKB(ctx, kbID); err != nil {
		return fmt.Errorf("delete documents: %w", err)
	}
	return nil
}
