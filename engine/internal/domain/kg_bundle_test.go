package domain_test

import (
	"strings"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

func TestNewKGBundle_Valid(t *testing.T) {
	t.Parallel()

	b, err := domain.NewKGBundle("tenant-1", "chirp-iot", "1.0.0", map[string]any{
		"entity_types": []string{"category", "product_attribute"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.TenantID != "tenant-1" {
		t.Errorf("TenantID: got %q, want %q", b.TenantID, "tenant-1")
	}
	if b.BundleName != "chirp-iot" {
		t.Errorf("BundleName: got %q, want %q", b.BundleName, "chirp-iot")
	}
	if b.CreatedAt.IsZero() || b.UpdatedAt.IsZero() {
		t.Error("timestamps must be set")
	}
}

func TestNewKGBundle_NilManifestInitialised(t *testing.T) {
	t.Parallel()

	b, err := domain.NewKGBundle("t", "name", "1.0.0", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.Manifest == nil {
		t.Error("nil Manifest must be initialised to empty map")
	}
}

func TestNewKGBundle_ValidationFailures(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		tenantID   string
		bundleName string
		version    string
		wantSubstr string
	}{
		{"empty tenant", "", "valid-name", "1.0", "tenant_id"},
		{"empty version", "t", "valid-name", "", "version"},
		{"empty bundle name", "t", "", "1.0", "bundle_name"},
		{"single char", "t", "a", "1.0", "bundle_name"},
		{"starts with hyphen", "t", "-name", "1.0", "bundle_name"},
		{"ends with hyphen", "t", "name-", "1.0", "bundle_name"},
		{"upper case", "t", "Bundle", "1.0", "bundle_name"},
		{"underscore", "t", "bundle_name", "1.0", "bundle_name"},
		{"slash traversal", "t", "../etc", "1.0", "bundle_name"},
		{"too long", "t", strings.Repeat("a", 65), "1.0", "bundle_name"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := domain.NewKGBundle(tc.tenantID, tc.bundleName, tc.version, nil)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSubstr)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}

func TestValidKGBundleName(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		input string
		want  bool
	}{
		{"valid simple", "iot", true},
		{"valid hyphenated", "chirp-iot-2026", true},
		{"valid digits", "v2", true},
		{"max length 64", strings.Repeat("a", 64), true},
		{"too long 65", strings.Repeat("a", 65), false},
		{"single char", "a", false},
		{"empty", "", false},
		{"starts with digit", "1abc", false},
		{"starts with hyphen", "-abc", false},
		{"ends with hyphen", "abc-", false},
		{"double hyphen ok", "a--b", true},
		{"uppercase", "Abc", false},
		{"underscore", "a_b", false},
		{"path traversal", "../etc", false},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := domain.ValidKGBundleName(tc.input); got != tc.want {
				t.Errorf("ValidKGBundleName(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}
