package configrepo

import (
	"context"
	"fmt"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"gorm.io/gorm"
)

// AuditFilters holds optional filters for querying audit logs.
type AuditFilters struct {
	ActorType string
	Action    string
	Resource  string
	From      *time.Time
	To        *time.Time
}

// GORMAuditRepository provides read access to the audit_logs table.
type GORMAuditRepository struct {
	db *gorm.DB
}

// NewGORMAuditRepository creates a new GORMAuditRepository.
func NewGORMAuditRepository(db *gorm.DB) *GORMAuditRepository {
	return &GORMAuditRepository{db: db}
}

// List returns a page of audit log entries matching the given filters (tenant-scoped).
// Returns the entries, total count, and any error.
func (r *GORMAuditRepository) List(ctx context.Context, filters AuditFilters, page, perPage int) ([]models.AuditLogModel, int64, error) {
	query := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Model(&models.AuditLogModel{})

	if filters.ActorType != "" {
		query = query.Where("actor_type = ?", filters.ActorType)
	}
	if filters.Action != "" {
		query = query.Where("action = ?", filters.Action)
	}
	if filters.Resource != "" {
		query = query.Where("resource = ?", filters.Resource)
	}
	if filters.From != nil {
		query = query.Where("occurred_at >= ?", *filters.From)
	}
	if filters.To != nil {
		query = query.Where("occurred_at <= ?", *filters.To)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count audit logs: %w", err)
	}

	offset := (page - 1) * perPage
	var logs []models.AuditLogModel
	if err := query.Order("occurred_at DESC").Offset(offset).Limit(perPage).Find(&logs).Error; err != nil {
		return nil, 0, fmt.Errorf("list audit logs: %w", err)
	}

	return logs, total, nil
}
