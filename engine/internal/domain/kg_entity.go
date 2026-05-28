package domain

import (
	"encoding/json"
	"fmt"
	"time"
)

// KGEntityMaxIDLength caps the length of an entity_id. Same as the varchar(128)
// constraint at the database level. Exported for use by import-side validators.
const KGEntityMaxIDLength = 128

// KGEntityMaxDataBytes caps the size of a single entity's JSONB payload.
// 100 KB is large enough for prose body fields while preventing pathological
// rows. Exported so the apply usecase can enforce limits before persistence.
const KGEntityMaxDataBytes = 100 * 1024

// KGEntity is one instance of a customer-defined entity type. Its shape is
// dictated by the KGEntitySchema with matching (TenantID, BundleName,
// EntityType). Data is stored as JSONB so any schema-conformant document
// can be persisted without DDL changes.
//
// SchemaHash records which schema version validated this entity row. If the
// schema is later replaced, defensive readers may re-validate against the
// current schema; mismatching hashes are a signal of drift.
type KGEntity struct {
	TenantID   string
	BundleName string
	EntityType string
	EntityID   string
	Data       []byte
	SchemaHash string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// NewKGEntity constructs a KGEntity with field + JSON validation. The data
// argument must be valid JSON; non-JSON payloads are rejected to keep the
// JSONB column well-formed.
func NewKGEntity(
	tenantID, bundleName, entityType, entityID string,
	data []byte,
	schemaHash string,
) (*KGEntity, error) {
	e := &KGEntity{
		TenantID:   tenantID,
		BundleName: bundleName,
		EntityType: entityType,
		EntityID:   entityID,
		Data:       data,
		SchemaHash: schemaHash,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return e, nil
}

// Validate checks KGEntity invariants. Called by constructors and the apply
// usecase before any state mutation.
//
// JSON Schema validation against the entity's schema is NOT performed here
// (it requires the schema document, which is a separate domain aggregate);
// callers in the usecase layer must invoke the SchemaValidator interface
// in addition to this method.
func (e *KGEntity) Validate() error {
	if e.TenantID == "" {
		return fmt.Errorf("kg_entity.tenant_id is required")
	}
	if !ValidKGBundleName(e.BundleName) {
		return fmt.Errorf("kg_entity.bundle_name %q invalid", e.BundleName)
	}
	if !ValidKGEntityType(e.EntityType) {
		return fmt.Errorf("kg_entity.entity_type %q invalid", e.EntityType)
	}
	if e.EntityID == "" {
		return fmt.Errorf("kg_entity.entity_id is required")
	}
	if len(e.EntityID) > KGEntityMaxIDLength {
		return fmt.Errorf("kg_entity.entity_id length %d exceeds max %d", len(e.EntityID), KGEntityMaxIDLength)
	}
	if len(e.Data) == 0 {
		return fmt.Errorf("kg_entity.data is required")
	}
	if len(e.Data) > KGEntityMaxDataBytes {
		return fmt.Errorf("kg_entity.data size %d exceeds max %d bytes", len(e.Data), KGEntityMaxDataBytes)
	}
	if !json.Valid(e.Data) {
		return fmt.Errorf("kg_entity.data must be valid JSON")
	}
	if e.SchemaHash == "" {
		return fmt.Errorf("kg_entity.schema_hash is required")
	}
	return nil
}
