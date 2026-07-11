package app

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// fakePolicyReader is an in-memory promptPrefixPolicyReader keyed by tenant.
// It resolves the tenant the same way the repositories do (context →
// CETenantID fallback) so per-tenant cache behavior can be exercised.
type fakePolicyReader struct {
	mu       sync.Mutex
	byTenant map[string]map[string]string
	err      error
	calls    map[string]int
}

func newFakePolicyReader() *fakePolicyReader {
	return &fakePolicyReader{
		byTenant: map[string]map[string]string{},
		calls:    map[string]int{},
	}
}

func (f *fakePolicyReader) set(tenantID, key, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.byTenant[tenantID] == nil {
		f.byTenant[tenantID] = map[string]string{}
	}
	f.byTenant[tenantID][key] = value
}

func (f *fakePolicyReader) GetMany(ctx context.Context, keys []string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tenantID := domain.TenantIDFromContext(ctx)
	if tenantID == "" {
		tenantID = domain.CETenantID
	}
	f.calls[tenantID]++
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]string{}
	for _, k := range keys {
		if v, ok := f.byTenant[tenantID][k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func (f *fakePolicyReader) callCount(tenantID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[tenantID]
}

func TestPolicyPromptPrefixProvider_Hit(t *testing.T) {
	reader := newFakePolicyReader()
	reader.set(domain.CETenantID, domain.PolicySystemPromptPrefix, "MY PREFIX")
	p := newPolicyPromptPrefixProvider(reader)

	assert.Equal(t, "MY PREFIX", p.PromptPrefix(context.Background()))
}

func TestPolicyPromptPrefixProvider_Absent(t *testing.T) {
	reader := newFakePolicyReader()
	p := newPolicyPromptPrefixProvider(reader)

	assert.Equal(t, "", p.PromptPrefix(context.Background()))
}

func TestPolicyPromptPrefixProvider_CachesWithinTTL(t *testing.T) {
	reader := newFakePolicyReader()
	reader.set(domain.CETenantID, domain.PolicySystemPromptPrefix, "V1")
	now := time.Unix(1_000_000, 0)
	p := newPolicyPromptPrefixProvider(reader)
	p.now = func() time.Time { return now }

	assert.Equal(t, "V1", p.PromptPrefix(context.Background()))
	// A second read inside the TTL is served from cache — even though the
	// underlying value changed.
	reader.set(domain.CETenantID, domain.PolicySystemPromptPrefix, "V2")
	assert.Equal(t, "V1", p.PromptPrefix(context.Background()))
	assert.Equal(t, 1, reader.callCount(domain.CETenantID), "second read within TTL must hit cache")

	// After the TTL elapses the row is re-read.
	now = now.Add(promptPrefixCacheTTL + time.Second)
	assert.Equal(t, "V2", p.PromptPrefix(context.Background()))
	assert.Equal(t, 2, reader.callCount(domain.CETenantID))
}

func TestPolicyPromptPrefixProvider_DBError_FailsOpenAndCaches(t *testing.T) {
	reader := newFakePolicyReader()
	reader.err = fmt.Errorf("db down")
	now := time.Unix(1_000_000, 0)
	p := newPolicyPromptPrefixProvider(reader)
	p.now = func() time.Time { return now }

	assert.Equal(t, "", p.PromptPrefix(context.Background()), "DB error must fail open to empty prefix")
	// The empty result is cached for the TTL so a struggling DB is not
	// re-queried every turn.
	assert.Equal(t, "", p.PromptPrefix(context.Background()))
	assert.Equal(t, 1, reader.callCount(domain.CETenantID), "error result must be cached within TTL")
}

func TestPolicyPromptPrefixProvider_PerTenantIsolation(t *testing.T) {
	reader := newFakePolicyReader()
	reader.set("tenant-a", domain.PolicySystemPromptPrefix, "PREFIX A")
	reader.set("tenant-b", domain.PolicySystemPromptPrefix, "PREFIX B")
	p := newPolicyPromptPrefixProvider(reader)

	ctxA := domain.WithTenantID(context.Background(), "tenant-a")
	ctxB := domain.WithTenantID(context.Background(), "tenant-b")

	assert.Equal(t, "PREFIX A", p.PromptPrefix(ctxA))
	assert.Equal(t, "PREFIX B", p.PromptPrefix(ctxB))
	// Cached independently.
	assert.Equal(t, "PREFIX A", p.PromptPrefix(ctxA))
	assert.Equal(t, "PREFIX B", p.PromptPrefix(ctxB))
}

func TestPolicyPromptPrefixProvider_OverflowReset(t *testing.T) {
	reader := newFakePolicyReader()
	p := newPolicyPromptPrefixProvider(reader)

	// Fill the cache to its cap with distinct tenants.
	for i := 0; i < promptPrefixCacheMaxEntries; i++ {
		tenantID := fmt.Sprintf("tenant-%d", i)
		p.PromptPrefix(domain.WithTenantID(context.Background(), tenantID))
	}
	require.Equal(t, promptPrefixCacheMaxEntries, len(p.cache))

	// The next distinct tenant trips the overflow reset: the whole cache is
	// dropped and only the new entry remains.
	p.PromptPrefix(domain.WithTenantID(context.Background(), "tenant-overflow"))
	assert.Equal(t, 1, len(p.cache), "overflow must reset the cache to just the new entry")
}
