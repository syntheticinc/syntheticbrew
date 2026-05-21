package models

import (
	"time"

	"gorm.io/datatypes"
)

// SessionModel maps to the "sessions" table.
//
// Q.5: dropped agent_name and agent_id — a session belongs to a schema
// (schema_id), not a single agent. The entry agent is resolved via
// schemas.entry_agent_id at dispatch time. schema_id is now NOT NULL.
//
// Identity model: sessions carry the end-user's JWT sub claim (user_sub)
// rather than a FK to users. End-users are external — authentication is
// delegated (per docs/database/target-schema.dbml). Isolation key:
// (tenant_id, schema_id, user_sub).
type SessionModel struct {
	ID          string     `gorm:"primaryKey;type:uuid"`
	TenantID    string     `gorm:"type:uuid;not null;default:'00000000-0000-0000-0000-000000000001';index:idx_sessions_isolation,priority:1;index:idx_sessions_tenant_user_chrono,priority:1" json:"tenant_id"`
	SchemaID    string     `gorm:"type:uuid;not null;index;index:idx_sessions_isolation,priority:2" json:"schema_id"`
	UserSub     string     `gorm:"column:user_sub;type:varchar(255);not null;index:idx_sessions_isolation,priority:3;index:idx_sessions_tenant_user_chrono,priority:2" json:"user_sub"`
	Title       string     `gorm:"type:varchar(500)"`
	// Metadata is opaque per-session JSON storage for clients that build
	// multi-tenant layers on top of one SyntheticBrew schema. Engine never reads
	// or interprets the contents — it only persists and returns the raw
	// blob. Capped to 16KB at the HTTP layer to prevent unbounded growth.
	// Added in engine 1.1.4 (Liquibase changeset 006).
	Metadata    datatypes.JSON `gorm:"column:metadata;type:jsonb;not null;default:'{}'::jsonb"`
	Status      string     `gorm:"type:varchar(20);not null;default:active;index"`
	CreatedAt   time.Time  `gorm:"autoCreateTime;index:idx_sessions_tenant_user_chrono,priority:3"`
	UpdatedAt   time.Time  `gorm:"autoUpdateTime"`
	CompletedAt *time.Time

	Tasks []TaskModel `gorm:"foreignKey:SessionID"`
}

func (SessionModel) TableName() string { return "sessions" }
