package domain_test

import (
	"strings"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

func TestNewKGEntity_Valid(t *testing.T) {
	t.Parallel()

	e, err := domain.NewKGEntity(
		"tenant-1", "chirp-iot", "industry", "PM",
		[]byte(`{"code":"PM","name":"Property Management"}`),
		"abc123",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.EntityID != "PM" {
		t.Errorf("EntityID: got %q, want %q", e.EntityID, "PM")
	}
}

func TestNewKGEntity_ValidationFailures(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		tenantID   string
		bundleName string
		entityType string
		entityID   string
		data       []byte
		schemaHash string
		want       string
	}{
		{"empty tenant", "", "bundle", "industry", "id", []byte("{}"), "h", "tenant_id"},
		{"bad bundle", "t", "Bad", "e", "id", []byte("{}"), "h", "bundle_name"},
		{"bad entity type", "t", "bundle", "Bad", "id", []byte("{}"), "h", "entity_type"},
		{"empty id", "t", "bundle", "industry", "", []byte("{}"), "h", "entity_id"},
		{"long id", "t", "bundle", "industry", strings.Repeat("x", 129), []byte("{}"), "h", "entity_id"},
		{"empty data", "t", "bundle", "industry", "id", []byte{}, "h", "data"},
		{"invalid JSON data", "t", "bundle", "industry", "id", []byte(`not json`), "h", "valid JSON"},
		{"empty schema hash", "t", "bundle", "industry", "id", []byte("{}"), "", "schema_hash"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := domain.NewKGEntity(
				tc.tenantID, tc.bundleName, tc.entityType, tc.entityID, tc.data, tc.schemaHash,
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

func TestNewKGEntity_RejectsHugeData(t *testing.T) {
	t.Parallel()

	// Build a valid JSON document just over the 100 KB limit.
	huge := make([]byte, domain.KGEntityMaxDataBytes+1)
	for i := range huge {
		huge[i] = 'a'
	}
	// Wrap as valid JSON string to pass the json.Valid check after size check
	// fires first.
	wrapped := append([]byte(`{"x":"`), append(huge, []byte(`"}`)...)...)

	_, err := domain.NewKGEntity("t", "bundle", "industry", "id", wrapped, "h")
	if err == nil {
		t.Fatal("expected size limit error")
	}
	if !strings.Contains(err.Error(), "exceeds max") {
		t.Errorf("error must mention size limit: %v", err)
	}
}

func TestNewKGEntity_AcceptsUnicodeInData(t *testing.T) {
	t.Parallel()

	e, err := domain.NewKGEntity(
		"t", "bundle", "industry", "PM",
		[]byte(`{"name":"사용자 🎉 测试","code":"PM"}`),
		"h",
	)
	if err != nil {
		t.Fatalf("unicode JSON must be accepted: %v", err)
	}
	if e == nil {
		t.Fatal("expected non-nil entity")
	}
}
