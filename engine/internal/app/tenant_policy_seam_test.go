package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// fakeTenantPolicyStore records Set/Delete/GetMany calls and the tenant
// stamped into the context so the seam's scoping and pass-through can be
// asserted without a database.
type fakeTenantPolicyStore struct {
	err    error
	values map[string]string

	setCalled    bool
	gotPolicy    domain.TenantPolicy
	deleteCalled bool
	gotKey       string
	getCalled    bool
	gotKeys      []string
	tenantOnCall string
}

func (f *fakeTenantPolicyStore) Set(ctx context.Context, p domain.TenantPolicy) error {
	f.setCalled = true
	f.gotPolicy = p
	f.tenantOnCall = domain.TenantIDFromContext(ctx)
	return f.err
}

func (f *fakeTenantPolicyStore) Delete(ctx context.Context, key string) error {
	f.deleteCalled = true
	f.gotKey = key
	f.tenantOnCall = domain.TenantIDFromContext(ctx)
	return f.err
}

func (f *fakeTenantPolicyStore) GetMany(ctx context.Context, keys []string) (map[string]string, error) {
	f.getCalled = true
	f.gotKeys = keys
	f.tenantOnCall = domain.TenantIDFromContext(ctx)
	return f.values, f.err
}

func TestEngineTenantPolicyWriter_SetScopesTenantAndWrites(t *testing.T) {
	store := &fakeTenantPolicyStore{}
	w := newEngineTenantPolicyWriter(store)

	err := w.SetPolicy(context.Background(), "tenant-a", plugin.PolicyActiveUsersMode, plugin.PolicyModeMonitor)
	if err != nil {
		t.Fatalf("SetPolicy: unexpected error: %v", err)
	}
	if !store.setCalled {
		t.Fatal("expected Set to be called")
	}
	if store.gotPolicy.Key != domain.PolicyActiveUsersMode || store.gotPolicy.Value != domain.ActiveUsersModeMonitor {
		t.Fatalf("wrong policy written: %+v", store.gotPolicy)
	}
	// The write must be scoped to the tenant so the repo stamps tenant_id.
	if store.tenantOnCall != "tenant-a" {
		t.Fatalf("expected tenant scoping on Set, got %q", store.tenantOnCall)
	}
}

func TestEngineTenantPolicyWriter_RequiresTenant(t *testing.T) {
	store := &fakeTenantPolicyStore{}
	w := newEngineTenantPolicyWriter(store)

	if err := w.SetPolicy(context.Background(), "", plugin.PolicySchemasLimit, "3"); err == nil {
		t.Fatal("expected error for empty tenant_id on SetPolicy")
	}
	if err := w.DeletePolicy(context.Background(), "", plugin.PolicySchemasLimit); err == nil {
		t.Fatal("expected error for empty tenant_id on DeletePolicy")
	}
	if store.setCalled || store.deleteCalled {
		t.Fatal("no repository access expected without a tenant")
	}
}

func TestEngineTenantPolicyWriter_RejectsBadKey(t *testing.T) {
	store := &fakeTenantPolicyStore{}
	w := newEngineTenantPolicyWriter(store)

	if err := w.SetPolicy(context.Background(), "tenant-a", "Bad-Key", "x"); err == nil {
		t.Fatal("expected validation error for malformed key on SetPolicy")
	}
	if err := w.DeletePolicy(context.Background(), "tenant-a", "Bad-Key"); err == nil {
		t.Fatal("expected validation error for malformed key on DeletePolicy")
	}
	if store.setCalled || store.deleteCalled {
		t.Fatal("repository must not run when validation fails")
	}
}

func TestEngineTenantPolicyWriter_RejectsOversizedValue(t *testing.T) {
	store := &fakeTenantPolicyStore{}
	w := newEngineTenantPolicyWriter(store)

	oversized := strings.Repeat("a", 8193)
	if err := w.SetPolicy(context.Background(), "tenant-a", plugin.PolicySystemPromptPrefix, oversized); err == nil {
		t.Fatal("expected validation error for oversized value")
	}
	if store.setCalled {
		t.Fatal("Set must not run when validation fails")
	}
}

func TestEngineTenantPolicyWriter_DeletePassthrough(t *testing.T) {
	store := &fakeTenantPolicyStore{}
	w := newEngineTenantPolicyWriter(store)

	if err := w.DeletePolicy(context.Background(), "tenant-a", plugin.PolicyWidgetAttribution); err != nil {
		t.Fatalf("DeletePolicy: unexpected error: %v", err)
	}
	if !store.deleteCalled {
		t.Fatal("expected Delete to be called")
	}
	if store.gotKey != domain.PolicyWidgetAttribution {
		t.Fatalf("wrong key deleted: %q", store.gotKey)
	}
	if store.tenantOnCall != "tenant-a" {
		t.Fatalf("expected tenant scoping on Delete, got %q", store.tenantOnCall)
	}
}

func TestEngineTenantPolicyWriter_PropagatesStoreError(t *testing.T) {
	store := &fakeTenantPolicyStore{err: errors.New("db down")}
	w := newEngineTenantPolicyWriter(store)

	if err := w.SetPolicy(context.Background(), "tenant-a", plugin.PolicySchemasLimit, "3"); err == nil {
		t.Fatal("expected the store error to propagate from SetPolicy")
	}
	if err := w.DeletePolicy(context.Background(), "tenant-a", plugin.PolicySchemasLimit); err == nil {
		t.Fatal("expected the store error to propagate from DeletePolicy")
	}
}

func TestEngineTenantPolicyReader_EmptyTenantShortCircuits(t *testing.T) {
	store := &fakeTenantPolicyStore{}
	r := newEngineTenantPolicyReader(store)

	values, err := r.GetPolicies(context.Background(), "", []string{plugin.PolicySchemasLimit})
	if err != nil {
		t.Fatalf("GetPolicies: unexpected error: %v", err)
	}
	if values != nil {
		t.Fatalf("expected nil map for empty tenant, got %v", values)
	}
	if store.getCalled {
		t.Fatal("no repository access expected without a tenant")
	}
}

func TestEngineTenantPolicyReader_ScopesTenantAndPassesKeys(t *testing.T) {
	store := &fakeTenantPolicyStore{values: map[string]string{
		plugin.PolicyActiveUsersLimit: "25",
	}}
	r := newEngineTenantPolicyReader(store)

	keys := []string{plugin.PolicyActiveUsersLimit, plugin.PolicyActiveUsersMode}
	values, err := r.GetPolicies(context.Background(), "tenant-a", keys)
	if err != nil {
		t.Fatalf("GetPolicies: unexpected error: %v", err)
	}
	if !store.getCalled {
		t.Fatal("expected GetMany to be called")
	}
	if len(store.gotKeys) != 2 || store.gotKeys[0] != keys[0] || store.gotKeys[1] != keys[1] {
		t.Fatalf("keys not passed through: %v", store.gotKeys)
	}
	if store.tenantOnCall != "tenant-a" {
		t.Fatalf("expected tenant scoping on GetMany, got %q", store.tenantOnCall)
	}
	if values[plugin.PolicyActiveUsersLimit] != "25" {
		t.Fatalf("expected store values returned as-is, got %v", values)
	}
	if _, ok := values[plugin.PolicyActiveUsersMode]; ok {
		t.Fatal("missing keys must stay absent from the map")
	}
}

// TestPluginPolicyVocabMatchesDomain pins the public plugin constants to the
// engine's internal domain constants. The writer validates against domain, so
// a drift here would turn every plugin-driven write into a validation error —
// catch it at compile/test time instead.
func TestPluginPolicyVocabMatchesDomain(t *testing.T) {
	pairs := map[string][2]string{
		"PolicySystemPromptPrefix":      {plugin.PolicySystemPromptPrefix, domain.PolicySystemPromptPrefix},
		"PolicyWidgetAttribution":       {plugin.PolicyWidgetAttribution, domain.PolicyWidgetAttribution},
		"PolicyActiveUsersLimit":        {plugin.PolicyActiveUsersLimit, domain.PolicyActiveUsersLimit},
		"PolicyActiveUsersMode":         {plugin.PolicyActiveUsersMode, domain.PolicyActiveUsersMode},
		"PolicyKnowledgeDocumentsLimit": {plugin.PolicyKnowledgeDocumentsLimit, domain.PolicyKnowledgeDocumentsLimit},
		"PolicySchemasLimit":            {plugin.PolicySchemasLimit, domain.PolicySchemasLimit},
		"PolicyModeEnforce":             {plugin.PolicyModeEnforce, domain.ActiveUsersModeEnforce},
		"PolicyModeMonitor":             {plugin.PolicyModeMonitor, domain.ActiveUsersModeMonitor},
	}
	for name, pair := range pairs {
		if pair[0] != pair[1] {
			t.Errorf("%s: plugin %q != domain %q", name, pair[0], pair[1])
		}
	}
}
