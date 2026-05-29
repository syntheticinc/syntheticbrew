package kgtools

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

func mustNewSchema(t *testing.T, tenantID, bundleName, entityType string) *domain.KGEntitySchema {
	t.Helper()
	s, err := domain.NewKGEntitySchema(
		tenantID, bundleName, entityType,
		[]byte(`{"type":"object","properties":{"code":{"type":"string"}}}`),
		"code",
		[]string{"list", "get"},
		"",
	)
	if err != nil {
		t.Fatalf("NewKGEntitySchema(%s): %v", entityType, err)
	}
	return s
}

func TestRegistry_AllToolNamesForTenantExceptBundle(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	r.Set("bundle1", []*domain.KGEntitySchema{mustNewSchema(t, "t1", "bundle1", "category")})
	r.Set("bundle2", []*domain.KGEntitySchema{mustNewSchema(t, "t1", "bundle2", "brand")})

	union := []string{"get_brand", "get_category", "list_brand", "list_category"}

	if got := r.AllToolNamesForTenant(); !reflect.DeepEqual(got, union) {
		t.Errorf("AllToolNamesForTenant() = %v, want %v", got, union)
	}

	if got := r.AllToolNamesForTenantExceptBundle(""); !reflect.DeepEqual(got, union) {
		t.Errorf("exclude=\"\" should equal full union, got %v", got)
	}

	want := []string{"get_brand", "list_brand"}
	if got := r.AllToolNamesForTenantExceptBundle("bundle1"); !reflect.DeepEqual(got, want) {
		t.Errorf("exclude=\"bundle1\" = %v, want %v", got, want)
	}

	if got := r.AllToolNamesForTenantExceptBundle("nonexistent"); !reflect.DeepEqual(got, union) {
		t.Errorf("exclude=\"nonexistent\" should equal full union, got %v", got)
	}
}

// TestRegistryToolNames_ExcludesReAppliedBundle is the regression guard for
// the 1.4.0 self-collision bug: re-applying the same bundle returned HTTP 409
// because the in-memory Registry source ignored the excludeBundle parameter
// and reported the bundle's own cached tools back as "existing".
func TestRegistryToolNames_ExcludesReAppliedBundle(t *testing.T) {
	t.Parallel()

	p := NewProvider(nil)
	reg, err := p.GetForTenant(context.Background(), "tenant-A")
	if err != nil {
		t.Fatalf("GetForTenant: %v", err)
	}
	reg.Set("bundle-x", []*domain.KGEntitySchema{
		mustNewSchema(t, "tenant-A", "bundle-x", "category"),
		mustNewSchema(t, "tenant-A", "bundle-x", "brand"),
	})

	src := RegistryToolNames{Provider: p}

	got, err := src.ToolNamesForTenant(context.Background(), "tenant-A", "bundle-x")
	if err != nil {
		t.Fatalf("ToolNamesForTenant: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("re-applying bundle-x must not self-collide; got existing tools %v", got)
	}

	got, err = src.ToolNamesForTenant(context.Background(), "tenant-A", "other-bundle")
	if err != nil {
		t.Fatalf("ToolNamesForTenant: %v", err)
	}
	want := []string{"get_brand", "get_category", "list_brand", "list_category"}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("with unrelated excludeBundle, expected full union %v, got %v", want, got)
	}
}

// TestCollisionDetector_ReApplyDoesNotSelfCollide drives the full detector
// path that the kgapply usecase invokes. A registry source with a cached
// bundle_X must not flag bundle_X's own tool names as collisions when the
// caller is re-applying bundle_X.
func TestCollisionDetector_ReApplyDoesNotSelfCollide(t *testing.T) {
	t.Parallel()

	p := NewProvider(nil)
	reg, err := p.GetForTenant(context.Background(), "tenant-A")
	if err != nil {
		t.Fatalf("GetForTenant: %v", err)
	}
	reg.Set("bundle-x", []*domain.KGEntitySchema{
		mustNewSchema(t, "tenant-A", "bundle-x", "category"),
	})

	det := NewCollisionDetector(RegistryToolNames{Provider: p})

	newTools := []string{"list_category", "get_category"}
	collisions, err := det.Detect(context.Background(), "tenant-A", "bundle-x", newTools)
	if err != nil {
		t.Fatalf("Detect re-apply: %v", err)
	}
	if len(collisions) != 0 {
		t.Errorf("re-applying bundle-x must produce zero collisions, got %v", collisions)
	}

	collisions, err = det.Detect(context.Background(), "tenant-A", "different-bundle", newTools)
	if err != nil {
		t.Fatalf("Detect cross-bundle: %v", err)
	}
	want := []string{"get_category", "list_category"}
	if !reflect.DeepEqual(collisions, want) {
		t.Errorf("cross-bundle collisions = %v, want %v", collisions, want)
	}
}
