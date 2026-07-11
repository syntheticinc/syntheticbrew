package models

import "time"

// ActiveUserModel is a last-seen record, one row per (tenant_id, user_sub).
// Each authenticated request touches the row's last_active_at; the number of
// users active within a window is COUNT(*) WHERE last_active_at > now() - window.
type ActiveUserModel struct {
	TenantID     string    `gorm:"primaryKey;type:uuid;default:'00000000-0000-0000-0000-000000000001'" json:"tenant_id"`
	UserSub      string    `gorm:"column:user_sub;primaryKey;type:varchar(255)" json:"user_sub"`
	LastActiveAt time.Time `gorm:"not null;default:now()" json:"last_active_at"`
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// TableName maps the model to the active_users table.
func (ActiveUserModel) TableName() string { return "active_users" }
