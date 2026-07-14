package app

import (
	"context"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	admintools "github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools/admin"
	svcknowledge "github.com/syntheticinc/syntheticbrew/internal/service/knowledge"
	pluginpkg "github.com/syntheticinc/syntheticbrew/pkg/plugin"
	"gorm.io/gorm"
)

// embedderFactoryAdapter routes embedding construction through the plugin: for a
// model the plugin recognizes (by its DB-resolved fields — e.g. an opaque
// base-URL marker the engine never interprets) it returns the plugin's embedder;
// otherwise it declines (ok=false) and the upload service falls back to the
// built-in OpenAI-compatible client. Wired into the shared upload service so
// every ingest embedding passes one seam; the CE Noop plugin always declines.
type embedderFactoryAdapter struct {
	plugin pluginpkg.Plugin
}

func (a *embedderFactoryAdapter) EmbedderFor(ctx context.Context, info *svcknowledge.EmbeddingModelInfo) (svcknowledge.EmbeddingProvider, bool) {
	if a.plugin == nil {
		return nil, false
	}
	return a.plugin.EmbedderFor(ctx, info.BaseURL, info.APIKey, info.ModelName, info.EmbeddingDim)
}

// knowledgeToolAdapter implements admintools.KnowledgeToolDeps over the existing
// KB store, upload service, KB repository, and model table. One concrete type
// satisfies all the narrow tool-side interfaces (each tool depends only on the
// slice it uses — ISP). Every method derives the tenant from ctx; the tools
// never accept a tenant argument.
type knowledgeToolAdapter struct {
	kbStore *kbStoreAdapter
	kbRepo  *configrepo.GORMKnowledgeBaseRepository
	upload  *svcknowledge.UploadService
	db      *gorm.DB
}

func newKnowledgeToolAdapter(
	kbStore *kbStoreAdapter,
	kbRepo *configrepo.GORMKnowledgeBaseRepository,
	upload *svcknowledge.UploadService,
	db *gorm.DB,
) *knowledgeToolAdapter {
	return &knowledgeToolAdapter{kbStore: kbStore, kbRepo: kbRepo, upload: upload, db: db}
}

func (a *knowledgeToolAdapter) tenantID(ctx context.Context) string {
	tid := domain.TenantIDFromContext(ctx)
	if tid == "" {
		return domain.CETenantID
	}
	return tid
}

// CreateKB creates a knowledge base. The KB store resolves embeddingModelRef
// (name or UUID) to a tenant-scoped embedding model UUID.
func (a *knowledgeToolAdapter) CreateKB(ctx context.Context, name, description, embeddingModelRef string) (*admintools.KnowledgeBaseInfo, error) {
	info, err := a.kbStore.Create(ctx, name, description, embeddingModelRef, a.tenantID(ctx))
	if err != nil {
		return nil, err
	}
	return &admintools.KnowledgeBaseInfo{
		ID:               info.ID,
		Name:             info.Name,
		Description:      info.Description,
		EmbeddingModelID: info.EmbeddingModelID,
	}, nil
}

// GetKBIDByName resolves a KB name to its UUID within the caller's tenant.
func (a *knowledgeToolAdapter) GetKBIDByName(ctx context.Context, name string) (string, error) {
	return a.kbRepo.GetKBIDByName(ctx, name)
}

// GetKBByID loads a KB by UUID (tenant-scoped). Returns nil when absent.
func (a *knowledgeToolAdapter) GetKBByID(ctx context.Context, id string) (*admintools.KnowledgeBaseInfo, error) {
	info, err := a.kbStore.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if info == nil {
		return nil, nil
	}
	return &admintools.KnowledgeBaseInfo{
		ID:               info.ID,
		Name:             info.Name,
		Description:      info.Description,
		EmbeddingModelID: info.EmbeddingModelID,
	}, nil
}

// LinkAgent links a KB to an agent; both are re-verified in-tenant by the repo.
func (a *knowledgeToolAdapter) LinkAgent(ctx context.Context, kbID, agentName string) error {
	return a.kbStore.LinkAgent(ctx, kbID, agentName)
}

// UploadDocument ingests a document via the shared upload service (whose
// admission gate enforces the document quota on this path too).
func (a *knowledgeToolAdapter) UploadDocument(ctx context.Context, kbID, embeddingModelID, fileName, fileType string, fileSize int64, fileHash string, content []byte) (*admintools.DocumentInfo, error) {
	resp, err := a.upload.UploadFileToKB(ctx, a.tenantID(ctx), kbID, embeddingModelID, fileName, fileType, fileSize, fileHash, content)
	if err != nil {
		return nil, err
	}
	return &admintools.DocumentInfo{
		ID:         resp.ID,
		FileName:   resp.FileName,
		FileType:   resp.FileType,
		Status:     resp.Status,
		ChunkCount: resp.ChunkCount,
	}, nil
}

// DeleteDocument removes a document from a KB (tenant-scoped inside the service).
func (a *knowledgeToolAdapter) DeleteDocument(ctx context.Context, kbID, fileID string) error {
	return a.upload.DeleteFileByKB(ctx, kbID, fileID)
}

// ListDocuments returns a KB's documents with indexing status.
func (a *knowledgeToolAdapter) ListDocuments(ctx context.Context, kbID string) ([]admintools.DocumentInfo, error) {
	files, err := a.upload.ListFilesByKB(ctx, kbID)
	if err != nil {
		return nil, err
	}
	out := make([]admintools.DocumentInfo, 0, len(files))
	for _, f := range files {
		out = append(out, admintools.DocumentInfo{
			ID:         f.ID,
			FileName:   f.FileName,
			FileType:   f.FileType,
			Status:     f.Status,
			ChunkCount: f.ChunkCount,
		})
	}
	return out, nil
}

// ResolveSingleEmbeddingModel returns the tenant's only embedding model UUID.
// Zero or more than one is an error the tool renders as "specify the embedding
// model explicitly" — a KB without an embedding model cannot ingest documents,
// so an ambiguous default is refused rather than silently guessed.
func (a *knowledgeToolAdapter) ResolveSingleEmbeddingModel(ctx context.Context) (string, error) {
	tenantID := a.tenantID(ctx)
	var ids []string
	if err := a.db.WithContext(ctx).
		Model(&models.LLMProviderModel{}).
		Where("tenant_id = ? AND kind = ?", tenantID, "embedding").
		Pluck("id", &ids).Error; err != nil {
		return "", fmt.Errorf("list embedding models: %w", err)
	}
	switch len(ids) {
	case 0:
		return "", fmt.Errorf("no embedding model configured: specify the embedding model explicitly")
	case 1:
		return ids[0], nil
	default:
		return "", fmt.Errorf("multiple embedding models configured: specify the embedding model explicitly")
	}
}
