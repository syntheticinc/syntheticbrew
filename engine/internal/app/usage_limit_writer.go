package app

import (
	"context"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// usageLimitConfigStore is the subset of the usage-limit repository the writer
// needs: atomically insert a limit only when none exists for its (tenant,
// scope). The GORM usage-limit repository satisfies it.
type usageLimitConfigStore interface {
	CreateIfAbsent(ctx context.Context, limit domain.UsageLimit) (bool, error)
}

// engineUsageLimitWriter is the concrete plugin.UsageLimitWriter the engine
// wires into the plugin at startup. It writes through the engine's own
// tenant-scoped usage-limit repository so a provisioning plugin can install a
// default cap without reimplementing the write or knowing the tenant context
// key — the same "use the engine's real code path" contract as the tenant
// seeder.
type engineUsageLimitWriter struct {
	configs usageLimitConfigStore
}

// newEngineUsageLimitWriter constructs the writer over a usage-limit store.
func newEngineUsageLimitWriter(configs usageLimitConfigStore) *engineUsageLimitWriter {
	return &engineUsageLimitWriter{configs: configs}
}

// EnsureLimit satisfies plugin.UsageLimitWriter. It writes the limit for
// tenantID's scope only when none is configured yet, never overwriting an
// existing one, so re-provisioning a tenant — or one whose limit an operator
// has since changed — is safe. The insert-if-absent is atomic (repository ON
// CONFLICT DO NOTHING), so there is no check-then-act window. Returns whether a
// row was written.
func (w *engineUsageLimitWriter) EnsureLimit(ctx context.Context, tenantID, scope, unit string, limitValue, intervalSeconds int64) (bool, error) {
	if tenantID == "" {
		return false, fmt.Errorf("tenant_id is required")
	}

	limit := domain.UsageLimit{
		Scope:           scope,
		Unit:            unit,
		LimitValue:      limitValue,
		IntervalSeconds: intervalSeconds,
		Enabled:         true,
	}
	if err := limit.Validate(); err != nil {
		return false, fmt.Errorf("validate default usage limit: %w", err)
	}

	// Scope the context to the tenant so the repository stamps tenant_id.
	ctx = domain.WithTenantID(ctx, tenantID)

	created, err := w.configs.CreateIfAbsent(ctx, limit)
	if err != nil {
		return false, fmt.Errorf("ensure default usage limit: %w", err)
	}
	return created, nil
}

var _ plugin.UsageLimitWriter = (*engineUsageLimitWriter)(nil)
