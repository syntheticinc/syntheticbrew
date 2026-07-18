package app

import (
	"context"
	"sync"
	"time"

	"gorm.io/gorm"

	deliveryhttp "github.com/syntheticinc/syntheticbrew/internal/delivery/http"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
)

// byokCacheTTL bounds how long a resolved per-tenant BYOK config is cached.
// It is defence-in-depth for revocation (D4): disabling BYOK is the abuse
// kill-switch, and an entry that survived a missed invalidation must still
// expire within a known window. Invalidation on write is the primary mechanism;
// the TTL is the backstop.
const byokCacheTTL = 30 * time.Second

// byokTenantResolver resolves BYOK configuration per tenant from the
// tenant-scoped `settings` rows, replacing the former process-global config so
// one tenant's toggle can never enable BYOK for another (BUG-08). It mirrors
// agentregistry.Manager: a per-tenant cache invalidated on the writer's tenant.
type byokTenantResolver struct {
	db       *gorm.DB
	fallback config.BYOKConfig
	// sentinelFallback controls what an empty tenant_id resolves to (B1). In
	// local auth-mode the CE token carries no tenant and the whole engine reads
	// under the sentinel, so an empty tenant maps to the sentinel row. In
	// external (multi-tenant) auth-mode an empty tenant is a fault and must fail
	// closed — never read the sentinel's row for an unattributed request.
	sentinelFallback bool

	mu    sync.RWMutex
	cache map[string]cachedBYOK
}

type cachedBYOK struct {
	cfg deliveryhttp.BYOKConfig
	at  time.Time
}

// newBYOKTenantResolver builds a resolver. sentinelFallback should be true only
// in local auth-mode.
func newBYOKTenantResolver(db *gorm.DB, fallback config.BYOKConfig, sentinelFallback bool) *byokTenantResolver {
	return &byokTenantResolver{
		db:               db,
		fallback:         fallback,
		sentinelFallback: sentinelFallback,
		cache:            make(map[string]cachedBYOK),
	}
}

// resolveKey maps a request context to the cache key (a tenant_id) and reports
// whether BYOK may be resolved at all. It is the single normalisation used by
// both Resolve and InvalidateTenant so their keys always match (B1).
func (r *byokTenantResolver) resolveKey(ctx context.Context) (string, bool) {
	tenantID := domain.TenantIDFromContext(ctx)
	if tenantID != "" {
		return tenantID, true
	}
	if r.sentinelFallback {
		return domain.CETenantID, true
	}
	// External auth-mode with no tenant — fail closed, never read the sentinel.
	return "", false
}

// Resolve returns the BYOK config for the request's tenant. It fails closed
// (BYOK disabled) when the tenant cannot be attributed in multi-tenant mode
// (F7) — never falling through to another tenant's row.
func (r *byokTenantResolver) Resolve(ctx context.Context) deliveryhttp.BYOKConfig {
	key, ok := r.resolveKey(ctx)
	if !ok {
		return deliveryhttp.BYOKConfig{Enabled: false}
	}

	r.mu.RLock()
	if entry, hit := r.cache[key]; hit && time.Since(entry.at) < byokCacheTTL {
		r.mu.RUnlock()
		return entry.cfg
	}
	r.mu.RUnlock()

	// Load under the resolved tenant so the settings read is correctly scoped
	// (the cache key and the loaded tenant are identical — no cross-tenant race).
	loaded := loadBYOKConfig(domain.WithTenantID(ctx, key), r.db, r.fallback)
	cfg := deliveryhttp.BYOKConfig{
		Enabled:          loaded.Enabled,
		AllowedProviders: loaded.AllowedProviders,
	}

	r.mu.Lock()
	r.cache[key] = cachedBYOK{cfg: cfg, at: time.Now()}
	r.mu.Unlock()
	return cfg
}

// InvalidateTenant drops the cached config for the writer's tenant so a
// settings change takes effect on the next request. It uses the same key
// normalisation as Resolve (B1) — otherwise a CE write (empty tenant) would
// miss the sentinel-keyed entry and the toggle would not apply until restart.
func (r *byokTenantResolver) InvalidateTenant(ctx context.Context) {
	key, ok := r.resolveKey(ctx)
	if !ok {
		return
	}
	r.mu.Lock()
	delete(r.cache, key)
	r.mu.Unlock()
}
