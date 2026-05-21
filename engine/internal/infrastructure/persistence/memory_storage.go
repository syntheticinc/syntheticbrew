package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"gorm.io/gorm"
)

// MemoryStorage implements memory persistence using GORM (PostgreSQL).
// All operations are tenant-scoped — memories are a triple of
// (tenant_id, schema_id, user_sub). In CE (single-tenant) mode the tenant
// falls back to domain.CETenantID when the context doesn't carry one.
type MemoryStorage struct {
	db *gorm.DB
}

// NewMemoryStorage creates a new memory storage.
func NewMemoryStorage(db *gorm.DB) *MemoryStorage {
	slog.InfoContext(context.Background(), "memory storage initialized (PostgreSQL)")
	return &MemoryStorage{db: db}
}

// tenantID extracts tenant from context, falling back to CETenantID for CE mode.
//
// TODO(tenancy): configrepo has a private `tenantIDFromCtx` helper with the
// same shape. Consolidate by promoting a public helper (e.g. in a new
// `internal/infrastructure/persistence/tenant.go`) once we are ready to touch
// both packages in the same change — this is a purely cosmetic duplication
// for now: the semantics match exactly (empty ctx → CETenantID).
func (s *MemoryStorage) tenantID(ctx context.Context) string {
	tid := domain.TenantIDFromContext(ctx)
	if tid == "" {
		return domain.CETenantID
	}
	return tid
}

// Store persists a memory entry. If max_entries is reached, evicts the oldest (FIFO).
// Tenant is stamped from context.
func (s *MemoryStorage) Store(ctx context.Context, mem *domain.Memory, maxEntries int) error {
	tenantID := s.tenantID(ctx)

	if maxEntries > 0 {
		if err := s.evictIfNeeded(ctx, tenantID, mem.SchemaID, mem.UserSub, maxEntries); err != nil {
			return fmt.Errorf("evict memories: %w", err)
		}
	}

	m := memoryToModel(mem)
	m.TenantID = tenantID
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	if err := s.db.WithContext(ctx).Create(&m).Error; err != nil {
		return fmt.Errorf("insert memory: %w", err)
	}

	mem.ID = m.ID
	slog.DebugContext(ctx, "memory stored", "id", mem.ID, "schema_id", mem.SchemaID, "user_sub", mem.UserSub)
	return nil
}

// ListBySchema retrieves all memories for a schema, ordered by most recent first (tenant-scoped).
func (s *MemoryStorage) ListBySchema(ctx context.Context, schemaID string) ([]*domain.Memory, error) {
	var ms []models.MemoryModel
	err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND schema_id = ?", s.tenantID(ctx), schemaID).
		Order("created_at DESC").
		Find(&ms).Error
	if err != nil {
		return nil, fmt.Errorf("list memories by schema: %w", err)
	}
	return modelsToMemories(ms), nil
}

// ListBySchemaAndUser retrieves memories for a schema+user pair (tenant-scoped).
func (s *MemoryStorage) ListBySchemaAndUser(ctx context.Context, schemaID, userSub string) ([]*domain.Memory, error) {
	var ms []models.MemoryModel
	err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND schema_id = ? AND user_sub = ?", s.tenantID(ctx), schemaID, userSub).
		Order("created_at DESC").
		Find(&ms).Error
	if err != nil {
		return nil, fmt.Errorf("list memories by schema+user: %w", err)
	}
	return modelsToMemories(ms), nil
}

// DeleteBySchema deletes all memories for a schema (tenant-scoped).
func (s *MemoryStorage) DeleteBySchema(ctx context.Context, schemaID string) (int64, error) {
	result := s.db.WithContext(ctx).
		Where("tenant_id = ? AND schema_id = ?", s.tenantID(ctx), schemaID).
		Delete(&models.MemoryModel{})
	if result.Error != nil {
		return 0, fmt.Errorf("delete memories by schema: %w", result.Error)
	}
	slog.InfoContext(ctx, "memories cleared", "schema_id", schemaID, "count", result.RowsAffected)
	return result.RowsAffected, nil
}

// DeleteByID deletes a single memory entry by ID (tenant-scoped).
func (s *MemoryStorage) DeleteByID(ctx context.Context, id string) error {
	result := s.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", s.tenantID(ctx), id).
		Delete(&models.MemoryModel{})
	if result.Error != nil {
		return fmt.Errorf("delete memory: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("memory not found: %s", id)
	}
	return nil
}

// CountBySchemaAndUser returns the number of memories for a schema+user pair (tenant-scoped).
func (s *MemoryStorage) CountBySchemaAndUser(ctx context.Context, schemaID, userSub string) (int, error) {
	var count int64
	err := s.db.WithContext(ctx).
		Model(&models.MemoryModel{}).
		Where("tenant_id = ? AND schema_id = ? AND user_sub = ?", s.tenantID(ctx), schemaID, userSub).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count memories: %w", err)
	}
	return int(count), nil
}

// evictIfNeeded removes the oldest entries when count >= maxEntries (FIFO, AC-MEM-RET-03).
// Caller provides the already-resolved tenantID to avoid repeated context lookups.
func (s *MemoryStorage) evictIfNeeded(ctx context.Context, tenantID, schemaID, userSub string, maxEntries int) error {
	var count int64
	if err := s.db.WithContext(ctx).
		Model(&models.MemoryModel{}).
		Where("tenant_id = ? AND schema_id = ? AND user_sub = ?", tenantID, schemaID, userSub).
		Count(&count).Error; err != nil {
		return fmt.Errorf("count memories: %w", err)
	}

	// Need to make room for the new entry
	toDelete := int(count) - maxEntries + 1
	if toDelete <= 0 {
		return nil
	}

	// Find IDs of oldest entries to delete
	var oldest []models.MemoryModel
	err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND schema_id = ? AND user_sub = ?", tenantID, schemaID, userSub).
		Order("created_at ASC").
		Limit(toDelete).
		Find(&oldest).Error
	if err != nil {
		return fmt.Errorf("find oldest memories: %w", err)
	}

	ids := make([]string, len(oldest))
	for i, m := range oldest {
		ids[i] = m.ID
	}

	if err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND id IN ?", tenantID, ids).
		Delete(&models.MemoryModel{}).Error; err != nil {
		return fmt.Errorf("delete oldest memories: %w", err)
	}

	slog.DebugContext(ctx, "FIFO eviction", "schema_id", schemaID, "user_sub", userSub, "evicted", len(ids))
	return nil
}

// CleanupExpiredBySchema deletes memories older than retentionDays for a given schema (tenant-scoped).
func (s *MemoryStorage) CleanupExpiredBySchema(ctx context.Context, schemaID string, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	result := s.db.WithContext(ctx).
		Where("tenant_id = ? AND schema_id = ? AND created_at < ?", s.tenantID(ctx), schemaID, cutoff).
		Delete(&models.MemoryModel{})
	if result.Error != nil {
		return 0, fmt.Errorf("cleanup expired memories: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		slog.InfoContext(ctx, "expired memories cleaned",
			"schema_id", schemaID, "retention_days", retentionDays, "deleted", result.RowsAffected)
	}
	return result.RowsAffected, nil
}

func memoryToModel(mem *domain.Memory) models.MemoryModel {
	metaJSON := "{}"
	if len(mem.Metadata) > 0 {
		if b, err := json.Marshal(mem.Metadata); err == nil {
			metaJSON = string(b)
		}
	}

	return models.MemoryModel{
		SchemaID: mem.SchemaID,
		UserSub:  mem.UserSub,
		Content:  mem.Content,
		Metadata: metaJSON,
	}
}

func modelToMemory(m *models.MemoryModel) *domain.Memory {
	metadata := make(map[string]string)
	if m.Metadata != "" {
		_ = json.Unmarshal([]byte(m.Metadata), &metadata)
	}

	return &domain.Memory{
		ID:        m.ID,
		SchemaID:  m.SchemaID,
		UserSub:   m.UserSub,
		Content:   m.Content,
		Metadata:  metadata,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

func modelsToMemories(ms []models.MemoryModel) []*domain.Memory {
	memories := make([]*domain.Memory, 0, len(ms))
	for i := range ms {
		memories = append(memories, modelToMemory(&ms[i]))
	}
	return memories
}
