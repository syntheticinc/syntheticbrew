package models

import "time"

// InterruptModel maps to the "interrupts" table.
// First-class state of a suspended agent for HITL Interrupt Primitive.
// Kind/schema/payload live in linked session_event_log rows — this table is a
// pure state-tracker with FK linkage to request and resolve events.
type InterruptModel struct {
	ID              string     `gorm:"primaryKey;type:uuid"`
	TenantID        string     `gorm:"type:uuid;not null;default:'00000000-0000-0000-0000-000000000001';index:idx_interrupts_pending,priority:1,where:status='pending'"`
	RequestEventID  string     `gorm:"type:uuid;not null;index:idx_interrupts_request_event"`
	Status          string     `gorm:"type:varchar(20);not null;default:'pending';index:idx_interrupts_pending,priority:2,where:status='pending'"`
	ResolveEventID  *string    `gorm:"type:uuid"`
	CreatedAt       time.Time  `gorm:"autoCreateTime"`
}

func (InterruptModel) TableName() string { return "interrupts" }
