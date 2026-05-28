package models

import (
	"time"

	"github.com/lib/pq"
	"gorm.io/datatypes"
)

// KGBundleModel maps to the "kg_bundle" table — one row per Knowledge Graph
// bundle (customer's deploy unit). Composite primary key (tenant_id,
// bundle_name) guarantees per-tenant isolation: tenant A cannot read or
// modify tenant B's bundle even when bundle_name matches.
type KGBundleModel struct {
	TenantID   string         `gorm:"primaryKey;type:uuid;default:'00000000-0000-0000-0000-000000000001'"`
	BundleName string         `gorm:"primaryKey;type:varchar(64)"`
	Version    string         `gorm:"type:varchar(32);not null"`
	Manifest   datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'::jsonb"`
	CreatedAt  time.Time      `gorm:"autoCreateTime"`
	UpdatedAt  time.Time      `gorm:"autoUpdateTime"`
}

func (KGBundleModel) TableName() string { return "kg_bundle" }

// KGEntitySchemaModel maps to the "kg_entity_schema" table — one row per
// entity type inside a bundle. Composite primary key (tenant_id, bundle_name,
// entity_type). Foreign key cascades from kg_bundle so deleting a bundle
// drops its schemas (and via further cascade, its entities).
type KGEntitySchemaModel struct {
	TenantID        string         `gorm:"primaryKey;type:uuid;default:'00000000-0000-0000-0000-000000000001'"`
	BundleName      string         `gorm:"primaryKey;type:varchar(64)"`
	EntityType      string         `gorm:"primaryKey;type:varchar(64)"`
	SchemaJSON      datatypes.JSON `gorm:"type:jsonb;not null;column:schema_json"`
	SchemaHash      string         `gorm:"type:char(64);not null"`
	IDField         string         `gorm:"type:varchar(64);not null;column:id_field"`
	ExposeTools     pq.StringArray `gorm:"type:text[];not null;default:ARRAY['list','get']::text[];column:expose_tools"`
	ToolDescription string         `gorm:"type:text;column:tool_description"`
	CreatedAt       time.Time      `gorm:"autoCreateTime"`
	UpdatedAt       time.Time      `gorm:"autoUpdateTime"`
}

func (KGEntitySchemaModel) TableName() string { return "kg_entity_schema" }

// KGEntityModel maps to the "kg_entity" table — one row per entity instance.
// Composite primary key (tenant_id, bundle_name, entity_type, entity_id).
// data is a JSONB blob storing the customer-supplied entity document.
type KGEntityModel struct {
	TenantID   string         `gorm:"primaryKey;type:uuid;default:'00000000-0000-0000-0000-000000000001'"`
	BundleName string         `gorm:"primaryKey;type:varchar(64)"`
	EntityType string         `gorm:"primaryKey;type:varchar(64)"`
	EntityID   string         `gorm:"primaryKey;type:varchar(128)"`
	Data       datatypes.JSON `gorm:"type:jsonb;not null"`
	SchemaHash string         `gorm:"type:char(64);not null"`
	CreatedAt  time.Time      `gorm:"autoCreateTime"`
	UpdatedAt  time.Time      `gorm:"autoUpdateTime"`
}

func (KGEntityModel) TableName() string { return "kg_entity" }
