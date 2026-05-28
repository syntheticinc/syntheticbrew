package kgread

import (
	"sort"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
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
