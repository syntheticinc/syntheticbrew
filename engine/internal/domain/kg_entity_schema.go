package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"time"
)

// kgEntityTypeRegex enforces a safe identifier for entity types. Lower-case
// letters, digits, underscores. Cannot start or end with an underscore.
// Length 2-64. The same regex is enforced at the database level via a CHECK
// constraint on kg_entity_schema.entity_type.
//
// Note: entity_type names become the suffix of auto-generated MCP tool names
// (`list_<entity_type>`, `get_<entity_type>`). The regex ensures the resulting
// tool names are also valid MCP tool identifiers.
var kgEntityTypeRegex = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}[a-z0-9]$`)

// ExposeToolList, ExposeToolGet, ExposeToolListIDs are the canonical values
// for KGEntitySchema.ExposeTools. Customer schemas use these in
// `x-tool-expose` annotation; the import layer canonicalises into this set.
const (
	ExposeToolList    = "list"
	ExposeToolGet     = "get"
	ExposeToolListIDs = "list_ids"
)

// AllExposeTools is the canonical, deterministic order of expose options.
// Used by validation and for default ordering on persistence.
var AllExposeTools = []string{ExposeToolList, ExposeToolGet, ExposeToolListIDs}

// KGEntitySchema is the customer-defined JSON Schema (Draft 2020-12 + x-*
// annotations) for one entity type inside a bundle. The schema drives:
//   - validation of entity instances at import / mutation time
//   - auto-generation of MCP tools (list_X, get_X, list_X_ids) exposed to agents
//   - filter parameter shape (one per x-index property)
//
// SchemaHash is a deterministic identifier (sha256 of the canonicalised schema
// JSON) used for change detection and to pin entity rows to a specific schema
// version.
type KGEntitySchema struct {
	TenantID        string
	BundleName      string
	EntityType      string
	SchemaJSON      []byte
	SchemaHash      string
	IDField         string
	ExposeTools     []string
	ToolDescription string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// NewKGEntitySchema constructs a KGEntitySchema with field validation and
// auto-computes SchemaHash from SchemaJSON. Returns an error describing the
// first invalid field if construction fails.
//
// schemaJSON must already be canonicalised (sorted keys) by the caller so
// hash is stable across re-imports.
func NewKGEntitySchema(
	tenantID, bundleName, entityType string,
	schemaJSON []byte,
	idField string,
	exposeTools []string,
	toolDescription string,
) (*KGEntitySchema, error) {
	if len(exposeTools) == 0 {
		exposeTools = []string{ExposeToolList, ExposeToolGet}
	}
	s := &KGEntitySchema{
		TenantID:        tenantID,
		BundleName:      bundleName,
		EntityType:      entityType,
		SchemaJSON:      schemaJSON,
		SchemaHash:      computeSchemaHash(schemaJSON),
		IDField:         idField,
		ExposeTools:     exposeTools,
		ToolDescription: toolDescription,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return s, nil
}

// Validate checks all KGEntitySchema invariants. Called by constructors and
// the apply usecase before any state mutation.
func (s *KGEntitySchema) Validate() error {
	if s.TenantID == "" {
		return fmt.Errorf("kg_entity_schema.tenant_id is required")
	}
	if !ValidKGBundleName(s.BundleName) {
		return fmt.Errorf("kg_entity_schema.bundle_name %q invalid", s.BundleName)
	}
	if !ValidKGEntityType(s.EntityType) {
		return fmt.Errorf("kg_entity_schema.entity_type %q invalid: must match %s", s.EntityType, kgEntityTypeRegex)
	}
	if len(s.SchemaJSON) == 0 {
		return fmt.Errorf("kg_entity_schema.schema_json is required")
	}
	if s.SchemaHash == "" {
		return fmt.Errorf("kg_entity_schema.schema_hash is required")
	}
	if s.IDField == "" {
		return fmt.Errorf("kg_entity_schema.id_field is required (from x-id-field annotation)")
	}
	if len(s.ExposeTools) == 0 {
		return fmt.Errorf("kg_entity_schema.expose_tools must not be empty")
	}
	for _, t := range s.ExposeTools {
		if !slices.Contains(AllExposeTools, t) {
			return fmt.Errorf("kg_entity_schema.expose_tools contains unknown value %q (allowed: %v)", t, AllExposeTools)
		}
	}
	return nil
}

// ShouldExpose reports whether the named tool should be auto-generated for
// this entity schema. Callers pass one of the ExposeTool* constants; the
// method is the single source of truth for tool generation decisions.
func (s *KGEntitySchema) ShouldExpose(tool string) bool {
	return slices.Contains(s.ExposeTools, tool)
}

// ToolNames returns the auto-generated MCP tool names exposed by this schema,
// in deterministic order. Used by the registry on apply and by the architectural
// collision detector to find conflicts with existing capability or MCP tools.
func (s *KGEntitySchema) ToolNames() []string {
	out := make([]string, 0, len(s.ExposeTools))
	if s.ShouldExpose(ExposeToolList) {
		out = append(out, "list_"+s.EntityType)
	}
	if s.ShouldExpose(ExposeToolGet) {
		out = append(out, "get_"+s.EntityType)
	}
	if s.ShouldExpose(ExposeToolListIDs) {
		out = append(out, "list_"+s.EntityType+"_ids")
	}
	return out
}

// ValidKGEntityType reports whether s is a syntactically valid KG entity type
// identifier. Exported so import-side validators use the same rule.
func ValidKGEntityType(s string) bool {
	if len(s) < 2 || len(s) > 64 {
		return false
	}
	return kgEntityTypeRegex.MatchString(s)
}

// computeSchemaHash returns the SHA-256 hash of the schema JSON as a
// lower-case hex string. Used for schema versioning and entity-schema
// pinning.
func computeSchemaHash(schemaJSON []byte) string {
	sum := sha256.Sum256(schemaJSON)
	return hex.EncodeToString(sum[:])
}
