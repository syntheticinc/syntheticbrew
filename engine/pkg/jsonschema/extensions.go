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
	}

	return ann, nil
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
	Properties       map[string]rawProperty `json:"properties"`
}

type rawProperty struct {
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
