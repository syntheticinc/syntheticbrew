package kgread

import (
	"fmt"
	"sort"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
	"github.com/syntheticinc/syntheticbrew/pkg/jsonschema"
)

// indexedFieldsFromSchema extracts the x-index property names from a domain
// KGEntitySchema by re-parsing its SchemaJSON. The result is a set keyed by
// property name for O(1) whitelist checks.
//
// Re-parsing on every list-with-filter call is acceptable: it's O(properties)
// over a single document. If profiling shows it dominates, the result can be
// cached on a future KGEntitySchema field (out of scope here — schema parsing
// is not in the hot path of agent tool execution).
func indexedFieldsFromSchema(schema *domain.KGEntitySchema) (map[string]struct{}, error) {
	ann, err := jsonschema.ParseAnnotations(schema.SchemaJSON)
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(ann.IndexedFields))
	for _, f := range ann.IndexedFields {
		out[f] = struct{}{}
	}
	return out, nil
}

// filterableFieldsFromSchema returns the indexed-field whitelist plus type
// info for every property in the schema. The indexed set drives the
// "filter field must be x-index" rule; the types map drives the
// "range operators require numeric/date" rule.
func filterableFieldsFromSchema(schema *domain.KGEntitySchema) (map[string]struct{}, map[string]jsonschema.FieldTypeSpec, error) {
	ann, err := jsonschema.ParseAnnotations(schema.SchemaJSON)
	if err != nil {
		return nil, nil, err
	}
	indexed := make(map[string]struct{}, len(ann.IndexedFields))
	for _, f := range ann.IndexedFields {
		indexed[f] = struct{}{}
	}
	return indexed, ann.FieldTypes, nil
}

// annotationsFromSchema is a convenience wrapper around jsonschema.ParseAnnotations
// used by sort + summary projection code paths that need EnumValues or
// SummaryFields. Re-parses SchemaJSON each call.
func annotationsFromSchema(schema *domain.KGEntitySchema) (*jsonschema.Annotations, error) {
	return jsonschema.ParseAnnotations(schema.SchemaJSON)
}

// validateFilterSpecs walks the requested filter map and rejects:
//   - non-indexed field with any operator
//   - range operator (gte/gt/lte/lt) on a non-numeric, non-date field
//   - empty IN array
//   - multiple operators on the same field (parser layer should have caught
//     this; usecase double-checks as defence in depth)
//
// Side effect: when validation passes for a range filter on a date /
// date-time field, sets the spec's CastExpr to "timestamptz" so the repo
// emits the right SQL cast. Mutates the map in place so the enriched
// specs flow downstream.
func validateFilterSpecs(
	filters map[string]FilterSpec,
	indexed map[string]struct{},
	types map[string]jsonschema.FieldTypeSpec,
) error {
	for name, spec := range filters {
		if _, ok := indexed[name]; !ok {
			return pkgerrors.InvalidInput(fmt.Sprintf("filter field %q is not indexed (allowed: %v)", name, sortedKeys(indexed)))
		}
		if len(spec.In) == 0 && spec.Eq == nil && !spec.IsRange() {
			return pkgerrors.InvalidInput(fmt.Sprintf("filter %q has no value or operator", name))
		}
		// Reject multiple operator families on the same field. Eq + range or
		// Eq + In is ambiguous.
		families := 0
		if spec.Eq != nil {
			families++
		}
		if len(spec.In) > 0 {
			families++
		}
		if spec.IsRange() {
			families++
		}
		if families > 1 {
			return pkgerrors.InvalidInput(fmt.Sprintf("filter %q mixes equality / in / range operators — choose one", name))
		}
		if spec.IsRange() {
			ftype, ok := types[name]
			if !ok || !ftype.IsRangeFilterable() {
				return pkgerrors.InvalidInput(fmt.Sprintf("range operators not supported on field %q (type=%q, format=%q); only numeric and date/date-time fields allow gte/gt/lte/lt", name, ftype.Type, ftype.Format))
			}
			// Enrich the spec with the SQL cast the repo needs. Numeric
			// fields default to "" (= ::numeric in the repo); date / date-time
			// strings cast to ::timestamptz so input like "2026-01-01" works.
			if ftype.Type == "string" && (ftype.Format == "date" || ftype.Format == "date-time") {
				spec.CastExpr = "timestamptz"
				filters[name] = spec
			}
		}
		// KG14-SEC-04: an unbounded IN list is a single-query DoS vector
		// (each value extends the seq-scan predicate evaluation). Cap to
		// match MaxBatchGetIDs so the two paths share the same upper bound.
		if len(spec.In) > MaxFilterInSize {
			return pkgerrors.InvalidInput(fmt.Sprintf("filter %q in-list size %d exceeds max %d", name, len(spec.In), MaxFilterInSize))
		}
	}
	return nil
}

// validateSortSpecs rejects sort entries that reference non-indexed fields,
// use an invalid order value, duplicate a field, or appear in an empty array.
// Defence in depth: caller (HTTP / tool args parser) should already produce
// well-formed specs, but the usecase enforces the contract before reaching
// the repo layer.
func validateSortSpecs(specs []SortSpec, indexed map[string]struct{}) error {
	if len(specs) == 0 {
		return pkgerrors.InvalidInput("sort array must contain at least one entry when provided")
	}
	seen := make(map[string]struct{}, len(specs))
	for _, s := range specs {
		if s.Field == "" {
			return pkgerrors.InvalidInput("sort entry has empty field")
		}
		if _, ok := indexed[s.Field]; !ok {
			return pkgerrors.InvalidInput(fmt.Sprintf("sort field %q must be indexed (x-index: true); allowed: %v", s.Field, sortedKeys(indexed)))
		}
		switch s.Order {
		case SortOrderAsc, SortOrderDesc:
		default:
			return pkgerrors.InvalidInput(fmt.Sprintf("sort entry for %q has invalid order %q (must be \"asc\" or \"desc\")", s.Field, s.Order))
		}
		if _, dup := seen[s.Field]; dup {
			return pkgerrors.InvalidInput(fmt.Sprintf("sort field %q appears more than once", s.Field))
		}
		seen[s.Field] = struct{}{}
	}
	return nil
}

// sortedKeys returns the deterministic ascending key list of a set. Used to
// produce stable error messages listing allowed values.
func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
