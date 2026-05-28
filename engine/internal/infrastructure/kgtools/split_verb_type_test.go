package kgtools

import "testing"

// splitVerbType is the central name-parser for KG tool dispatch. Tests pin
// the contract: list_<X>, get_<X>, list_<X>_ids are the only recognised
// patterns. Names that don't match return ("", "") so BuildTool can fall
// back to the next resolver.
//
// 1.4.0 closed a 1.3.0 gap — the prior splitVerbType only handled "list_"
// and "get_" prefixes; "list_X_ids" names (declared by domain.ToolNames)
// silently produced no tool because the suffix was not split out. The
// _ids cases below would have failed against 1.3.0 — they're the regression
// guards for that gap.
func TestSplitVerbType(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name           string
		input          string
		wantVerb       string
		wantEntityType string
	}{
		{"list happy", "list_category", "list", "category"},
		{"get happy", "get_brand", "get", "brand"},
		{"list_ids happy (1.4.0 new)", "list_use_case_ids", "list_ids", "use_case"},
		{"list_ids multi-underscore entity", "list_sensor_family_ids", "list_ids", "sensor_family"},
		{"list with embedded ids in middle (not suffix)", "list_ids_history", "list", "ids_history"},
		{"empty", "", "", ""},
		{"unknown prefix", "delete_thing", "", ""},
		{"only prefix", "list_", "list", ""},
		{"list_ids with empty entity body rejected", "list__ids", "", ""},
		{"get_ids is NOT a recognised pattern", "get_thing_ids", "get", "thing_ids"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			verb, et := splitVerbType(tc.input)
			if verb != tc.wantVerb {
				t.Errorf("verb: got %q, want %q", verb, tc.wantVerb)
			}
			if et != tc.wantEntityType {
				t.Errorf("entityType: got %q, want %q", et, tc.wantEntityType)
			}
		})
	}
}

// TestSummaryFieldsFromSchemaJSON — the bridging helper used by kgListIDsTool
// to decide between bare-ids and projection response shapes. Errors are
// swallowed (returns nil) per the production contract — apply already
// validated the schema; runtime parse errors fall back to safe bare-ids.
func TestSummaryFieldsFromSchemaJSON(t *testing.T) {
	t.Parallel()

	t.Run("absent annotation → nil", func(t *testing.T) {
		t.Parallel()
		got := summaryFieldsFromSchemaJSON([]byte(`{
			"x-id-field": "id",
			"properties": {"id": {"type": "string"}}
		}`))
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("present annotation → values", func(t *testing.T) {
		t.Parallel()
		got := summaryFieldsFromSchemaJSON([]byte(`{
			"x-id-field": "id",
			"x-summary-fields": ["title", "score"],
			"properties": {
				"id":    {"type": "string"},
				"title": {"type": "string"},
				"score": {"type": "integer"}
			}
		}`))
		if len(got) != 2 || got[0] != "title" || got[1] != "score" {
			t.Errorf("expected [title, score], got %v", got)
		}
	})

	t.Run("malformed JSON → nil (safe fallback)", func(t *testing.T) {
		t.Parallel()
		got := summaryFieldsFromSchemaJSON([]byte(`{not json`))
		if got != nil {
			t.Errorf("malformed should fall back to nil, got %v", got)
		}
	})
}
