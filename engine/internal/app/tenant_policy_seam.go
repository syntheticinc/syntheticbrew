package app

import (
	"context"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// tenantPolicyStore is the subset of the tenant-policy repository the seam
// needs: upsert, delete, and bulk read of policy entries. The GORM
// tenant-policy repository satisfies it.
type tenantPolicyStore interface {
	Set(ctx context.Context, p domain.TenantPolicy) error
	Delete(ctx context.Context, key string) error
	GetMany(ctx context.Context, keys []string) (map[string]string, error)
}

// engineTenantPolicyWriter is the concrete plugin.TenantPolicyWriter the
// engine wires into the plugin at startup. It writes through the engine's own
// tenant-scoped policy repository so a plugin can install or update protected
// policy entries without reimplementing the write or knowing the tenant
// context key — the same "use the engine's real code path" contract as the
// usage-limit writer.
type engineTenantPolicyWriter struct {
	policies tenantPolicyStore
}

// newEngineTenantPolicyWriter constructs the writer over a policy store.
func newEngineTenantPolicyWriter(policies tenantPolicyStore) *engineTenantPolicyWriter {
	return &engineTenantPolicyWriter{policies: policies}
}

// SetPolicy satisfies plugin.TenantPolicyWriter. It upserts the value for
// tenantID's key, overwriting an existing entry (full upsert, unlike the
// usage-limit writer's write-once EnsureLimit).
func (w *engineTenantPolicyWriter) SetPolicy(ctx context.Context, tenantID, key, value string) error {
	if tenantID == "" {
		return fmt.Errorf("tenant_id is required")
	}

	policy := domain.TenantPolicy{Key: key, Value: value}
	if err := policy.Validate(); err != nil {
		return fmt.Errorf("validate tenant policy: %w", err)
	}

	// Scope the context to the tenant so the repository stamps tenant_id.
	ctx = domain.WithTenantID(ctx, tenantID)

	if err := w.policies.Set(ctx, policy); err != nil {
		return fmt.Errorf("set tenant policy: %w", err)
	}
	return nil
}

// DeletePolicy satisfies plugin.TenantPolicyWriter. It removes the entry for
// tenantID's key; deleting an absent key is not an error.
func (w *engineTenantPolicyWriter) DeletePolicy(ctx context.Context, tenantID, key string) error {
	if tenantID == "" {
		return fmt.Errorf("tenant_id is required")
	}
	// Validate the key alone (an empty value is always valid) so a malformed
	// key is rejected before a round-trip, matching the SetPolicy gate.
	if err := (domain.TenantPolicy{Key: key}).Validate(); err != nil {
		return fmt.Errorf("validate tenant policy key: %w", err)
	}

	ctx = domain.WithTenantID(ctx, tenantID)

	if err := w.policies.Delete(ctx, key); err != nil {
		return fmt.Errorf("delete tenant policy: %w", err)
	}
	return nil
}

// engineTenantPolicyReader is the concrete plugin.TenantPolicyReader the
// engine wires into the plugin at startup. It reads through the engine's own
// tenant-scoped policy repository.
type engineTenantPolicyReader struct {
	policies tenantPolicyStore
}

// newEngineTenantPolicyReader constructs the reader over a policy store.
func newEngineTenantPolicyReader(policies tenantPolicyStore) *engineTenantPolicyReader {
	return &engineTenantPolicyReader{policies: policies}
}

// GetPolicies satisfies plugin.TenantPolicyReader. Keys with no configured
// entry are absent from the returned map. Empty tenantID short-circuits to
// (nil, nil) — CE / single-tenant mode has no policy surface.
func (r *engineTenantPolicyReader) GetPolicies(ctx context.Context, tenantID string, keys []string) (map[string]string, error) {
	if tenantID == "" {
		return nil, nil
	}

	ctx = domain.WithTenantID(ctx, tenantID)

	values, err := r.policies.GetMany(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("get tenant policies: %w", err)
	}
	return values, nil
}

var _ plugin.TenantPolicyWriter = (*engineTenantPolicyWriter)(nil)
var _ plugin.TenantPolicyReader = (*engineTenantPolicyReader)(nil)
