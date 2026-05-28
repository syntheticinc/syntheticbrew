// Package jsonschema parses customer-supplied JSON Schema documents and
// extracts the SyntheticBrew x-* annotations used by the Knowledge Graphs
// feature.
//
// This package is deliberately decoupled from the domain layer: it returns
// plain DTO structs that the usecase layer maps onto domain entities. No
// imports from internal/domain — preserves the Clean Architecture rule
// "infrastructure detail does not leak into domain".
package jsonschema

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Annotations is the parsed set of SyntheticBrew x-* annotations found in a
// customer-supplied JSON Schema document. Usecases map this DTO onto domain
// entities (KGEntitySchema, etc.) and use the typed information to drive
// validation, indexing, and auto-tool generation.
type Annotations struct {
	// IDField is the value of `x-id-field` on the schema root. Required:
	// schemas without this annotation are rejected by ParseAnnotations.
	IDField string

	// ExposeTools is the value of `x-tool-expose` on the schema root.
	// Defaults to ["list", "get"] when absent. Each entry must be one of
	// the canonical values ("list", "get", "list_ids"); unknown values are
	// rejected.
	ExposeTools []string

	// ToolDescription is the value of `x-tool-description` on the schema
	// root, used to override the auto-generated tool description text.
	// Empty if absent — caller falls back to the schema's `description`.
	ToolDescription string

	// IndexedFields lists the property names marked `x-index: true`. Order
	// is deterministic (alphabetical) so the auto-derived filter sub-schema
	// has a stable shape across re-imports.
	IndexedFields []string

	// Refs lists every property annotated with `x-ref`. Each entry records
	// the property name, the target entity type, and (optionally) the
	// target field name (default = target's x-id-field).
	Refs []RefAnnotation

	// DerivedFields lists property names marked `x-derived: true`. These
	// fields are computed by the engine, not authored by customers, and
	// are excluded from filter parameters on `list_*` auto-tools.
	DerivedFields []string

	// ContentTypes maps property name → x-content-type value. Used by the
	// admin UI to render markdown / code / URL fields appropriately. The
	// engine ignores this map; it exists purely as a UI hint.
	ContentTypes map[string]string

	// SummaryFields lists the property names in the top-level `x-summary-fields`
	// annotation. When non-empty, the auto-generated `list_<entity>_ids` tool
	// returns `{items: [{id_field, ...summary_fields}], total}` instead of the
	// default `{ids: [...], total}`. Default (nil/empty) preserves bare-ids
	// shape for backward compat with 1.3.x bundles. ID field is auto-included
	// — never duplicated here. Top-level properties only (no dot-notation).
	SummaryFields []string

	// FieldTypes maps property name → its JSON Schema type + format (e.g.
	// {"popularity_score": {Type: "integer"}}, {"created_at": {Type: "string",
	// Format: "date-time"}}). Populated for every property in the schema, not
	// just indexed ones. Used by the filter parser to decide whether range
	// operators (gte/gt/lte/lt) are valid on a field — only allowed on
	// numeric (integer/number) or date/date-time properties.
	FieldTypes map[string]FieldTypeSpec

	// EnumValues maps property name → declared enum values in document order.
	// Empty if the property is not an enum or has no `enum:` keyword. Used by
	// the sort builder to produce `ORDER BY array_position(ARRAY['v1','v2'],
	// data->>'field')` — sort order follows declaration order, NOT
	// alphabetical (the natural assumption that bites users on enum sorts).
	EnumValues map[string][]string
}

// FieldTypeSpec describes a property's JSON Schema type + format pair. Used
// by filter validation to gate range operators to numeric/date fields.
type FieldTypeSpec struct {
	// Type is the JSON Schema type keyword ("string", "integer", "number",
	// "boolean", "array", "object"). Empty if absent in source.
	Type string

	// Format is the JSON Schema format keyword (e.g. "date", "date-time",
	// "email"). Empty if absent. Only "date" and "date-time" affect filter
	// operator validation — both enable range operators on string-typed fields.
	Format string
}

// IsRangeFilterable reports whether a property can be filtered with range
// operators (gte/gt/lte/lt). True for numeric types and for strings annotated
// as date / date-time format. Centralised so handler + repo + tool builder
// agree on the rule.
func (s FieldTypeSpec) IsRangeFilterable() bool {
	switch s.Type {
	case "integer", "number":
		return true
	case "string":
		return s.Format == "date" || s.Format == "date-time"
	}
	return false
}

// RefAnnotation describes a cross-reference from one property to another
// entity type. Cross-refs are validated at import time (every referenced
// entity must exist in the same bundle or in the existing DB state) and
// surfaced in the admin UI as clickable navigation links.
type RefAnnotation struct {
	// Property is the name of the property on the current schema that
	// holds the reference value (or list of values for array properties).
	Property string

	// TargetType is the value of `x-ref` — the entity_type being referenced.
	TargetType string

	// TargetField is the field of the target entity to match against.
	// Empty string means "match against the target's x-id-field" (default).
	TargetField string
}

// ParseAnnotations reads a JSON Schema document (Draft 2020-12 + x-*
// annotations) and returns the SyntheticBrew-relevant annotations. Returns
// a typed error describing the first problem found.
//
// The function does NOT perform full JSON Schema validation against the
// meta-schema; that is delegated to the usecase layer's SchemaValidator
// (which wraps santhosh-tekuri/jsonschema for full Draft 2020-12 checks).
// ParseAnnotations only extracts the x-* extension data needed for
// SyntheticBrew-specific logic.
func ParseAnnotations(schemaJSON []byte) (*Annotations, error) {
	if len(schemaJSON) == 0 {
		return nil, fmt.Errorf("schema is empty")
	}

	var root rawSchema
	if err := json.Unmarshal(schemaJSON, &root); err != nil {
		return nil, fmt.Errorf("parse schema json: %w", err)
	}

	ann := &Annotations{
		ContentTypes: make(map[string]string),
		FieldTypes:   make(map[string]FieldTypeSpec),
		EnumValues:   make(map[string][]string),
	}

	// x-id-field (required).
	if root.XIDField == "" {
		return nil, fmt.Errorf("schema missing required annotation x-id-field")
	}
	ann.IDField = root.XIDField

	// x-id-field must reference an existing property.
	if _, ok := root.Properties[root.XIDField]; !ok {
		return nil, fmt.Errorf("x-id-field %q does not reference an existing property", root.XIDField)
	}

	// x-tool-expose (optional, default [list, get]).
	if root.XToolExpose == nil {
		ann.ExposeTools = []string{"list", "get"}
	} else {
		for _, t := range root.XToolExpose {
			if !isCanonicalExposeTool(t) {
				return nil, fmt.Errorf("x-tool-expose contains unknown value %q (allowed: list, get, list_ids)", t)
			}
		}
		ann.ExposeTools = root.XToolExpose
	}

	// x-tool-description (optional).
	ann.ToolDescription = root.XToolDescription

	// Walk properties for x-index, x-ref, x-derived, x-content-type.
	// Sorted iteration so output is deterministic.
	propNames := make([]string, 0, len(root.Properties))
	for name := range root.Properties {
		propNames = append(propNames, name)
	}
	sort.Strings(propNames)

	for _, name := range propNames {
		prop := root.Properties[name]

		if prop.XIndex {
			ann.IndexedFields = append(ann.IndexedFields, name)
		}
		if prop.XDerived {
			ann.DerivedFields = append(ann.DerivedFields, name)
		}
		if prop.XContentType != "" {
			ann.ContentTypes[name] = prop.XContentType
		}
		if prop.XRef != "" {
			ann.Refs = append(ann.Refs, RefAnnotation{
				Property:    name,
				TargetType:  prop.XRef,
				TargetField: prop.XRefField, // empty = default to target x-id-field
			})
		}
		if prop.Type != "" || prop.Format != "" {
			ann.FieldTypes[name] = FieldTypeSpec{Type: prop.Type, Format: prop.Format}
		}
		if enums := stringEnumValues(prop.Enum); len(enums) > 0 {
			ann.EnumValues[name] = enums
		}
	}

	// x-summary-fields (optional, opt-in projection for list_<entity>_ids).
	// Empty / nil array == absent (bare-ids mode). Validation:
	//   - each entry must reference an existing property
	//   - no dot-notation (top-level properties only)
	//   - ID field is auto-included downstream; silently de-dup if specified
	//   - duplicates within the array are rejected (user typo signal)
	if len(root.XSummaryFields) > 0 {
		summary, err := normaliseSummaryFields(root.XSummaryFields, ann.IDField, root.Properties)
		if err != nil {
			return nil, err
		}
		ann.SummaryFields = summary
	}

	return ann, nil
}

// normaliseSummaryFields validates and de-duplicates the x-summary-fields
// annotation. The ID field is silently dropped (auto-included downstream);
// unknown properties / dot-notation / duplicates produce a typed error.
func normaliseSummaryFields(raw []string, idField string, props map[string]rawProperty) ([]string, error) {
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, name := range raw {
		if name == "" {
			return nil, fmt.Errorf("x-summary-fields contains empty entry")
		}
		if strings.Contains(name, ".") {
			return nil, fmt.Errorf("x-summary-fields entry %q uses dot-notation; only top-level properties are supported", name)
		}
		if name == idField {
			// Silent dedup — ID field is auto-included.
			continue
		}
		if _, ok := props[name]; !ok {
			return nil, fmt.Errorf("x-summary-fields references unknown property %q", name)
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("x-summary-fields lists property %q more than once", name)
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// stringEnumValues converts a raw JSON Schema `enum` array (any types) into
// a []string for sort by declaration-order. Non-string enum values (numbers,
// bools) are stringified via fmt.Sprintf("%v") — sort comparison happens via
// data->>'field' which is also stringified, so this matches semantics.
// Returns nil for empty or absent enum arrays.
func stringEnumValues(raw []any) []string {
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if v == nil {
			continue
		}
		switch t := v.(type) {
		case string:
			out = append(out, t)
		default:
			out = append(out, fmt.Sprintf("%v", t))
		}
	}
	return out
}

// FilterableFields returns the property names that should appear as filter
// parameters on the auto-generated `list_<entity_type>` tool. Indexed fields
// minus derived fields (derived fields cannot be filter inputs because they
// are engine-computed). Order is deterministic.
func (a *Annotations) FilterableFields() []string {
	derived := make(map[string]struct{}, len(a.DerivedFields))
	for _, d := range a.DerivedFields {
		derived[d] = struct{}{}
	}
	out := make([]string, 0, len(a.IndexedFields))
	for _, f := range a.IndexedFields {
		if _, ok := derived[f]; ok {
			continue
		}
		out = append(out, f)
	}
	return out
}

// rawSchema and rawProperty are the JSON shapes we parse directly. Anything
// not listed here is preserved as part of schema_json (raw JSON) but not
// surfaced through Annotations.
type rawSchema struct {
	XIDField         string                 `json:"x-id-field"`
	XToolExpose      []string               `json:"x-tool-expose,omitempty"`
	XToolDescription string                 `json:"x-tool-description,omitempty"`
	XSummaryFields   []string               `json:"x-summary-fields,omitempty"`
	Properties       map[string]rawProperty `json:"properties"`
}

type rawProperty struct {
	Type         string `json:"type,omitempty"`
	Format       string `json:"format,omitempty"`
	Enum         []any  `json:"enum,omitempty"`
	XIndex       bool   `json:"x-index,omitempty"`
	XRef         string `json:"x-ref,omitempty"`
	XRefField    string `json:"x-ref-field,omitempty"`
	XDerived     bool   `json:"x-derived,omitempty"`
	XContentType string `json:"x-content-type,omitempty"`
}

// isCanonicalExposeTool reports whether t is one of the three accepted
// x-tool-expose values. Centralised so callers (parser + domain validators)
// stay in sync.
func isCanonicalExposeTool(t string) bool {
	switch strings.TrimSpace(t) {
	case "list", "get", "list_ids":
		return true
	}
	return false
}
