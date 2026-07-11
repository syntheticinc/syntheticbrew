package models

import "time"

// APITokenModel maps to the "api_tokens" table.
//
// Identity: user_sub is the JWT `sub` claim of the admin/user who created the
// token. No FK to a users table — identity is external (multi-tenant) or synthetic
// (CE local admin = "local-admin"). varchar, not uuid.
type APITokenModel struct {
	ID         string     `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	UserSub    string     `gorm:"column:user_sub;type:varchar(255);not null;default:''"`
	Name       string     `gorm:"uniqueIndex;not null"`
	TokenHash  string     `gorm:"uniqueIndex;not null"`
	ScopesMask int        `gorm:"not null;default:0"`
	TenantID   string     `gorm:"type:uuid;not null;default:'00000000-0000-0000-0000-000000000001'" json:"tenant_id"`
	CreatedAt  time.Time  `gorm:"autoCreateTime"`
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

func (APITokenModel) TableName() string { return "api_tokens" }

// Scope bitmask constants.
const (
	ScopeChat          = 1
	ScopeTasks         = 2
	ScopeAgentsRead    = 4
	ScopeConfig        = 8
	ScopeAdmin         = 16
	ScopeAgentsWrite   = 32
	ScopeModelsRead    = 64
	ScopeModelsWrite   = 128
	ScopeMCPRead       = 256
	ScopeMCPWrite      = 512
	ScopeTriggersRead  = 1024
	ScopeTriggersWrite = 2048
)

// HasScope checks whether the token has the given scope bit set.
func (t *APITokenModel) HasScope(scope int) bool {
	return t.ScopesMask&scope != 0
}
