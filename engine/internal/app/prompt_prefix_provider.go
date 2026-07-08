package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// promptPrefixPolicyReader reads protected per-tenant policy values by key.
// Defined consumer-side; configrepo.GORMTenantPolicyRepository satisfies it.
type promptPrefixPolicyReader interface {
	GetMany(ctx context.Context, keys []string) (map[string]string, error)
}

// promptPrefixCacheTTL is how long a resolved prefix (including the empty
// one) is served from cache before the policy row is re-read.
const promptPrefixCacheTTL = 60 * time.Second

// promptPrefixCacheMaxEntries caps the per-tenant cache size.
const promptPrefixCacheMaxEntries = 10000

type promptPrefixCacheEntry struct {
	value     string
	expiresAt time.Time
}

// policyPromptPrefixProvider resolves the system_prompt_prefix policy for the
// tenant in context, with a per-tenant TTL cache so the engine's
// per-execution lookup stays cheap. It implements engine.PromptPrefixProvider.
type policyPromptPrefixProvider struct {
	policies promptPrefixPolicyReader
	ttl      time.Duration
	now      func() time.Time

	mu    sync.Mutex
	cache map[string]promptPrefixCacheEntry
}

// newPolicyPromptPrefixProvider creates a policyPromptPrefixProvider backed
// by the tenant-policy store.
func newPolicyPromptPrefixProvider(policies promptPrefixPolicyReader) *policyPromptPrefixProvider {
	return &policyPromptPrefixProvider{
		policies: policies,
		ttl:      promptPrefixCacheTTL,
		now:      time.Now,
		cache:    make(map[string]promptPrefixCacheEntry),
	}
}

// PromptPrefix returns the tenant's configured prompt prefix, or "" when none
// is configured. DB errors resolve to "" (fail-open: a policy-read hiccup
// must not kill chat) and the empty result is cached for the TTL so a
// struggling DB is not re-queried every turn.
func (p *policyPromptPrefixProvider) PromptPrefix(ctx context.Context) string {
	tenantID := domain.TenantIDFromContext(ctx)
	if tenantID == "" {
		// Same CE single-tenant fallback the tenant-scoped repositories use.
		tenantID = domain.CETenantID
	}

	p.mu.Lock()
	entry, ok := p.cache[tenantID]
	p.mu.Unlock()
	if ok && p.now().Before(entry.expiresAt) {
		return entry.value
	}

	value := p.readPrefix(ctx)
	p.store(tenantID, value)
	return value
}

// readPrefix reads the policy row for the tenant in context.
func (p *policyPromptPrefixProvider) readPrefix(ctx context.Context) string {
	values, err := p.policies.GetMany(ctx, []string{domain.PolicySystemPromptPrefix})
	if err != nil {
		slog.WarnContext(ctx, "prompt-prefix policy read failed — continuing without prefix", "error", err)
		return ""
	}
	return values[domain.PolicySystemPromptPrefix]
}

// store caches value for tenantID with the provider's TTL.
func (p *policyPromptPrefixProvider) store(tenantID, value string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Bound memory: on overflow drop the whole cache instead of tracking
	// recency — the reset is cheap because a refill is one indexed read per
	// tenant.
	if len(p.cache) >= promptPrefixCacheMaxEntries {
		p.cache = make(map[string]promptPrefixCacheEntry)
	}
	p.cache[tenantID] = promptPrefixCacheEntry{value: value, expiresAt: p.now().Add(p.ttl)}
}
