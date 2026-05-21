package configrepo

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// GORMInterruptRepository persists HITL interrupt state, tenant-scoped.
type GORMInterruptRepository struct {
	db *gorm.DB
}

func NewGORMInterruptRepository(db *gorm.DB) *GORMInterruptRepository {
	return &GORMInterruptRepository{db: db}
}

// Create inserts a new pending interrupt, stamping tenant from ctx.
func (r *GORMInterruptRepository) Create(ctx context.Context, interrupt *domain.Interrupt) error {
	m := models.InterruptModel{
		ID:             interrupt.ID,
		TenantID:       tenantIDFromCtx(ctx),
		RequestEventID: interrupt.RequestEventID,
		Status:         string(domain.InterruptStatusPending),
	}
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		return fmt.Errorf("create interrupt: %w", err)
	}
	interrupt.TenantID = m.TenantID
	interrupt.Status = domain.InterruptStatusPending
	interrupt.CreatedAt = m.CreatedAt
	return nil
}

// Get returns an interrupt by ID, tenant-scoped. Returns (nil, nil) when not found.
func (r *GORMInterruptRepository) Get(ctx context.Context, id string) (*domain.Interrupt, error) {
	var m models.InterruptModel
	err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("id = ?", id).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get interrupt: %w", err)
	}
	return modelToDomain(&m), nil
}

// LoadWithRequestEvent JOINs the interrupt row with its request event so
// kind/schema/session_id (from proto_data) are available in one round-trip.
// Tenant-scoped; returns (nil, nil, nil) when not found.
func (r *GORMInterruptRepository) LoadWithRequestEvent(
	ctx context.Context,
	id string,
) (*domain.Interrupt, *models.SessionEventLogModel, error) {
	type row struct {
		models.InterruptModel
		EventSessionID string `gorm:"column:event_session_id"`
		EventProtoData []byte `gorm:"column:event_proto_data"`
	}

	var rec row
	err := r.db.WithContext(ctx).
		Table("interrupts AS i").
		Select(
			"i.id, i.tenant_id, i.request_event_id, i.status, i.resolve_event_id, i.created_at, "+
				"e.session_id AS event_session_id, e.proto_data AS event_proto_data",
		).
		Joins("JOIN session_event_log AS e ON e.id = i.request_event_id").
		Where("i.id = ? AND i.tenant_id = ?", id, tenantIDFromCtx(ctx)).
		Take(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("load interrupt with request event: %w", err)
	}

	interrupt := modelToDomain(&rec.InterruptModel)
	requestEvent := &models.SessionEventLogModel{
		ID:        rec.RequestEventID,
		SessionID: rec.EventSessionID,
		EventType: "interrupt_request",
		ProtoData: rec.EventProtoData,
	}
	return interrupt, requestEvent, nil
}

// MarkResolved is an atomic pending→resolved transition (WHERE status='pending').
// Returns false on 0 rows affected — caller maps to 409 Conflict.
func (r *GORMInterruptRepository) MarkResolved(ctx context.Context, id, resolveEventID string) (bool, error) {
	result := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Model(&models.InterruptModel{}).
		Where("id = ? AND status = ?", id, string(domain.InterruptStatusPending)).
		Updates(map[string]interface{}{
			"status":           string(domain.InterruptStatusResolved),
			"resolve_event_id": resolveEventID,
		})
	if result.Error != nil {
		return false, fmt.Errorf("mark interrupt resolved: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

// MarkAbandonedForSession transitions all pending interrupts in the given
// session to 'abandoned'. Triggered when a regular user_message arrives
// while interrupts are pending so they can't be resumed later. Joins
// session_event_log because the interrupts table doesn't store session_id.
func (r *GORMInterruptRepository) MarkAbandonedForSession(ctx context.Context, sessionID string) (int64, error) {
	subq := r.db.WithContext(ctx).
		Table("interrupts AS i").
		Select("i.id").
		Joins("JOIN session_event_log AS e ON e.id = i.request_event_id").
		Where("e.session_id = ?", sessionID).
		Where("i.tenant_id = ?", tenantIDFromCtx(ctx)).
		Where("i.status = ?", string(domain.InterruptStatusPending))

	result := r.db.WithContext(ctx).
		Model(&models.InterruptModel{}).
		Where("id IN (?)", subq).
		Update("status", string(domain.InterruptStatusAbandoned))
	if result.Error != nil {
		return 0, fmt.Errorf("mark interrupts abandoned: %w", result.Error)
	}
	return result.RowsAffected, nil
}

func modelToDomain(m *models.InterruptModel) *domain.Interrupt {
	return &domain.Interrupt{
		ID:             m.ID,
		TenantID:       m.TenantID,
		RequestEventID: m.RequestEventID,
		Status:         domain.InterruptStatus(m.Status),
		ResolveEventID: m.ResolveEventID,
		CreatedAt:      m.CreatedAt,
	}
}
