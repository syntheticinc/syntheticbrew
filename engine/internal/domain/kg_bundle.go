package domain

import (
	"fmt"
	"regexp"
	"time"
)

// kgBundleNameRegex enforces a safe, URL-friendly identifier for KG bundle
// names. Lower-case letters, digits, single hyphens. Cannot start or end with
// a hyphen. Length 2-64. This regex is also enforced at the database level
// via a CHECK constraint on kg_bundle.bundle_name.
var kgBundleNameRegex = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}[a-z0-9]$`)

// KGBundle is a customer's deploy unit for a Knowledge Graph. A bundle groups
// related entity schemas and their instances; it is the atomic apply unit
// (entire bundle apply succeeds or rolls back). Bundles are tenant-scoped:
// the same bundle_name across tenants refers to independent data.
type KGBundle struct {
	TenantID    string
	BundleName  string
	Version     string
	Manifest    map[string]any // entity_types + counts + schema_hashes summary
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// NewKGBundle constructs a KGBundle with field validation. Returns an error
// describing the first invalid field if construction fails.
func NewKGBundle(tenantID, bundleName, version string, manifest map[string]any) (*KGBundle, error) {
	b := &KGBundle{
		TenantID:   tenantID,
		BundleName: bundleName,
		Version:    version,
		Manifest:   manifest,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if b.Manifest == nil {
		b.Manifest = make(map[string]any)
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return b, nil
}

// Validate checks all KGBundle invariants. Called by constructors and the
// apply usecase before any state mutation. Returns a wrapped error describing
// the first violation.
func (b *KGBundle) Validate() error {
	if b.TenantID == "" {
		return fmt.Errorf("kg_bundle.tenant_id is required")
	}
	if !ValidKGBundleName(b.BundleName) {
		return fmt.Errorf("kg_bundle.bundle_name %q invalid: must match %s", b.BundleName, kgBundleNameRegex)
	}
	if b.Version == "" {
		return fmt.Errorf("kg_bundle.version is required")
	}
	return nil
}

// ValidKGBundleName reports whether s is a syntactically valid KG bundle name.
// Exported so import-side validators (brewctl loader, /config/import handler)
// can use the same rule as the domain layer without re-implementing the regex.
func ValidKGBundleName(s string) bool {
	if len(s) < 2 || len(s) > 64 {
		return false
	}
	return kgBundleNameRegex.MatchString(s)
}
