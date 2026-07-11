package models

import "time"

// TenantPolicyModel is a protected per-tenant key/value entry, one row per
// (tenant_id, key). Unlike the settings table (tenant-writable via HTTP),
// rows here are written only through the plugin seam, so tenants cannot
// change them via the API.
type TenantPolicyModel struct {
	ID        string    `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	TenantID  string    `gorm:"type:uuid;not null;default:'00000000-0000-0000-0000-000000000001';uniqueIndex:uq_tenant_policies_tenant_key,priority:1" json:"tenant_id"`
	Key       string    `gorm:"type:varchar(100);not null;uniqueIndex:uq_tenant_policies_tenant_key,priority:2" json:"key"`
	Value     string    `gorm:"type:text;not null;default:''" json:"value"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// TableName maps the model to the tenant_policies table.
func (TenantPolicyModel) TableName() string { return "tenant_policies" }
