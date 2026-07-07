package agentregistry

import (
	"context"
	"sync"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
)

// countingAgentReader records how many times each tenant's registry was loaded
// (List is called once per Load) so a test can prove that invalidating one
// tenant does not trigger a reload of another.
type countingAgentReader struct {
	mu      sync.Mutex
	loadsBy map[string]int
	records []configrepo.AgentRecord
}

func newCountingReader(records ...configrepo.AgentRecord) *countingAgentReader {
	return &countingAgentReader{loadsBy: map[string]int{}, records: records}
}

func (c *countingAgentReader) List(ctx context.Context) ([]configrepo.AgentRecord, error) {
	c.mu.Lock()
	c.loadsBy[domain.TenantIDFromContext(ctx)]++
	c.mu.Unlock()
	return c.records, nil
}

func (c *countingAgentReader) GetByName(context.Context, string) (*configrepo.AgentRecord, error) {
	return nil, nil
}
func (c *countingAgentReader) Count(context.Context) (int64, error) { return 0, nil }

func (c *countingAgentReader) loads(tenant string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loadsBy[tenant]
}

// TestManager_InvalidateTenant_IsolatesOtherTenants is the cross-tenant isolation regression
// guard for the [major] concern behind the admin-reloader tenant-scoping fix (#1):
// an admin/provisioning write by tenant A must invalidate ONLY tenant A's cached
// agent registry and never evict or reload another tenant's registry. Pre-fix the
// reloader could only InvalidateAll(), a cross-tenant broadcast; a tenant A request
// would then force every other tenant to reload — a cross-tenant side-effect.
func TestManager_InvalidateTenant_IsolatesOtherTenants(t *testing.T) {
	const tenantA = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const tenantB = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	reader := newCountingReader(configrepo.AgentRecord{
		Name: "support", ModelName: "gpt-4", SystemPrompt: "x",
		Lifecycle: "persistent", ToolExecution: "sequential", MaxSteps: 10, MaxContextSize: 8000,
	})
	mgr := NewManager(reader, true) // perTenant (cloud/EE)

	ctxA := domain.WithTenantID(context.Background(), tenantA)
	ctxB := domain.WithTenantID(context.Background(), tenantB)

	// Warm both tenants' registries (lazy load on first access).
	regA1, err := mgr.GetForContext(ctxA)
	if err != nil {
		t.Fatalf("warm A: %v", err)
	}
	regB1, err := mgr.GetForContext(ctxB)
	if err != nil {
		t.Fatalf("warm B: %v", err)
	}
	if got := reader.loads(tenantA); got != 1 {
		t.Fatalf("tenant A loaded %d times, want 1", got)
	}
	if got := reader.loads(tenantB); got != 1 {
		t.Fatalf("tenant B loaded %d times, want 1", got)
	}

	// Tenant A's write invalidates only tenant A.
	mgr.InvalidateTenant(tenantA)

	// A reloads: fresh instance + a second List for A.
	regA2, err := mgr.GetForContext(ctxA)
	if err != nil {
		t.Fatalf("reload A: %v", err)
	}
	if regA2 == regA1 {
		t.Fatalf("tenant A registry was not evicted: same instance after InvalidateTenant(A)")
	}
	if got := reader.loads(tenantA); got != 2 {
		t.Fatalf("tenant A loaded %d times after invalidate, want 2", got)
	}

	// B is untouched: same cached instance, NO extra load (no cross-tenant side-effect).
	regB2, err := mgr.GetForContext(ctxB)
	if err != nil {
		t.Fatalf("get B: %v", err)
	}
	if regB2 != regB1 {
		t.Fatalf("tenant B registry was evicted by tenant A's invalidation (cross-tenant side-effect)")
	}
	if got := reader.loads(tenantB); got != 1 {
		t.Fatalf("tenant B reloaded %d times, want 1 — tenant A's write leaked into B", got)
	}
}

// TestManager_InvalidateTenant_NoopInSingleTenant guards the CE degenerate case:
// with perTenant=false there is no tenant map, so InvalidateTenant must be a
// no-op that leaves the single shared registry instance intact (only the
// tenant-less InvalidateAll path reloads it).
func TestManager_InvalidateTenant_NoopInSingleTenant(t *testing.T) {
	reader := newCountingReader()
	mgr := NewManager(reader, false) // single-tenant (CE)

	single := mgr.Single()
	mgr.InvalidateTenant("anything")
	if mgr.Single() != single {
		t.Fatalf("InvalidateTenant must not replace the CE singleton registry")
	}
}
