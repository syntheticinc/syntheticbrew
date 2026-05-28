package app

import (
	"context"

	deliveryhttp "github.com/syntheticinc/syntheticbrew/internal/delivery/http"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/kgtools"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/kgread"
	"github.com/syntheticinc/syntheticbrew/pkg/jsonschema"
	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// parseAnnotationsSafe wraps jsonschema.ParseAnnotations for callsites where
// errors are tolerable (e.g. enum lookup for sort enrichment) — returns nil
// annotations instead of propagating. Schema apply already validated the
// document, so runtime parse failures are transient corruption signals best
// left to the apply audit log rather than surfaced through query paths.
func parseAnnotationsSafe(raw []byte) (*jsonschema.Annotations, error) {
	return jsonschema.ParseAnnotations(raw)
}

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
		Filters:    kgreadFiltersToRepo(q.Filters),
		Sort:       kgreadSortToRepo(q.Sort),
		Limit:      q.Limit,
		Offset:     q.Offset,
	})
}

// kgreadSortToRepo copies SortSpec across the layer boundary. Identical
// shape — duplication is the cost of layer-pure types.
func kgreadSortToRepo(in []kgread.SortSpec) []configrepo.SortSpec {
	if len(in) == 0 {
		return nil
	}
	out := make([]configrepo.SortSpec, len(in))
	for i, s := range in {
		out[i] = configrepo.SortSpec{
			Field:      s.Field,
			Order:      s.Order,
			EnumValues: s.EnumValues,
		}
	}
	return out
}

// kgreadFiltersToRepo copies the kgread.FilterSpec map to configrepo.FilterSpec.
// Identical shapes; the duplication is the price of layer-pure types.
func kgreadFiltersToRepo(in map[string]kgread.FilterSpec) map[string]configrepo.FilterSpec {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]configrepo.FilterSpec, len(in))
	for k, v := range in {
		out[k] = configrepo.FilterSpec{
			Eq:       v.Eq,
			In:       v.In,
			Gte:      v.Gte,
			Gt:       v.Gt,
			Lte:      v.Lte,
			Lt:       v.Lt,
			CastExpr: v.CastExpr,
		}
	}
	return out
}

// plainFiltersToRepo converts a polymorphic filter map (as received from
// LLM tool args or pre-1.4 callers using bare equality) into the typed
// configrepo.FilterSpec. Nested maps with operator keys (`in`, `gte`, ...)
// are recognised and unpacked; everything else becomes Eq.
//
// HTTP delivery and tool args both flow through this helper to avoid
// duplicating operator-key detection in two parsers.
func plainFiltersToRepo(in map[string]any) map[string]configrepo.FilterSpec {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]configrepo.FilterSpec, len(in))
	for k, v := range in {
		out[k] = plainValueToFilterSpec(v)
	}
	return out
}

// recognisedFilterOps is the closed set of operator keys we accept inside a
// nested filter map (`filters: {field: {gte: 10}}`). Used both to dispatch
// the operator and to detect "unknown operator inside an op-shaped map"
// — an LLM emitting `{between: [70, 95]}` should NOT silently degrade into
// "match the whole object as equality" because that produces zero hits
// with a confusing log signature instead of a clear error.
var recognisedFilterOps = map[string]struct{}{
	"in": {}, "gte": {}, "gt": {}, "lte": {}, "lt": {}, "eq": {},
}

// plainValueToFilterSpec normalises a polymorphic filter value. Nested maps
// with recognised operator keys produce In/Gte/Gt/Lte/Lt; bare values
// produce Eq.
//
// Disambiguation: if a map is supplied but none of its keys are recognised
// operators, that's almost certainly an LLM (or HTTP query) sending an
// unsupported operator name. We refuse to collapse it into "match the whole
// map as a JSONB object equality" — that path was silent and produced zero
// results with no signal to the caller. Instead we leave the spec empty,
// and the usecase-layer validator (`validateFilterSpecs`) rejects empty
// specs with `[INVALID_INPUT] filter "X" has no value or operator`.
func plainValueToFilterSpec(v any) configrepo.FilterSpec {
	asMap, ok := v.(map[string]any)
	if !ok {
		return configrepo.FilterSpec{Eq: v}
	}
	spec := configrepo.FilterSpec{}
	sawRecognised := false
	for opKey, opVal := range asMap {
		if _, ok := recognisedFilterOps[opKey]; !ok {
			continue
		}
		sawRecognised = true
		switch opKey {
		case "in":
			if arr, ok := opVal.([]any); ok {
				spec.In = arr
			}
		case "gte":
			spec.Gte = opVal
		case "gt":
			spec.Gt = opVal
		case "lte":
			spec.Lte = opVal
		case "lt":
			spec.Lt = opVal
		case "eq":
			spec.Eq = opVal
		}
	}
	// Map shape with no recognised operator → empty spec on purpose. The
	// validator surfaces this as `[INVALID_INPUT] filter "X" has no value
	// or operator` rather than the silent "JSONB exact match on the entire
	// op-bag" behaviour the old fallback produced.
	_ = sawRecognised
	return spec
}

// IsRange mirrors kgread.FilterSpec.IsRange — duplicated here because we use
// configrepo.FilterSpec in the helper.
func filterSpecIsRange(s configrepo.FilterSpec) bool {
	return s.Gte != nil || s.Gt != nil || s.Lte != nil || s.Lt != nil
}

// httpSortToKgread converts the HTTP layer's lightweight KGSortParam list
// into kgread.SortSpec. The kgread layer enriches EnumValues from the schema
// downstream, so this adapter does not need to know about enum semantics.
func httpSortToKgread(in []deliveryhttp.KGSortParam) []kgread.SortSpec {
	if len(in) == 0 {
		return nil
	}
	out := make([]kgread.SortSpec, len(in))
	for i, s := range in {
		out[i] = kgread.SortSpec{Field: s.Field, Order: s.Order}
	}
	return out
}

// plainFiltersToKgread normalises a polymorphic filter map into kgread.FilterSpec.
// HTTP delivery + tool args both currently pass `map[string]any`; the usecase
// layer wants typed specs. Stage 6 will replace the HTTP path with a query
// parser that produces kgread.FilterSpec directly.
func plainFiltersToKgread(in map[string]any) map[string]kgread.FilterSpec {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]kgread.FilterSpec, len(in))
	for k, v := range in {
		repoSpec := plainValueToFilterSpec(v)
		out[k] = kgread.FilterSpec{
			Eq:  repoSpec.Eq,
			In:  repoSpec.In,
			Gte: repoSpec.Gte,
			Gt:  repoSpec.Gt,
			Lte: repoSpec.Lte,
			Lt:  repoSpec.Lt,
		}
	}
	return out
}

// GetEntity forwards to the underlying repo so the adapter satisfies the
// full EntityReader contract.
func (a *kgEntityReaderAdapter) GetEntity(
	ctx context.Context,
	tenantID, bundleName, entityType, entityID string,
) (*domain.KGEntity, error) {
	return a.repo.GetEntity(ctx, tenantID, bundleName, entityType, entityID)
}

// GetEntities forwards to the underlying repo for batch lookup.
func (a *kgEntityReaderAdapter) GetEntities(
	ctx context.Context,
	tenantID, bundleName, entityType string,
	ids []string,
) ([]*domain.KGEntity, []string, error) {
	return a.repo.GetEntities(ctx, tenantID, bundleName, entityType, ids)
}

// kgEntityReaderForToolFactory adapts the kgread.Usecase to the
// kgtools.EntityReader interface used by the dynamic tool factory.
//
// SECURITY-CRITICAL: this adapter routes the LLM-facing tool path THROUGH
// the kgread.Usecase, not directly to the repo. Without this indirection
// every 1.4.0 hardening (schema-bound filter whitelist, range-on-non-numeric
// rejection, MaxFilterInSize cap, MaxBatchGetIDs cap, KGQueryTimeout wrap)
// would be HTTP-path-only and a prompt-injected LLM call could bypass all
// of them. See `security_1_4_test.go` mutation guards.
type kgEntityReaderForToolFactory struct {
	uc      *kgread.Usecase
	schemas *configrepo.GORMKGSchemaRepository
}

func (a *kgEntityReaderForToolFactory) ListEntities(
	ctx context.Context,
	tenantID, bundleName, entityType string,
	filters map[string]any,
	sort []kgtools.SortHint,
	limit, offset int,
) ([]*domain.KGEntity, int, error) {
	return a.uc.ListEntities(ctx, kgread.ListEntitiesQuery{
		TenantID:   tenantID,
		BundleName: bundleName,
		EntityType: entityType,
		Filters:    plainFiltersToKgread(filters),
		Sort:       toolSortHintsToKgread(sort),
		Limit:      limit,
		Offset:     offset,
	})
}

func (a *kgEntityReaderForToolFactory) GetEntity(
	ctx context.Context,
	tenantID, bundleName, entityType, entityID string,
) (*domain.KGEntity, error) {
	return a.uc.GetEntity(ctx, tenantID, bundleName, entityType, entityID)
}

// GetEntities forwards the batch get call through the usecase so the LLM
// path inherits MaxBatchGetIDs, dedup, and KGQueryTimeout.
func (a *kgEntityReaderForToolFactory) GetEntities(
	ctx context.Context,
	tenantID, bundleName, entityType string,
	ids []string,
) ([]*domain.KGEntity, []string, error) {
	res, err := a.uc.GetEntities(ctx, tenantID, bundleName, entityType, ids)
	if err != nil {
		return nil, nil, err
	}
	return res.Entities, res.NotFound, nil
}

// toolSortHintsToKgread converts the LLM-facing SortHint list into the
// usecase-layer SortSpec. The usecase enriches EnumValues from the schema
// inside its ListEntities path — so this adapter does NOT populate
// EnumValues itself (only the usecase is trusted with that).
func toolSortHintsToKgread(in []kgtools.SortHint) []kgread.SortSpec {
	if len(in) == 0 {
		return nil
	}
	out := make([]kgread.SortSpec, len(in))
	for i, h := range in {
		out[i] = kgread.SortSpec{Field: h.Field, Order: h.Order}
	}
	return out
}

// toolSortHintsToRepo is no longer used — the tool path now routes through
// kgread.Usecase which is the only layer trusted to populate EnumValues
// from the schema. Kept removed; see toolSortHintsToKgread below for the
// usecase-routed equivalent.

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
