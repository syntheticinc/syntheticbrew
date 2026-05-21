package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// MCPCatalogModel maps to the "mcp_catalog" table.
//
// V2 Commit Group C (§5.5): system-wide MCP server catalog. Rows are seeded
// from `mcp-catalog.yaml` at engine startup via `seedMCPCatalog`. Instances
// created via "install from catalog" are copied into `mcp_servers` with no FK
// back — catalog updates never touch existing installs.
type MCPCatalogModel struct {
	ID            string            `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	Name          string            `gorm:"type:varchar(255);uniqueIndex;not null"`
	Display       string            `gorm:"type:varchar(255);not null"`
	Description   string            `gorm:"type:text"`
	Category      string            `gorm:"type:varchar(30);not null"`
	Verified      bool              `gorm:"not null;default:false"`
	Packages      MCPCatalogPackages `gorm:"type:jsonb;not null;default:'[]'"`
	ProvidedTools MCPCatalogTools    `gorm:"type:jsonb"`
	CreatedAt     time.Time         `gorm:"autoCreateTime"`
	UpdatedAt     time.Time         `gorm:"autoUpdateTime"`
}

// TableName returns the table name used by GORM.
func (MCPCatalogModel) TableName() string { return "mcp_catalog" }

// MCPCatalogPackages is a GORM-compatible slice type for the jsonb `packages`
// column. Round-trips through Scan/Value as JSON.
type MCPCatalogPackages []domain.MCPCatalogPackage

// Scan implements sql.Scanner for MCPCatalogPackages.
func (p *MCPCatalogPackages) Scan(value interface{}) error {
	if value == nil {
		*p = MCPCatalogPackages{}
		return nil
	}
	var raw []byte
	switch v := value.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return fmt.Errorf("MCPCatalogPackages.Scan: unsupported type %T", value)
	}
	if len(raw) == 0 {
		*p = MCPCatalogPackages{}
		return nil
	}
	return json.Unmarshal(raw, p)
}

// Value implements driver.Valuer for MCPCatalogPackages.
func (p MCPCatalogPackages) Value() (driver.Value, error) {
	if p == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(p)
}

// MCPCatalogTools is a GORM-compatible slice type for the jsonb
// `provided_tools` column. Round-trips through Scan/Value as JSON.
type MCPCatalogTools []domain.MCPCatalogTool

// Scan implements sql.Scanner for MCPCatalogTools.
func (t *MCPCatalogTools) Scan(value interface{}) error {
	if value == nil {
		*t = MCPCatalogTools{}
		return nil
	}
	var raw []byte
	switch v := value.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return fmt.Errorf("MCPCatalogTools.Scan: unsupported type %T", value)
	}
	if len(raw) == 0 {
		*t = MCPCatalogTools{}
		return nil
	}
	return json.Unmarshal(raw, t)
}

// Value implements driver.Valuer for MCPCatalogTools.
func (t MCPCatalogTools) Value() (driver.Value, error) {
	if t == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(t)
}
