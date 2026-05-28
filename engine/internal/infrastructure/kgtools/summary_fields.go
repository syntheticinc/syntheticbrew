package kgtools

import (
	"github.com/syntheticinc/syntheticbrew/pkg/jsonschema"
)

// summaryFieldsFromSchemaJSON returns the x-summary-fields annotation from
// a raw schema JSON document, or nil if the annotation is absent / parsing
// fails. The tool layer uses this to decide between bare-ids and projection
// response shapes for list_<entity>_ids.
//
// Errors are swallowed (returns nil) because a parse error here should not
// fail the agent tool call — bare-ids is the safe fallback. Schema apply
// already validated the schema at write time; runtime parse errors are
// either a transient issue or schema corruption, both better surfaced via
// the apply pass than via opaque tool errors.
func summaryFieldsFromSchemaJSON(schemaJSON []byte) []string {
	ann, err := jsonschema.ParseAnnotations(schemaJSON)
	if err != nil || ann == nil {
		return nil
	}
	return ann.SummaryFields
}
