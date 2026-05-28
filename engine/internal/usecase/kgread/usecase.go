// Package kgread implements read endpoints for Knowledge Graph bundles,
// schemas, and entities. Used by the admin SPA, brewctl, and (indirectly via
// auto-generated tools) agents.
//
// All reads are tenant-scoped: the usecase resolves tenant_id from the
// explicit argument falling back to the request context, and rejects calls
// that have neither.
//
// Convention for "not found": readers MUST return a pkgerrors.NotFound
// DomainError when a single-row lookup misses; list endpoints return
// (empty, nil). The usecase propagates these errors without re-wrapping.
package kgread

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// Default and maximum page sizes for ListEntities. Exported so HTTP delivery
// can advertise the same numbers in API documentation.
const (
	DefaultListLimit = 50
	MaxListLimit     = 500

	// MaxFilterInSize caps the number of values in a single `[in]` filter
	// to mirror MaxBatchGetIDs. Without this cap a 10k-value IN list would
	// produce a multi-second seq scan even within sweet spot — see security
	// threat KG14-SEC-04.
	MaxFilterInSize = 500
)

// KGQueryTimeout caps how long a single read (ListEntities / GetEntities /
// GetEntity) may run before Go-side context cancellation flips. Combined with
// PostgreSQL responding to context cancellation, this is a Stage-7 mitigation
// for KG14-SEC-05 (sort × no filter on a large entity_type producing a
// multi-second seq scan that holds a connection).
//
// 5 seconds is well above the in-sweet-spot p99 (<200ms typical for 10k rows
// on JSONB extracts) and well below the user-perception threshold for an
// agent tool call. Document the limit in the tool description so an agent
// that times out can prompt the user / retry with a tighter filter.
const KGQueryTimeout = 5 * time.Second

// --- Consumer-side interfaces ---

// BundleReader fetches kg_bundle rows.
type BundleReader interface {
	List(ctx context.Context, tenantID string) ([]*domain.KGBundle, error)
	Get(ctx context.Context, tenantID, bundleName string) (*domain.KGBundle, error)
}

// SchemaReader fetches kg_entity_schema rows.
type SchemaReader interface {
	ListByBundle(ctx context.Context, tenantID, bundleName string) ([]*domain.KGEntitySchema, error)
	Get(ctx context.Context, tenantID, bundleName, entityType string) (*domain.KGEntitySchema, error)
}

// EntityReader fetches kg_entity rows. ListEntities is filter-only by the
// schema's x-index fields; the usecase enforces this whitelist before
// invoking the reader.
//
// GetEntity (single) is retained for REST single-id endpoint use — no
// breaking change there in 1.4.0. GetEntities (batch) is the new path that
// auto-MCP `get_<entity>` tool consumes; tool args layer constructs the ids
// slice and the repo emits a single round-trip with array_position-preserved
// ordering.
type EntityReader interface {
	ListEntities(ctx context.Context, q ListEntitiesQuery) (items []*domain.KGEntity, total int, err error)
	GetEntity(ctx context.Context, tenantID, bundleName, entityType, entityID string) (*domain.KGEntity, error)
	GetEntities(ctx context.Context, tenantID, bundleName, entityType string, ids []string) (found []*domain.KGEntity, notFound []string, err error)
}

// MaxBatchGetIDs caps the number of IDs that the batch get tool accepts in
// a single call. Aligned with MaxListLimit so the two paths share the same
// upper bound on per-call result-set size. Exceeding this returns 400.
const MaxBatchGetIDs = 500

// BatchGetResult is what `get_<entity>(ids[])` returns: ordered Entities
// (deduplicated, input-order preserved) and the NotFound list (input-order
// preserved). Partial success semantics — 200 OK even when some IDs miss.
type BatchGetResult struct {
	Entities []*domain.KGEntity
	NotFound []string
}

// FilterSpec captures one field's filter expression. Exactly one of the
// fields below is expected to be non-zero per spec — the parser layer
// (HTTP handler / tool args) is responsible for surfacing "multiple
// operators on the same field" as InvalidInput, the usecase trusts the spec.
//
// Equality (Eq) is the backward-compat default and what 1.3.0 clients always
// produced. Operators were added in 1.4.0:
//   - In  → matches any value in the list (`data->>'field' = ANY($1)`)
//   - Gte / Gt / Lte / Lt → range; only valid on numeric or date/date-time
//     properties, validated against schema FieldTypes
type FilterSpec struct {
	Eq  any
	In  []any
	Gte any
	Gt  any
	Lte any
	Lt  any

	// CastExpr is engine-populated by the usecase after validation; callers
	// (HTTP / tool args) MUST NOT set this. The validator inspects the
	// schema's FieldTypes for the filtered field and sets "timestamptz" when
	// the field is string+date/date-time, leaving empty otherwise. The repo
	// uses empty == numeric for backward compat with all integer/number
	// range filters. Without this hint a range filter on a date column
	// would try to cast a date string to numeric and 500.
	CastExpr string
}

// IsRange reports whether the spec uses any of the four range operators.
// Used to gate type-validation (range allowed only on numeric/date fields).
func (s FilterSpec) IsRange() bool {
	return s.Gte != nil || s.Gt != nil || s.Lte != nil || s.Lt != nil
}

// IsEquality reports whether the spec uses only Eq (no operators set).
func (s FilterSpec) IsEquality() bool {
	return s.Eq != nil && len(s.In) == 0 && !s.IsRange()
}

// SortSpec describes one component of an ORDER BY clause. Multi-field sort
// arrays produce composite ordering (first entry primary, remaining tiebreak).
//
// Field must reference an `x-index: true` property in the schema — same
// whitelist that gates filters. Order is "asc" or "desc"; anything else is
// rejected as InvalidInput.
//
// Enum properties (string + enum: [...]) sort by *declaration order*
// (`array_position(ARRAY['v1','v2','v3'], data->>'field')`), not alphabetical.
// This is the contractual surprise documented in the tool description and
// guaranteed by integration test TestKG14_Sort_EnumByDeclarationOrder.
type SortSpec struct {
	Field string
	Order string // "asc" | "desc"

	// EnumValues is engine-populated — callers (HTTP handler, tool args)
	// MUST NOT set this. The usecase fills it from the schema's parsed
	// EnumValues so the repo can emit array_position-based ordering without
	// re-parsing the schema. Empty means natural sort on data->>'field'.
	EnumValues []string
}

const (
	SortOrderAsc  = "asc"
	SortOrderDesc = "desc"
)

// ListEntitiesQuery is the parameter object for the entity list endpoint.
type ListEntitiesQuery struct {
	TenantID   string
	BundleName string
	EntityType string
	Filters    map[string]FilterSpec
	Sort       []SortSpec
	Limit      int
	Offset     int
}

// --- Usecase ---

// Usecase aggregates the three read collaborators.
type Usecase struct {
	bundles  BundleReader
	schemas  SchemaReader
	entities EntityReader
}

// New constructs a Usecase. All readers are required.
func New(b BundleReader, s SchemaReader, e EntityReader) *Usecase {
	return &Usecase{bundles: b, schemas: s, entities: e}
}

// ListBundles returns all bundles for the tenant.
func (u *Usecase) ListBundles(ctx context.Context, tenantID string) ([]*domain.KGBundle, error) {
	t, err := resolveTenantID(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out, err := u.bundles.List(ctx, t)
	if err != nil {
		return nil, fmt.Errorf("list bundles: %w", err)
	}
	return out, nil
}

// GetBundle returns one bundle. Returns NotFound from the reader when absent.
func (u *Usecase) GetBundle(ctx context.Context, tenantID, bundleName string) (*domain.KGBundle, error) {
	t, err := resolveTenantID(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if !domain.ValidKGBundleName(bundleName) {
		return nil, pkgerrors.InvalidInput(fmt.Sprintf("invalid bundle name %q", bundleName))
	}
	return u.bundles.Get(ctx, t, bundleName)
}

// ListSchemas returns all schemas in the given bundle.
func (u *Usecase) ListSchemas(ctx context.Context, tenantID, bundleName string) ([]*domain.KGEntitySchema, error) {
	t, err := resolveTenantID(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if !domain.ValidKGBundleName(bundleName) {
		return nil, pkgerrors.InvalidInput(fmt.Sprintf("invalid bundle name %q", bundleName))
	}
	out, err := u.schemas.ListByBundle(ctx, t, bundleName)
	if err != nil {
		return nil, fmt.Errorf("list schemas: %w", err)
	}
	return out, nil
}

// GetSchema returns one schema by entity type.
func (u *Usecase) GetSchema(ctx context.Context, tenantID, bundleName, entityType string) (*domain.KGEntitySchema, error) {
	t, err := resolveTenantID(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if !domain.ValidKGBundleName(bundleName) {
		return nil, pkgerrors.InvalidInput(fmt.Sprintf("invalid bundle name %q", bundleName))
	}
	if !domain.ValidKGEntityType(entityType) {
		return nil, pkgerrors.InvalidInput(fmt.Sprintf("invalid entity_type %q", entityType))
	}
	return u.schemas.Get(ctx, t, bundleName, entityType)
}

// ListEntities returns a paginated slice of entities under one entity type,
// optionally filtered by indexed fields. Filters that reference non-indexed
// fields are rejected as InvalidInput before the reader is touched.
func (u *Usecase) ListEntities(ctx context.Context, q ListEntitiesQuery) ([]*domain.KGEntity, int, error) {
	t, err := resolveTenantID(ctx, q.TenantID)
	if err != nil {
		return nil, 0, err
	}
	if !domain.ValidKGBundleName(q.BundleName) {
		return nil, 0, pkgerrors.InvalidInput(fmt.Sprintf("invalid bundle name %q", q.BundleName))
	}
	if !domain.ValidKGEntityType(q.EntityType) {
		return nil, 0, pkgerrors.InvalidInput(fmt.Sprintf("invalid entity_type %q", q.EntityType))
	}
	if q.Offset < 0 {
		return nil, 0, pkgerrors.InvalidInput("offset must be non-negative")
	}

	// Normalise pagination.
	if q.Limit <= 0 {
		q.Limit = DefaultListLimit
	} else if q.Limit > MaxListLimit {
		q.Limit = MaxListLimit
	}

	// Resolve schema once if EITHER filters OR sort are present — both need
	// the indexed-field whitelist (and filters also need FieldTypes for
	// range-operator gating). Sort additionally consults EnumValues so the
	// repo can emit `array_position(...)` for declaration-order enums.
	if len(q.Filters) > 0 || len(q.Sort) > 0 {
		schema, err := u.schemas.Get(ctx, t, q.BundleName, q.EntityType)
		if err != nil {
			return nil, 0, err
		}
		indexed, types, err := filterableFieldsFromSchema(schema)
		if err != nil {
			return nil, 0, err
		}
		if len(q.Filters) > 0 {
			if err := validateFilterSpecs(q.Filters, indexed, types); err != nil {
				return nil, 0, err
			}
		}
		if len(q.Sort) > 0 {
			if err := validateSortSpecs(q.Sort, indexed); err != nil {
				return nil, 0, err
			}
			// Enrich sort specs with EnumValues so the repo can emit
			// array_position-based ordering for declaration-order enum sort.
			ann, err := annotationsFromSchema(schema)
			if err != nil {
				return nil, 0, err
			}
			for i := range q.Sort {
				if values, ok := ann.EnumValues[q.Sort[i].Field]; ok {
					q.Sort[i].EnumValues = values
				}
			}
		}
	}

	q.TenantID = t
	ctx, cancel := context.WithTimeout(ctx, KGQueryTimeout)
	defer cancel()
	items, total, err := u.entities.ListEntities(ctx, q)
	if err != nil {
		return nil, 0, fmt.Errorf("list entities: %w", err)
	}
	slog.DebugContext(ctx, "kg list entities",
		"tenant_id", t, "bundle", q.BundleName, "entity_type", q.EntityType,
		"count", len(items), "total", total)
	return items, total, nil
}

// GetEntities returns matching entities by composite ID list with partial
// success semantics — missing IDs do not fail the call, they appear in
// NotFound. Order in both Entities and NotFound matches the (deduplicated)
// input array.
//
// Edge case contract (matches the partner integration test fixtures):
//   - empty ids → InvalidInput
//   - >500 ids → InvalidInput
//   - duplicate ids → silently de-duplicated before query
//   - all ids missing → (BatchGetResult{Entities:nil, NotFound: input}, nil)
func (u *Usecase) GetEntities(
	ctx context.Context,
	tenantID, bundleName, entityType string,
	ids []string,
) (*BatchGetResult, error) {
	t, err := resolveTenantID(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if !domain.ValidKGBundleName(bundleName) {
		return nil, pkgerrors.InvalidInput(fmt.Sprintf("invalid bundle name %q", bundleName))
	}
	if !domain.ValidKGEntityType(entityType) {
		return nil, pkgerrors.InvalidInput(fmt.Sprintf("invalid entity_type %q", entityType))
	}
	if len(ids) == 0 {
		return nil, pkgerrors.InvalidInput("ids must contain at least one element")
	}
	if len(ids) > MaxBatchGetIDs {
		return nil, pkgerrors.InvalidInput(fmt.Sprintf("ids count %d exceeds max %d", len(ids), MaxBatchGetIDs))
	}
	// Dedup while preserving input order. Reject obviously bad ids
	// (empty / length cap) up-front so we don't waste a DB roundtrip.
	seen := make(map[string]struct{}, len(ids))
	dedup := make([]string, 0, len(ids))
	for i, id := range ids {
		if id == "" {
			return nil, pkgerrors.InvalidInput(fmt.Sprintf("ids[%d] is empty", i))
		}
		if len(id) > domain.KGEntityMaxIDLength {
			return nil, pkgerrors.InvalidInput(fmt.Sprintf("ids[%d] length %d exceeds max %d", i, len(id), domain.KGEntityMaxIDLength))
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		dedup = append(dedup, id)
	}

	ctx, cancel := context.WithTimeout(ctx, KGQueryTimeout)
	defer cancel()
	found, notFound, err := u.entities.GetEntities(ctx, t, bundleName, entityType, dedup)
	if err != nil {
		return nil, fmt.Errorf("batch get entities: %w", err)
	}
	return &BatchGetResult{Entities: found, NotFound: notFound}, nil
}

// GetEntity returns one entity by id.
func (u *Usecase) GetEntity(ctx context.Context, tenantID, bundleName, entityType, entityID string) (*domain.KGEntity, error) {
	t, err := resolveTenantID(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if !domain.ValidKGBundleName(bundleName) {
		return nil, pkgerrors.InvalidInput(fmt.Sprintf("invalid bundle name %q", bundleName))
	}
	if !domain.ValidKGEntityType(entityType) {
		return nil, pkgerrors.InvalidInput(fmt.Sprintf("invalid entity_type %q", entityType))
	}
	if entityID == "" {
		return nil, pkgerrors.InvalidInput("entity_id is required")
	}
	if len(entityID) > domain.KGEntityMaxIDLength {
		return nil, pkgerrors.InvalidInput(fmt.Sprintf("entity_id length %d exceeds max %d", len(entityID), domain.KGEntityMaxIDLength))
	}
	return u.entities.GetEntity(ctx, t, bundleName, entityType, entityID)
}

// --- helpers ---

// resolveTenantID returns explicit tenantID, falling back to context. Empty
// result is an InvalidInput error.
func resolveTenantID(ctx context.Context, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if t := domain.TenantIDFromContext(ctx); t != "" {
		return t, nil
	}
	return "", pkgerrors.InvalidInput("tenant_id required (from input or context)")
}
