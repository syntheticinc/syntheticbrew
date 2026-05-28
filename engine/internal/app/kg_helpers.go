package app

import (
	"context"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/kgtools"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/kgread"
	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// kgEnforcerAdapter adapts a plugin.KGEnforcer to the consumer-side quota
// interfaces declared by kgapply and kgmutate. Layer-integrity glue: the
// usecases depend on a minimal interface, not on pkg/plugin directly.
type kgEnforcerAdapter struct {
	enforcer plugin.KGEnforcer
}

// OnEntityWrite forwards to the plugin enforcer. The signature matches
// kgapply.QuotaEnforcer AND kgmutate.QuotaEnforcer (identical contract).
func (a *kgEnforcerAdapter) OnEntityWrite(
	ctx context.Context,
	tenantID, bundleName string,
	deltaEntities int,
	deltaBytes int64,
) error {
	if a == nil || a.enforcer == nil {
		return nil
	}
	return a.enforcer.OnEntityWrite(ctx, tenantID, bundleName, deltaEntities, deltaBytes)
}

// kgAdvisoryLockerNoop satisfies kgapply.AdvisoryLocker without acquiring an
// actual database advisory lock. Used in CE/EE single-tenant deployments
// where bundle apply contention is negligible. Cloud deployments swap in a
// real implementation backed by pg_advisory_xact_lock keyed by
// hash(tenant_id, bundle_name).
type kgAdvisoryLockerNoop struct{}

// LockBundle returns a no-op unlock function.
func (kgAdvisoryLockerNoop) LockBundle(_ context.Context, _, _ string) (func(), error) {
	return func() {}, nil
}

// kgEntityReaderAdapter adapts *configrepo.GORMKGEntityRepository to the
// kgread.EntityReader interface — converts between the two ListEntitiesQuery
// types (each package defines its own per Clean Architecture rule).
type kgEntityReaderAdapter struct {
	repo *configrepo.GORMKGEntityRepository
}

// ListEntities translates kgread.ListEntitiesQuery → configrepo.ListEntitiesQuery
// and forwards to the GORM repository.
func (a *kgEntityReaderAdapter) ListEntities(
	ctx context.Context,
	q kgread.ListEntitiesQuery,
) ([]*domain.KGEntity, int, error) {
	return a.repo.ListEntities(ctx, configrepo.ListEntitiesQuery{
		TenantID:   q.TenantID,
		BundleName: q.BundleName,
		EntityType: q.EntityType,
		Filters:    q.Filters,
		Limit:      q.Limit,
		Offset:     q.Offset,
	})
}

// GetEntity forwards to the underlying repo so the adapter satisfies the
// full EntityReader contract.
func (a *kgEntityReaderAdapter) GetEntity(
	ctx context.Context,
	tenantID, bundleName, entityType, entityID string,
) (*domain.KGEntity, error) {
	return a.repo.GetEntity(ctx, tenantID, bundleName, entityType, entityID)
}

// kgEntityReaderForToolFactory adapts the GORM entity repo to the
// kgtools.EntityReader interface used by the dynamic tool factory. The two
// interfaces share the same column-level semantics but use different shapes
// (positional args vs query struct) — this glue keeps kgtools free of any
// configrepo dependency so it doesn't pull GORM into the tool layer.
type kgEntityReaderForToolFactory struct {
	repo *configrepo.GORMKGEntityRepository
}

func (a *kgEntityReaderForToolFactory) ListEntities(
	ctx context.Context,
	tenantID, bundleName, entityType string,
	filters map[string]any,
	limit, offset int,
) ([]*domain.KGEntity, int, error) {
	return a.repo.ListEntities(ctx, configrepo.ListEntitiesQuery{
		TenantID:   tenantID,
		BundleName: bundleName,
		EntityType: entityType,
		Filters:    filters,
		Limit:      limit,
		Offset:     offset,
	})
}

func (a *kgEntityReaderForToolFactory) GetEntity(
	ctx context.Context,
	tenantID, bundleName, entityType, entityID string,
) (*domain.KGEntity, error) {
	return a.repo.GetEntity(ctx, tenantID, bundleName, entityType, entityID)
}

// kgProviderInvalidatorAdapter wraps the kgtools.Provider so it can be passed
// to the usecase layer through its narrow BundleInvalidator interface.
// Decouples kgapply/kgmutate (clean layer) from kgtools (infrastructure).
type kgProviderInvalidatorAdapter struct {
	provider *kgtools.Provider
}

// InvalidateBundle forwards to the provider's per-bundle invalidation. nil
// provider is tolerated for tests that don't wire the resolver.
func (a *kgProviderInvalidatorAdapter) InvalidateBundle(tenantID, bundleName string) {
	if a == nil || a.provider == nil {
		return
	}
	a.provider.InvalidateBundle(tenantID, bundleName)
}
