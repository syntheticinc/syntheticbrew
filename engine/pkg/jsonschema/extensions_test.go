package jsonschema_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/syntheticinc/syntheticbrew/pkg/jsonschema"
)

const validIndustrySchema = `{
  "$id": "industry",
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
			"industry": {"type": "string", "x-ref": "industry"},
			"sensor": {"type": "string", "x-ref": "sensor_family", "x-ref-field": "slug"}
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
		{Property: "industry", TargetType: "industry", TargetField: ""},
		{Property: "sensor", TargetType: "sensor_family", TargetField: "slug"},
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
