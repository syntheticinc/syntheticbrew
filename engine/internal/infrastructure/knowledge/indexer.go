package knowledge

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// EmbeddingProvider generates vector embeddings for text.
type EmbeddingProvider interface {
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// KnowledgeRepository persists knowledge documents and chunks.
type KnowledgeRepository interface {
	GetDocumentByPath(ctx context.Context, kbID, filePath string) (*models.KnowledgeDocument, error)
	SaveDocument(ctx context.Context, doc *models.KnowledgeDocument) error
	SaveChunks(ctx context.Context, chunks []models.KnowledgeChunk) error
	DeleteChunksByDocument(ctx context.Context, documentID string) error
	ListDocumentsByKB(ctx context.Context, kbID string) ([]models.KnowledgeDocument, error)
}

// Indexer scans folders, chunks documents, embeds them, and stores in the database.
type Indexer struct {
	embeddings EmbeddingProvider
	repo       KnowledgeRepository
	logger     *slog.Logger
}

// NewIndexer creates an Indexer with the given embedding provider and repository.
func NewIndexer(embeddings EmbeddingProvider, repo KnowledgeRepository, logger *slog.Logger) *Indexer {
	return &Indexer{
		embeddings: embeddings,
		repo:       repo,
		logger:     logger,
	}
}

// supportedExtensions lists file extensions eligible for indexing.
var supportedExtensions = map[string]bool{
	".md":  true,
	".txt": true,
}

// IndexFolder scans a folder, chunks documents, embeds, and saves to DB.
// Incremental: skips files with unchanged SHA256 hash.
// kbID is the knowledge base to scope documents to.
func (idx *Indexer) IndexFolder(ctx context.Context, kbID string, folderPath string) error {
	if kbID == "" {
		return fmt.Errorf("knowledge base ID is required")
	}
	if folderPath == "" {
		return fmt.Errorf("folder path is required")
	}

	info, err := os.Stat(folderPath)
	if err != nil {
		return fmt.Errorf("stat folder %s: %w", folderPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", folderPath)
	}

	// Collect all indexable files
	files, err := idx.collectFiles(folderPath)
	if err != nil {
		return fmt.Errorf("collect files: %w", err)
	}

	idx.logger.InfoContext(ctx, "starting knowledge indexing",
		"kb_id", kbID, "folder", folderPath, "files_found", len(files))

	var docsIndexed, chunksCreated int

	// Index each file
	for _, filePath := range files {
		indexed, chunks, err := idx.indexFile(ctx, kbID, folderPath, filePath)
		if err != nil {
			idx.logger.WarnContext(ctx, "failed to index file",
				"file", filePath, "error", err)
			continue
		}
		if indexed {
			docsIndexed++
			chunksCreated += chunks
		}
	}

	// Remove orphaned documents (files that no longer exist on disk)
	if err := idx.removeOrphans(ctx, kbID, files); err != nil {
		idx.logger.WarnContext(ctx, "failed to remove orphaned documents",
			"kb_id", kbID, "error", err)
	}

	idx.logger.InfoContext(ctx, "knowledge indexing complete",
		"kb_id", kbID, "docs_indexed", docsIndexed, "chunks_created", chunksCreated)

	return nil
}

// collectFiles walks the directory tree and returns paths to all supported files.
func (idx *Indexer) collectFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if supportedExtensions[ext] {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// indexFile indexes a single file. Returns (wasIndexed, chunkCount, error).
func (idx *Indexer) indexFile(ctx context.Context, kbID, folderRoot, filePath string) (bool, int, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return false, 0, fmt.Errorf("read file: %w", err)
	}

	hash := fmt.Sprintf("%x", sha256.Sum256(content))

	// Check if already indexed with same hash
	relPath, err := filepath.Rel(folderRoot, filePath)
	if err != nil {
		relPath = filePath
	}

	existing, err := idx.repo.GetDocumentByPath(ctx, kbID, relPath)
	if err != nil {
		return false, 0, fmt.Errorf("check existing document: %w", err)
	}
	if existing != nil && existing.FileHash == hash {
		return false, 0, nil // unchanged, skip
	}

	// Chunk the content
	chunker := ChunkerForFile(filePath)
	chunks := chunker.Chunk(string(content))
	if len(chunks) == 0 {
		return false, 0, nil
	}

	// Extract texts for embedding
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Content
	}

	// Embed all chunks
	embeddings, err := idx.embeddings.EmbedBatch(ctx, texts)
	if err != nil {
		return false, 0, fmt.Errorf("embed chunks: %w", err)
	}

	// Prepare document
	docID := uuid.New().String()
	if existing != nil {
		docID = existing.ID
		// Delete old chunks before saving new ones
		if err := idx.repo.DeleteChunksByDocument(ctx, docID); err != nil {
			return false, 0, fmt.Errorf("delete old chunks: %w", err)
		}
	}

	doc := &models.KnowledgeDocument{
		ID:              docID,
		KnowledgeBaseID: kbID,
		FilePath:        relPath,
		FileHash:        hash,
		ChunkCount:      len(chunks),
		IndexedAt:       time.Now(),
	}

	if err := idx.repo.SaveDocument(ctx, doc); err != nil {
		return false, 0, fmt.Errorf("save document: %w", err)
	}

	// Prepare and save chunks
	chunkModels := make([]models.KnowledgeChunk, 0, len(chunks))
	for i, c := range chunks {
		if i >= len(embeddings) || embeddings[i] == nil {
			continue
		}
		chunkModels = append(chunkModels, models.KnowledgeChunk{
			ID:         uuid.New().String(),
			DocumentID: docID,
			Content:    c.Content,
			ChunkOrder: c.Order,
			Embedding:  pgvector.NewVector(embeddings[i]),
		})
	}

	if len(chunkModels) > 0 {
		if err := idx.repo.SaveChunks(ctx, chunkModels); err != nil {
			return false, 0, fmt.Errorf("save chunks: %w", err)
		}
	}

	return true, len(chunkModels), nil
}

// removeOrphans deletes documents from DB that no longer exist on disk.
func (idx *Indexer) removeOrphans(ctx context.Context, kbID string, currentFiles []string) error {
	docs, err := idx.repo.ListDocumentsByKB(ctx, kbID)
	if err != nil {
		return fmt.Errorf("list documents: %w", err)
	}

	// For orphan detection, we check if any current file ends with the stored relative path.
	// This handles the case where folderPath differs between runs.
	for _, doc := range docs {
		found := false
		for _, f := range currentFiles {
			if strings.HasSuffix(filepath.ToSlash(f), filepath.ToSlash(doc.FilePath)) {
				found = true
				break
			}
		}
		if found {
			continue
		}

		idx.logger.InfoContext(ctx, "removing orphaned document",
			"kb_id", kbID, "file", doc.FilePath)

		if err := idx.repo.DeleteChunksByDocument(ctx, doc.ID); err != nil {
			idx.logger.WarnContext(ctx, "failed to delete orphan chunks",
				"document_id", doc.ID, "error", err)
		}
	}

	return nil
}
