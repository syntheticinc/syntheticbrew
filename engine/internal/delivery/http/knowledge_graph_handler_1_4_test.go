package http

import (
	"reflect"
	"strings"
	"testing"
)

// --- parseFilterQuery 1.4.0 operator-bag tests ---

func TestParseFilterQuery_BareEquality_BackwardCompat(t *testing.T) {
	t.Parallel()

	got := parseFilterQuery(map[string][]string{
		"filter[industry]": {"PM"},
	})
	want := map[string]any{"industry": "PM"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bare equality: got %v, want %v", got, want)
	}
}

func TestParseFilterQuery_InOperator(t *testing.T) {
	t.Parallel()

	got := parseFilterQuery(map[string][]string{
		"filter[industry][in]": {"PM,FB,RT"},
	})
	wantBag := map[string]any{"in": []any{"PM", "FB", "RT"}}
	if !reflect.DeepEqual(got["industry"], wantBag) {
		t.Errorf("in operator: got %v, want %v", got["industry"], wantBag)
	}
}

func TestParseFilterQuery_RangeOperators_Combined(t *testing.T) {
	t.Parallel()

	got := parseFilterQuery(map[string][]string{
		"filter[score][gte]": {"70"},
		"filter[score][lte]": {"95"},
	})
	wantBag := map[string]any{"gte": "70", "lte": "95"}
	if !reflect.DeepEqual(got["score"], wantBag) {
		t.Errorf("combined range: got %v, want %v", got["score"], wantBag)
	}
}

func TestParseFilterQuery_AllRangeOperators(t *testing.T) {
	t.Parallel()

	got := parseFilterQuery(map[string][]string{
		"filter[a][gte]": {"1"},
		"filter[a][gt]":  {"2"},
		"filter[a][lte]": {"3"},
		"filter[a][lt]":  {"4"},
	})
	want := map[string]any{"gte": "1", "gt": "2", "lte": "3", "lt": "4"}
	if !reflect.DeepEqual(got["a"], want) {
		t.Errorf("all range operators: got %v, want %v", got["a"], want)
	}
}

func TestParseFilterQuery_UnknownOperator_SilentlyIgnored(t *testing.T) {
	t.Parallel()

	// Unknown operators are silently dropped — the service layer surfaces a
	// friendlier error via filter validation (non-indexed / unknown shape).
	got := parseFilterQuery(map[string][]string{
		"filter[score][bogus]": {"X"},
	})
	if _, exists := got["score"]; exists {
		t.Errorf("unknown operator should produce no entry, got %v", got)
	}
}

func TestParseFilterQuery_EmptyFieldName_Ignored(t *testing.T) {
	t.Parallel()

	got := parseFilterQuery(map[string][]string{
		"filter[]":     {"x"},
		"filter[][in]": {"a,b"},
		"filter":       {"x"},
		"notfilter[x]": {"y"},
	})
	if len(got) != 0 {
		t.Errorf("invalid keys should produce empty map, got %v", got)
	}
}

func TestParseFilterQuery_MultipleFields(t *testing.T) {
	t.Parallel()

	got := parseFilterQuery(map[string][]string{
		"filter[industry]":   {"PM"},
		"filter[score][gte]": {"70"},
		"filter[status][in]": {"approved,pending"},
	})
	if got["industry"] != "PM" {
		t.Errorf("industry equality: got %v", got["industry"])
	}
	if bag, ok := got["score"].(map[string]any); !ok || bag["gte"] != "70" {
		t.Errorf("score range: got %v", got["score"])
	}
	if bag, ok := got["status"].(map[string]any); !ok || !reflect.DeepEqual(bag["in"], []any{"approved", "pending"}) {
		t.Errorf("status in: got %v", got["status"])
	}
}

// --- parseSortQuery tests ---

func TestParseSortQuery_Empty(t *testing.T) {
	t.Parallel()

	got, err := parseSortQuery("")
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if got != nil {
		t.Errorf("empty should produce nil, got %v", got)
	}
}

func TestParseSortQuery_SingleField(t *testing.T) {
	t.Parallel()

	got, err := parseSortQuery("popularity:desc")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	want := []KGSortParam{{Field: "popularity", Order: "desc"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseSortQuery_MultiField(t *testing.T) {
	t.Parallel()

	got, err := parseSortQuery("popularity:desc,code:asc")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	want := []KGSortParam{
		{Field: "popularity", Order: "desc"},
		{Field: "code", Order: "asc"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseSortQuery_CaseInsensitiveOrder(t *testing.T) {
	t.Parallel()

	got, err := parseSortQuery("score:DESC")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got[0].Order != "desc" {
		t.Errorf("order normalised to lower: got %q", got[0].Order)
	}
}

func TestParseSortQuery_InvalidOrder(t *testing.T) {
	t.Parallel()

	_, err := parseSortQuery("score:bogus")
	if err == nil || !strings.Contains(err.Error(), "asc or desc") {
		t.Errorf("expected invalid order error, got %v", err)
	}
}

func TestParseSortQuery_MissingOrder(t *testing.T) {
	t.Parallel()

	_, err := parseSortQuery("score:")
	if err == nil {
		t.Fatalf("expected error on missing order")
	}
}

func TestParseSortQuery_MissingField(t *testing.T) {
	t.Parallel()

	_, err := parseSortQuery(":desc")
	if err == nil {
		t.Fatalf("expected error on missing field")
	}
}

func TestParseSortQuery_NoColon(t *testing.T) {
	t.Parallel()

	_, err := parseSortQuery("score")
	if err == nil {
		t.Fatalf("expected error on missing colon")
	}
}

func TestParseSortQuery_EmptyEntryInMiddle(t *testing.T) {
	t.Parallel()

	_, err := parseSortQuery("a:asc,,b:desc")
	if err == nil {
		t.Fatalf("expected error on empty middle entry")
	}
}

// KG14-SEC-08: query string size DoS guard
func TestParseSortQuery_OversizeRejected(t *testing.T) {
	t.Parallel()

	// 3KB sort query — should reject before splitting and burning memory.
	huge := strings.Repeat("a:asc,", 600) + "a:asc"
	_, err := parseSortQuery(huge)
	if err == nil || !strings.Contains(err.Error(), "too long") {
		t.Errorf("expected size-cap error, got %v", err)
	}
}
