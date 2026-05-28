package domain_test

import (
	"strings"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

func TestNewKGEntitySchema_Valid(t *testing.T) {
	t.Parallel()

	schemaJSON := []byte(`{"$id":"category","type":"object","x-id-field":"code"}`)
	s, err := domain.NewKGEntitySchema(
		"tenant-1", "chirp-iot", "category", schemaJSON,
		"code", nil, "An category vertical in the taxonomy.",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.SchemaHash == "" || len(s.SchemaHash) != 64 {
		t.Errorf("SchemaHash must be 64-char sha256 hex, got %q", s.SchemaHash)
	}
	// Default expose tools = ["list", "get"]
	if len(s.ExposeTools) != 2 ||
		s.ExposeTools[0] != domain.ExposeToolList ||
		s.ExposeTools[1] != domain.ExposeToolGet {
		t.Errorf("default ExposeTools: got %v, want [list, get]", s.ExposeTools)
	}
}

func TestNewKGEntitySchema_DefaultExposeWhenNil(t *testing.T) {
	t.Parallel()

	s, err := domain.NewKGEntitySchema(
		"t", "bundle", "category", []byte("{}"), "code", nil, "",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.ExposeTools) != 2 {
		t.Errorf("default ExposeTools should have 2 entries, got %d", len(s.ExposeTools))
	}
}

func TestNewKGEntitySchema_AcceptsListIDs(t *testing.T) {
	t.Parallel()

	s, err := domain.NewKGEntitySchema(
		"t", "bundle", "category", []byte("{}"), "code",
		[]string{"list", "get", "list_ids"}, "",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s.ShouldExpose("list_ids") {
		t.Error("ShouldExpose(list_ids) must return true")
	}
}

func TestNewKGEntitySchema_RejectsUnknownExposeTool(t *testing.T) {
	t.Parallel()

	_, err := domain.NewKGEntitySchema(
		"t", "bundle", "category", []byte("{}"), "code",
		[]string{"list", "evil_tool"}, "",
	)
	if err == nil {
		t.Fatal("expected error for unknown expose tool")
	}
	if !strings.Contains(err.Error(), "evil_tool") {
		t.Errorf("error must name the bad tool: %v", err)
	}
}

func TestNewKGEntitySchema_ValidationFailures(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		tenantID   string
		bundleName string
		entityType string
		schemaJSON []byte
		idField    string
		want       string
	}{
		{"empty tenant", "", "bundle", "category", []byte("{}"), "id", "tenant_id"},
		{"bad bundle", "t", "Bad-Name", "category", []byte("{}"), "id", "bundle_name"},
		{"bad entity type uppercase", "t", "bundle", "Category", []byte("{}"), "id", "entity_type"},
		{"bad entity type hyphen", "t", "bundle", "category-name", []byte("{}"), "id", "entity_type"},
		{"empty schema", "t", "bundle", "category", []byte{}, "id", "schema_json"},
		{"empty id_field", "t", "bundle", "category", []byte("{}"), "", "id_field"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := domain.NewKGEntitySchema(
				tc.tenantID, tc.bundleName, tc.entityType, tc.schemaJSON,
				tc.idField, nil, "",
			)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestKGEntitySchema_ShouldExpose(t *testing.T) {
	t.Parallel()

	s, err := domain.NewKGEntitySchema(
		"t", "bundle", "category", []byte("{}"), "id",
		[]string{"list", "get"}, "",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s.ShouldExpose("list") {
		t.Error("ShouldExpose(list) must be true")
	}
	if !s.ShouldExpose("get") {
		t.Error("ShouldExpose(get) must be true")
	}
	if s.ShouldExpose("list_ids") {
		t.Error("ShouldExpose(list_ids) must be false (not in defaults)")
	}
	if s.ShouldExpose("nonsense") {
		t.Error("ShouldExpose(nonsense) must be false")
	}
}

func TestKGEntitySchema_ToolNames(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		entityType  string
		exposeTools []string
		want        []string
	}{
		{
			name:        "default list+get",
			entityType:  "category",
			exposeTools: []string{"list", "get"},
			want:        []string{"list_category", "get_category"},
		},
		{
			name:        "list+get+list_ids",
			entityType:  "product_attribute",
			exposeTools: []string{"list", "get", "list_ids"},
			want:        []string{"list_product_attribute", "get_product_attribute", "list_product_attribute_ids"},
		},
		{
			name:        "only get",
			entityType:  "category",
			exposeTools: []string{"get"},
			want:        []string{"get_category"},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, err := domain.NewKGEntitySchema(
				"t", "bundle", tc.entityType, []byte("{}"), "id",
				tc.exposeTools, "",
			)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := s.ToolNames()
			if len(got) != len(tc.want) {
				t.Fatalf("ToolNames length: got %d (%v), want %d (%v)", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("ToolNames[%d]: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestValidKGEntityType(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		input string
		want  bool
	}{
		{"category", true},
		{"product_attribute", true},
		{"sensor_master_v2", true},
		{"a_b", true},
		{"a", false},                     // too short
		{"", false},                      // empty
		{"_x", false},                    // starts underscore
		{"x_", false},                    // ends underscore
		{"Category", false},              // uppercase
		{"category-name", false},         // hyphen
		{"1industry", false},             // starts digit
		{strings.Repeat("a", 65), false}, // too long
	} {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			if got := domain.ValidKGEntityType(tc.input); got != tc.want {
				t.Errorf("ValidKGEntityType(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestKGEntitySchema_HashStable(t *testing.T) {
	t.Parallel()

	schema := []byte(`{"$id":"category","type":"object","x-id-field":"code"}`)
	s1, err := domain.NewKGEntitySchema("t", "bundle", "category", schema, "code", nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s2, _ := domain.NewKGEntitySchema("t", "bundle", "category", schema, "code", nil, "")
	if s1.SchemaHash != s2.SchemaHash {
		t.Errorf("schema hash unstable: %q vs %q", s1.SchemaHash, s2.SchemaHash)
	}

	differentSchema := []byte(`{"$id":"category","type":"object","x-id-field":"slug"}`)
	s3, _ := domain.NewKGEntitySchema("t", "bundle", "category", differentSchema, "slug", nil, "")
	if s1.SchemaHash == s3.SchemaHash {
		t.Error("different schemas must produce different hashes")
	}
}
