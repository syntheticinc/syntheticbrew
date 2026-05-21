package configrepo

import (
	"context"
	"fmt"
	"time"

	"github.com/pgvector/pgvector-go"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"gorm.io/gorm"
)

// GORMKnowledgeRepository provides CRUD and vector search for knowledge documents and chunks.
type GORMKnowledgeRepository struct {
	db *gorm.DB
}

// NewGORMKnowledgeRepository creates a new GORMKnowledgeRepository.
func NewGORMKnowledgeRepository(db *gorm.DB) *GORMKnowledgeRepository {
	return &GORMKnowledgeRepository{db: db}
}

// tenantID extracts tenant from context, falling back to CETenantID for CE mode.
// Delegates to the package-level helper so all repos agree on tenant resolution.
func (r *GORMKnowledgeRepository) tenantID(ctx context.Context) string {
	return tenantIDFromCtx(ctx)
}

// GetDocumentByPath returns a document by KB ID and file path, or nil if not found.
func (r *GORMKnowledgeRepository) GetDocumentByPath(ctx context.Context, kbID, filePath string) (*models.KnowledgeDocument, error) {
	tenantID := r.tenantID(ctx)
	var doc models.KnowledgeDocument
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND file_path = ?", tenantID, kbID, filePath).
		First(&doc).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get document by path: %w", err)
	}
	return &doc, nil
}

// SaveDocument creates or updates a knowledge document, stamping tenant when missing.
func (r *GORMKnowledgeRepository) SaveDocument(ctx context.Context, doc *models.KnowledgeDocument) error {
	if doc.TenantID == "" {
		doc.TenantID = r.tenantID(ctx)
	}
	if err := r.db.WithContext(ctx).Save(doc).Error; err != nil {
		return fmt.Errorf("save document: %w", err)
	}
	return nil
}

// ListDocumentsByKB returns all documents belonging to a knowledge base (tenant-scoped).
func (r *GORMKnowledgeRepository) ListDocumentsByKB(ctx context.Context, kbID string) ([]models.KnowledgeDocument, error) {
	tenantID := r.tenantID(ctx)
	var docs []models.KnowledgeDocument
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).
		Find(&docs).Error; err != nil {
		return nil, fmt.Errorf("list documents by KB: %w", err)
	}
	return docs, nil
}

// GetDocumentByID returns a document by its ID (tenant-scoped), or nil if not found.
func (r *GORMKnowledgeRepository) GetDocumentByID(ctx context.Context, id string) (*models.KnowledgeDocument, error) {
	var doc models.KnowledgeDocument
	err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("id = ?", id).
		First(&doc).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get document by id: %w", err)
	}
	return &doc, nil
}

// DeleteDocument removes a single document by ID (tenant-scoped).
func (r *GORMKnowledgeRepository) DeleteDocument(ctx context.Context, id string) error {
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("id = ?", id).
		Delete(&models.KnowledgeDocument{}).Error; err != nil {
		return fmt.Errorf("delete document: %w", err)
	}
	return nil
}

// SaveChunks inserts a batch of knowledge chunks, stamping tenant when missing.
func (r *GORMKnowledgeRepository) SaveChunks(ctx context.Context, chunks []models.KnowledgeChunk) error {
	if len(chunks) == 0 {
		return nil
	}
	tenantID := r.tenantID(ctx)
	for i := range chunks {
		if chunks[i].TenantID == "" {
			chunks[i].TenantID = tenantID
		}
	}
	if err := r.db.WithContext(ctx).Create(&chunks).Error; err != nil {
		return fmt.Errorf("save chunks: %w", err)
	}
	return nil
}

// DeleteChunksByDocument removes all chunks belonging to a document (tenant-scoped).
func (r *GORMKnowledgeRepository) DeleteChunksByDocument(ctx context.Context, documentID string) error {
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("document_id = ?", documentID).
		Delete(&models.KnowledgeChunk{}).Error; err != nil {
		return fmt.Errorf("delete chunks by document: %w", err)
	}
	return nil
}

// SearchSimilarByKBs finds the most similar chunks across multiple knowledge bases.
func (r *GORMKnowledgeRepository) SearchSimilarByKBs(ctx context.Context, kbIDs []string, embedding pgvector.Vector, limit int, similarityThreshold float64) ([]models.KnowledgeChunk, error) {
	if len(kbIDs) == 0 {
		return nil, nil
	}
	tenantID := r.tenantID(ctx)
	// Join through knowledge_documents to get KB-scoped chunks (knowledge_base_id dropped from chunks).
	var chunks []models.KnowledgeChunk
	var err error
	if similarityThreshold > 0 {
		err = r.db.WithContext(ctx).
			Raw(`SELECT kc.* FROM knowledge_chunks kc
				JOIN knowledge_documents kd ON kd.id = kc.document_id
				WHERE kc.tenant_id = ? AND kd.knowledge_base_id IN ?
				AND (1 - (kc.embedding_vector <=> ?)) >= ?
				ORDER BY kc.embedding_vector <=> ? LIMIT ?`,
				tenantID, kbIDs, embedding, similarityThreshold, embedding, limit).
			Scan(&chunks).Error
	} else {
		err = r.db.WithContext(ctx).
			Raw(`SELECT kc.* FROM knowledge_chunks kc
				JOIN knowledge_documents kd ON kd.id = kc.document_id
				WHERE kc.tenant_id = ? AND kd.knowledge_base_id IN ?
				ORDER BY kc.embedding_vector <=> ? LIMIT ?`,
				tenantID, kbIDs, embedding, limit).
			Scan(&chunks).Error
	}
	if err != nil {
		return nil, fmt.Errorf("search similar by KBs: %w", err)
	}
	return chunks, nil
}

// SearchByKeywordKBs finds chunks by keyword across multiple knowledge bases.
func (r *GORMKnowledgeRepository) SearchByKeywordKBs(ctx context.Context, kbIDs []string, keyword string, limit int) ([]models.KnowledgeChunk, error) {
	if len(kbIDs) == 0 {
		return nil, nil
	}
	tenantID := r.tenantID(ctx)
	var chunks []models.KnowledgeChunk
	err := r.db.WithContext(ctx).
		Raw(`SELECT kc.* FROM knowledge_chunks kc
			JOIN knowledge_documents kd ON kd.id = kc.document_id
			WHERE kc.tenant_id = ? AND kd.knowledge_base_id IN ? AND kc.content ILIKE ?
			LIMIT ?`,
			tenantID, kbIDs, "%"+keyword+"%", limit).
		Scan(&chunks).Error
	if err != nil {
		return nil, fmt.Errorf("search by keyword KBs: %w", err)
	}
	return chunks, nil
}

// DeleteDocumentsByKB removes all documents for a given knowledge base (tenant-scoped).
func (r *GORMKnowledgeRepository) DeleteDocumentsByKB(ctx context.Context, kbID string) error {
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("knowledge_base_id = ?", kbID).
		Delete(&models.KnowledgeDocument{}).Error; err != nil {
		return fmt.Errorf("delete documents by KB: %w", err)
	}
	return nil
}

// DeleteChunksByKB removes all chunks for documents belonging to a knowledge base (tenant-scoped).
// Both the outer DELETE and the inner SELECT are scoped to the current tenant so that
// a caller cannot delete another tenant's chunks by passing their kbID/documentID.
func (r *GORMKnowledgeRepository) DeleteChunksByKB(ctx context.Context, kbID string) error {
	tenantID := r.tenantID(ctx)
	if err := r.db.WithContext(ctx).
		Exec(`DELETE FROM knowledge_chunks
			WHERE tenant_id = ? AND document_id IN
				(SELECT id FROM knowledge_documents
				 WHERE tenant_id = ? AND knowledge_base_id = ?)`,
			tenantID, tenantID, kbID).Error; err != nil {
		return fmt.Errorf("delete chunks by KB: %w", err)
	}
	return nil
}

// GetStatsByKBs returns document count, chunk count, and last indexed time for given KB IDs (tenant-scoped).
func (r *GORMKnowledgeRepository) GetStatsByKBs(ctx context.Context, kbIDs []string) (docCount int, chunkCount int, lastIndexed *time.Time, err error) {
	if len(kbIDs) == 0 {
		return 0, 0, nil, nil
	}
	tenantID := r.tenantID(ctx)
	var dc int64
	if err := r.db.WithContext(ctx).Model(&models.KnowledgeDocument{}).
		Where("tenant_id = ? AND knowledge_base_id IN ?", tenantID, kbIDs).Count(&dc).Error; err != nil {
		return 0, 0, nil, fmt.Errorf("count documents: %w", err)
	}

	var cc int64
	if err := r.db.WithContext(ctx).
		Raw(`SELECT COUNT(*) FROM knowledge_chunks kc
			JOIN knowledge_documents kd ON kd.id = kc.document_id
			WHERE kc.tenant_id = ? AND kd.knowledge_base_id IN ?`, tenantID, kbIDs).
		Scan(&cc).Error; err != nil {
		return 0, 0, nil, fmt.Errorf("count chunks: %w", err)
	}

	var doc models.KnowledgeDocument
	result := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id IN ?", tenantID, kbIDs).
		Order("indexed_at DESC").
		First(&doc)
	if result.Error != nil && result.Error != gorm.ErrRecordNotFound {
		return 0, 0, nil, fmt.Errorf("get last indexed: %w", result.Error)
	}

	var li *time.Time
	if result.Error == nil {
		li = &doc.IndexedAt
	}

	return int(dc), int(cc), li, nil
}
