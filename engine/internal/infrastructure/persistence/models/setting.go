package models

import "time"

// DefaultTenantID is the sentinel UUID used for the single-tenant default
// in CE. Multi-tenant deployments assign real tenant UUIDs on registration; CE stamps every
// row with this constant so the column shape stays identical across
// editions (V2 §5.8 "Settings + BYOK").
const DefaultTenantID = "00000000-0000-0000-0000-000000000001"

// SettingModel maps to the "settings" key-value table.
//
// V2 final shape (Commit Group G, target-schema.dbml Table settings):
//   - Composite PK: (tenant_id, key)
//   - Value is jsonb — allows structured values (e.g. byok.allowed_providers
//     as a real array). The Go field is []byte holding raw JSON bytes; the
//     repository encodes/decodes around it.
//   - No `scope` column — redundant with tenant_id.
type SettingModel struct {
	TenantID  string    `gorm:"primaryKey;type:uuid;default:'00000000-0000-0000-0000-000000000001'"`
	Key       string    `gorm:"primaryKey;type:varchar(255)"`
	Value     []byte    `gorm:"type:jsonb;not null;default:'{}'"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

func (SettingModel) TableName() string { return "settings" }
