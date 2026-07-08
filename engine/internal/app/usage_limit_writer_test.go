package app

import (
	"context"
	"errors"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// fakeUsageLimitStore records the CreateIfAbsent call and the tenant stamped
// into the context so the writer's scoping and pass-through can be asserted
// without a database. `created` models what the repository's atomic
// insert-if-absent returns (true = inserted, false = a row already existed).
type fakeUsageLimitStore struct {
	created bool
	err     error

	called       bool
	gotLimit     domain.UsageLimit
	tenantOnCall string
}

func (f *fakeUsageLimitStore) CreateIfAbsent(ctx context.Context, limit domain.UsageLimit) (bool, error) {
	f.called = true
	f.gotLimit = limit
	f.tenantOnCall = domain.TenantIDFromContext(ctx)
	return f.created, f.err
}

func TestEngineUsageLimitWriter_WritesWhenAbsent(t *testing.T) {
	store := &fakeUsageLimitStore{created: true}
	w := newEngineUsageLimitWriter(store)

	wrote, err := w.EnsureLimit(context.Background(), "tenant-a",
		plugin.UsageScopeTenant, plugin.UsageUnitTurns, 50, 2592000)
	if err != nil {
		t.Fatalf("EnsureLimit: unexpected error: %v", err)
	}
	if !wrote {
		t.Fatal("expected wrote=true when the row was inserted")
	}
	if !store.called {
		t.Fatal("expected CreateIfAbsent to be called")
	}
	if store.gotLimit.Scope != domain.ScopeTenant || store.gotLimit.Unit != domain.UnitTurns {
		t.Fatalf("wrong scope/unit written: %+v", store.gotLimit)
	}
	if store.gotLimit.LimitValue != 50 || store.gotLimit.IntervalSeconds != 2592000 {
		t.Fatalf("wrong value/interval written: %+v", store.gotLimit)
	}
	if !store.gotLimit.Enabled {
		t.Fatal("expected the written limit to be enabled")
	}
	// The write must be scoped to the tenant so the repo stamps tenant_id.
	if store.tenantOnCall != "tenant-a" {
		t.Fatalf("expected tenant scoping on CreateIfAbsent, got %q", store.tenantOnCall)
	}
}

func TestEngineUsageLimitWriter_ReportsNotWrittenWhenRowExists(t *testing.T) {
	// The repository's atomic insert-if-absent declines (created=false) when a
	// row already exists — an upgraded/operator-set limit survives. The writer
	// must surface that as wrote=false without error.
	store := &fakeUsageLimitStore{created: false}
	w := newEngineUsageLimitWriter(store)

	wrote, err := w.EnsureLimit(context.Background(), "tenant-a",
		plugin.UsageScopeTenant, plugin.UsageUnitTurns, 50, 2592000)
	if err != nil {
		t.Fatalf("EnsureLimit: unexpected error: %v", err)
	}
	if wrote {
		t.Fatal("expected wrote=false when a row already exists")
	}
}

func TestEngineUsageLimitWriter_RejectsInvalidLimit(t *testing.T) {
	store := &fakeUsageLimitStore{created: true}
	w := newEngineUsageLimitWriter(store)

	// Unknown unit — must fail validation before any write.
	_, err := w.EnsureLimit(context.Background(), "tenant-a",
		plugin.UsageScopeTenant, "megabytes", 50, 2592000)
	if err == nil {
		t.Fatal("expected validation error for unknown unit")
	}
	if store.called {
		t.Fatal("CreateIfAbsent must not run when validation fails")
	}
}

func TestEngineUsageLimitWriter_RequiresTenant(t *testing.T) {
	store := &fakeUsageLimitStore{}
	w := newEngineUsageLimitWriter(store)

	_, err := w.EnsureLimit(context.Background(), "",
		plugin.UsageScopeTenant, plugin.UsageUnitTurns, 50, 2592000)
	if err == nil {
		t.Fatal("expected error for empty tenant_id")
	}
	if store.called {
		t.Fatal("no repository access expected without a tenant")
	}
}

func TestEngineUsageLimitWriter_PropagatesStoreError(t *testing.T) {
	store := &fakeUsageLimitStore{err: errors.New("db down")}
	w := newEngineUsageLimitWriter(store)

	_, err := w.EnsureLimit(context.Background(), "tenant-a",
		plugin.UsageScopeTenant, plugin.UsageUnitTurns, 50, 2592000)
	if err == nil {
		t.Fatal("expected the store error to propagate")
	}
}

// TestPluginUsageVocabMatchesDomain pins the public plugin constants to the
// engine's internal domain constants. The writer validates against domain, so
// a drift here would turn every plugin-driven write into a validation error —
// catch it at compile/test time instead.
func TestPluginUsageVocabMatchesDomain(t *testing.T) {
	if plugin.UsageScopeTenant != domain.ScopeTenant {
		t.Errorf("UsageScopeTenant %q != domain.ScopeTenant %q", plugin.UsageScopeTenant, domain.ScopeTenant)
	}
	if plugin.UsageScopeUser != domain.ScopeUser {
		t.Errorf("UsageScopeUser %q != domain.ScopeUser %q", plugin.UsageScopeUser, domain.ScopeUser)
	}
	if plugin.UsageUnitTurns != domain.UnitTurns {
		t.Errorf("UsageUnitTurns %q != domain.UnitTurns %q", plugin.UsageUnitTurns, domain.UnitTurns)
	}
	if plugin.UsageUnitSteps != domain.UnitSteps {
		t.Errorf("UsageUnitSteps %q != domain.UnitSteps %q", plugin.UsageUnitSteps, domain.UnitSteps)
	}
}
