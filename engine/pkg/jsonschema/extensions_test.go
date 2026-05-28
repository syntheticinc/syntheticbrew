package jsonschema_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/syntheticinc/syntheticbrew/pkg/jsonschema"
)

const validIndustrySchema = `{
  "$id": "category",
  "type": "object",
  "x-id-field": "code",
  "x-tool-expose": ["list", "get"],
  "x-tool-description": "Industries in the customer taxonomy.",
  "properties": {
    "code": {
      "type": "string",
      "x-index": true
    },
    "name": {
      "type": "string"
    },
    "slug": {
      "type": "string",
      "x-index": true
    },
    "popularity": {
      "type": "string",
      "x-index": true
    },
    "description": {
      "type": "string",
      "x-content-type": "markdown"
    },
    "use_case_count": {
      "type": "integer",
      "x-derived": true,
      "x-index": true
    }
  }
}`

func TestParseAnnotations_Valid(t *testing.T) {
	t.Parallel()

	ann, err := jsonschema.ParseAnnotations([]byte(validIndustrySchema))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ann.IDField != "code" {
		t.Errorf("IDField: got %q, want \"code\"", ann.IDField)
	}
	if !reflect.DeepEqual(ann.ExposeTools, []string{"list", "get"}) {
		t.Errorf("ExposeTools: got %v, want [list get]", ann.ExposeTools)
	}
	if ann.ToolDescription != "Industries in the customer taxonomy." {
		t.Errorf("ToolDescription: got %q", ann.ToolDescription)
	}

	wantIndexed := []string{"code", "popularity", "slug", "use_case_count"}
	if !reflect.DeepEqual(ann.IndexedFields, wantIndexed) {
		t.Errorf("IndexedFields: got %v, want %v", ann.IndexedFields, wantIndexed)
	}
	if !reflect.DeepEqual(ann.DerivedFields, []string{"use_case_count"}) {
		t.Errorf("DerivedFields: got %v, want [use_case_count]", ann.DerivedFields)
	}
	if ann.ContentTypes["description"] != "markdown" {
		t.Errorf("ContentTypes[description]: got %q, want \"markdown\"", ann.ContentTypes["description"])
	}
}

func TestParseAnnotations_FilterableFields(t *testing.T) {
	t.Parallel()

	ann, err := jsonschema.ParseAnnotations([]byte(validIndustrySchema))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// use_case_count is x-index + x-derived → must NOT appear as filterable.
	want := []string{"code", "popularity", "slug"}
	got := ann.FilterableFields()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FilterableFields: got %v, want %v (derived field must be excluded)", got, want)
	}
}

func TestParseAnnotations_DefaultExposeWhenAbsent(t *testing.T) {
	t.Parallel()

	schema := `{
		"x-id-field": "id",
		"properties": {"id": {"type": "string"}}
	}`
	ann, err := jsonschema.ParseAnnotations([]byte(schema))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(ann.ExposeTools, []string{"list", "get"}) {
		t.Errorf("default ExposeTools: got %v, want [list get]", ann.ExposeTools)
	}
}

func TestParseAnnotations_RefAnnotations(t *testing.T) {
	t.Parallel()

	schema := `{
		"x-id-field": "code",
		"properties": {
			"code": {"type": "string"},
			"category": {"type": "string", "x-ref": "category"},
			"sensor": {"type": "string", "x-ref": "brand", "x-ref-field": "slug"}
		}
	}`
	ann, err := jsonschema.ParseAnnotations([]byte(schema))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(ann.Refs) != 2 {
		t.Fatalf("expected 2 refs, got %d: %v", len(ann.Refs), ann.Refs)
	}

	// Refs sorted alphabetically by Property name.
	wantRefs := []jsonschema.RefAnnotation{
		{Property: "category", TargetType: "category", TargetField: ""},
		{Property: "sensor", TargetType: "brand", TargetField: "slug"},
	}
	if !reflect.DeepEqual(ann.Refs, wantRefs) {
		t.Errorf("Refs: got %v, want %v", ann.Refs, wantRefs)
	}
}

func TestParseAnnotations_Errors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		schema     string
		wantSubstr string
	}{
		{"empty input", ``, "empty"},
		{"invalid json", `{not valid`, "parse"},
		{
			name:       "missing x-id-field",
			schema:     `{"properties": {"id": {"type": "string"}}}`,
			wantSubstr: "x-id-field",
		},
		{
			name:       "x-id-field references missing property",
			schema:     `{"x-id-field": "ghost", "properties": {"real": {"type": "string"}}}`,
			wantSubstr: "does not reference",
		},
		{
			name:       "unknown expose tool",
			schema:     `{"x-id-field": "id", "x-tool-expose": ["evil"], "properties": {"id": {"type": "string"}}}`,
			wantSubstr: "evil",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := jsonschema.ParseAnnotations([]byte(tc.schema))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSubstr)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}

func TestParseAnnotations_EmptyExposeRejected(t *testing.T) {
	t.Parallel()

	// Empty array (not absent) must still be accepted as "no tools"? No —
	// our policy is that an explicit empty array means "expose none", which
	// would generate zero tools. We reject this at the parser level because
	// a schema with no exposed tools is semantically useless and likely a
	// customer error. Verify this contract.
	schema := `{
		"x-id-field": "id",
		"x-tool-expose": [],
		"properties": {"id": {"type": "string"}}
	}`
	ann, err := jsonschema.ParseAnnotations([]byte(schema))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Current contract: empty array → empty ExposeTools (we accept the literal).
	// Domain layer's KGEntitySchema.Validate() rejects empty ExposeTools, so
	// the apply usecase will surface this as an error before persistence.
	// Pin the parser's behaviour explicitly.
	if len(ann.ExposeTools) != 0 {
		t.Errorf("explicit empty array: got %v, want empty slice", ann.ExposeTools)
	}
}

const summaryFieldsSchema = `{
  "x-id-field": "code",
  "x-summary-fields": ["title", "popularity", "industry"],
  "properties": {
    "code":       {"type": "string", "x-index": true},
    "title":      {"type": "string"},
    "popularity": {"type": "string", "enum": ["very_high", "high", "normal", "low"], "x-index": true},
    "industry":   {"type": "string", "x-index": true},
    "score":      {"type": "integer", "x-index": true},
    "created_at": {"type": "string", "format": "date-time", "x-index": true}
  }
}`

func TestParseAnnotations_SummaryFields_HappyPath(t *testing.T) {
	t.Parallel()

	ann, err := jsonschema.ParseAnnotations([]byte(summaryFieldsSchema))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"title", "popularity", "industry"}
	if !reflect.DeepEqual(ann.SummaryFields, want) {
		t.Errorf("SummaryFields: got %v, want %v", ann.SummaryFields, want)
	}
}

func TestParseAnnotations_SummaryFields_AbsentReturnsNil(t *testing.T) {
	t.Parallel()

	ann, err := jsonschema.ParseAnnotations([]byte(validIndustrySchema))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ann.SummaryFields != nil {
		t.Errorf("SummaryFields absent: got %v, want nil", ann.SummaryFields)
	}
}

func TestParseAnnotations_SummaryFields_EmptyArrayEquivalentToAbsent(t *testing.T) {
	t.Parallel()

	schema := `{
		"x-id-field": "id",
		"x-summary-fields": [],
		"properties": {"id": {"type": "string"}}
	}`
	ann, err := jsonschema.ParseAnnotations([]byte(schema))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ann.SummaryFields != nil {
		t.Errorf("empty array: got %v, want nil (treated as absent)", ann.SummaryFields)
	}
}

func TestParseAnnotations_SummaryFields_IDFieldSilentlyDeduped(t *testing.T) {
	t.Parallel()

	schema := `{
		"x-id-field": "code",
		"x-summary-fields": ["code", "title"],
		"properties": {
			"code":  {"type": "string"},
			"title": {"type": "string"}
		}
	}`
	ann, err := jsonschema.ParseAnnotations([]byte(schema))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"title"}
	if !reflect.DeepEqual(ann.SummaryFields, want) {
		t.Errorf("ID field should be silently de-duped: got %v, want %v", ann.SummaryFields, want)
	}
}

func TestParseAnnotations_SummaryFields_ErrorCases(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		schema     string
		wantSubstr string
	}{
		{
			name: "unknown property",
			schema: `{
				"x-id-field": "id",
				"x-summary-fields": ["ghost"],
				"properties": {"id": {"type": "string"}}
			}`,
			wantSubstr: `unknown property "ghost"`,
		},
		{
			name: "dot notation rejected",
			schema: `{
				"x-id-field": "id",
				"x-summary-fields": ["address.city"],
				"properties": {"id": {"type": "string"}, "address": {"type": "object"}}
			}`,
			wantSubstr: "dot-notation",
		},
		{
			name: "empty entry rejected",
			schema: `{
				"x-id-field": "id",
				"x-summary-fields": [""],
				"properties": {"id": {"type": "string"}}
			}`,
			wantSubstr: "empty entry",
		},
		{
			name: "duplicate entry rejected",
			schema: `{
				"x-id-field": "id",
				"x-summary-fields": ["title", "title"],
				"properties": {"id": {"type": "string"}, "title": {"type": "string"}}
			}`,
			wantSubstr: "more than once",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := jsonschema.ParseAnnotations([]byte(tc.schema))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSubstr)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}

func TestParseAnnotations_FieldTypes_Populated(t *testing.T) {
	t.Parallel()

	ann, err := jsonschema.ParseAnnotations([]byte(summaryFieldsSchema))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := ann.FieldTypes["score"]; got.Type != "integer" {
		t.Errorf("score type: got %q, want \"integer\"", got.Type)
	}
	if got := ann.FieldTypes["created_at"]; got.Type != "string" || got.Format != "date-time" {
		t.Errorf("created_at: got %+v, want {string, date-time}", got)
	}
	if got := ann.FieldTypes["title"]; got.Type != "string" || got.Format != "" {
		t.Errorf("title: got %+v, want {string, \"\"}", got)
	}
}

func TestFieldTypeSpec_IsRangeFilterable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		typ, format string
		want        bool
	}{
		{"integer", "", true},
		{"number", "", true},
		{"string", "date", true},
		{"string", "date-time", true},
		{"string", "", false},
		{"string", "email", false},
		{"boolean", "", false},
		{"array", "", false},
		{"object", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got := jsonschema.FieldTypeSpec{Type: tc.typ, Format: tc.format}.IsRangeFilterable()
		if got != tc.want {
			t.Errorf("IsRangeFilterable(%q,%q): got %v, want %v", tc.typ, tc.format, got, tc.want)
		}
	}
}

func TestParseAnnotations_EnumValues_DeclarationOrder(t *testing.T) {
	t.Parallel()

	ann, err := jsonschema.ParseAnnotations([]byte(summaryFieldsSchema))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// CRITICAL: enum values must preserve declaration order — sort by
	// declaration order is the whole point. Alphabetical sort would break it.
	wantEnum := []string{"very_high", "high", "normal", "low"}
	got := ann.EnumValues["popularity"]
	if !reflect.DeepEqual(got, wantEnum) {
		t.Errorf("EnumValues[popularity] must preserve declaration order: got %v, want %v (NOT alphabetical)", got, wantEnum)
	}

	// Non-enum field should have no entry.
	if _, ok := ann.EnumValues["title"]; ok {
		t.Errorf("EnumValues[title]: non-enum field should not be in map")
	}
}

func TestParseAnnotations_EnumValues_MixedTypes(t *testing.T) {
	t.Parallel()

	schema := `{
		"x-id-field": "id",
		"properties": {
			"id": {"type": "string"},
			"level": {"type": "integer", "enum": [1, 2, 3]}
		}
	}`
	ann, err := jsonschema.ParseAnnotations([]byte(schema))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Integer enum values stringified — comparison happens against data->>'level'
	// which is also stringified at the SQL layer, so semantics match.
	wantEnum := []string{"1", "2", "3"}
	if got := ann.EnumValues["level"]; !reflect.DeepEqual(got, wantEnum) {
		t.Errorf("EnumValues[level] integer enum: got %v, want %v", got, wantEnum)
	}
}
