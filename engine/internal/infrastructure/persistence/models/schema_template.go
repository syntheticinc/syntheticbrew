package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// SchemaTemplateModel maps to the "schema_templates" table.
//
// V2 Commit Group L (§2.2): system-wide catalog of schema starter templates.
// Seeded from `schema-templates.yaml` at engine startup via
// seedSchemaTemplates. "Use template" forks Definition into tenant-owned
// rows in schemas + agents + agent_relations + triggers with no FK back —
// catalog updates never touch existing forks.
type SchemaTemplateModel struct {
	ID          string                      `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	Name        string                      `gorm:"type:varchar(255);uniqueIndex;not null"`
	Display     string                      `gorm:"type:varchar(255);not null"`
	Description string                      `gorm:"type:text"`
	Category    string                      `gorm:"type:varchar(30);not null"`
	Icon        string                      `gorm:"type:varchar(64)"`
	Version     string                      `gorm:"type:varchar(32);not null;default:'1.0'"`
	Definition  SchemaTemplateDefinitionJSON `gorm:"type:jsonb;not null"`
	CreatedAt   time.Time                   `gorm:"autoCreateTime"`
	UpdatedAt   time.Time                   `gorm:"autoUpdateTime"`
}

// TableName returns the table name used by GORM.
func (SchemaTemplateModel) TableName() string { return "schema_templates" }

// SchemaTemplateDefinitionJSON is a GORM-compatible wrapper for the
// domain.SchemaTemplateDefinition persisted as jsonb. Round-trips through
// Scan/Value as JSON.
type SchemaTemplateDefinitionJSON domain.SchemaTemplateDefinition

// Scan implements sql.Scanner.
func (d *SchemaTemplateDefinitionJSON) Scan(value interface{}) error {
	if value == nil {
		*d = SchemaTemplateDefinitionJSON{}
		return nil
	}
	var raw []byte
	switch v := value.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return fmt.Errorf("SchemaTemplateDefinitionJSON.Scan: unsupported type %T", value)
	}
	if len(raw) == 0 {
		*d = SchemaTemplateDefinitionJSON{}
		return nil
	}
	return json.Unmarshal(raw, d)
}

// Value implements driver.Valuer.
func (d SchemaTemplateDefinitionJSON) Value() (driver.Value, error) {
	return json.Marshal(d)
}
