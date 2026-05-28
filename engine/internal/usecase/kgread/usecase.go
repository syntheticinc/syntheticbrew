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

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// Default and maximum page sizes for ListEntities. Exported so HTTP delivery
// can advertise the same numbers in API documentation.
const (
	DefaultListLimit = 50
	MaxListLimit     = 500
)

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
type EntityReader interface {
	ListEntities(ctx context.Context, q ListEntitiesQuery) (items []*domain.KGEntity, total int, err error)
	GetEntity(ctx context.Context, tenantID, bundleName, entityType, entityID string) (*domain.KGEntity, error)
}

// ListEntitiesQuery is the parameter object for the entity list endpoint.
type ListEntitiesQuery struct {
	TenantID   string
	BundleName string
	EntityType string
	Filters    map[string]any
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

	// Resolve schema to enforce filter-field whitelist. Pass the resolved
	// tenant_id explicitly to avoid double-lookup against ctx.
	if len(q.Filters) > 0 {
		schema, err := u.schemas.Get(ctx, t, q.BundleName, q.EntityType)
		if err != nil {
			return nil, 0, err
		}
		indexed, err := indexedFieldsFromSchema(schema)
		if err != nil {
			return nil, 0, err
		}
		for k := range q.Filters {
			if _, ok := indexed[k]; !ok {
				return nil, 0, pkgerrors.InvalidInput(fmt.Sprintf("filter field %q is not indexed (allowed: %v)", k, sortedKeys(indexed)))
			}
		}
	}

	q.TenantID = t
	items, total, err := u.entities.ListEntities(ctx, q)
	if err != nil {
		return nil, 0, fmt.Errorf("list entities: %w", err)
	}
	slog.DebugContext(ctx, "kg list entities",
		"tenant_id", t, "bundle", q.BundleName, "entity_type", q.EntityType,
		"count", len(items), "total", total)
	return items, total, nil
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
