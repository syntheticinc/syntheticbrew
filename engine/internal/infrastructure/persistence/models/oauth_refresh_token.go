package models

import "time"

// OAuthRefreshTokenModel maps to the "oauth_refresh_tokens" table.
//
// The raw refresh token is never stored — only TokenHash (SHA-256 hex).
// FamilyID groups a rotation chain so reuse of a revoked token can revoke the
// whole family. CodeJTI is the authorization-code identifier the family's first
// token was issued from; its unique constraint makes code replay detectable.
// It is nullable because only the first token of a family carries one.
type OAuthRefreshTokenModel struct {
	ID        string  `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	TenantID  string  `gorm:"column:tenant_id;type:uuid;not null;default:'00000000-0000-0000-0000-000000000001'"`
	UserSub   string  `gorm:"column:user_sub;type:varchar(255);not null"`
	CidHash   string  `gorm:"column:cid_hash;type:varchar(64);not null"`
	Scope     string  `gorm:"column:scope;type:varchar(255);not null"`
	Resource  string  `gorm:"column:resource;type:varchar(512);not null"`
	FamilyID  string  `gorm:"column:family_id;type:uuid;not null"`
	TokenHash string  `gorm:"column:token_hash;type:varchar(64);not null;uniqueIndex"`
	CodeJTI   *string `gorm:"column:code_jti;type:varchar(64);uniqueIndex"`
	CreatedAt time.Time
	ExpiresAt time.Time  `gorm:"column:expires_at;not null"`
	RevokedAt *time.Time `gorm:"column:revoked_at"`
}

// TableName pins the table name (GORM would otherwise pluralize the struct).
func (OAuthRefreshTokenModel) TableName() string { return "oauth_refresh_tokens" }
