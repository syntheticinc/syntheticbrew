package kgtools

import (
	"context"
	"sort"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// ToolNameSource is a single source of currently-registered tool names for
// a tenant. The collision detector aggregates names from all configured
// sources to spot conflicts before a new schema's tools are added.
//
// excludeBundle is the bundle currently being applied — sources that span
// the tenant's existing KG schemas use it to skip self-collision when a
// bundle is re-applied. Sources that don't track bundle scope (static
// builtins, MCP tools) ignore the parameter.
//
// Implementations live next to the sources they wrap:
//   - capability tools (built-in memory/knowledge) → constant slice
//   - MCP server tools (per-tenant MCP Manager)    → MCPNameLister
//   - existing KG bundles                          → DBSchemaToolNames (DB) / RegistryToolNames (in-memory)
type ToolNameSource interface {
	ToolNamesForTenant(ctx context.Context, tenantID, excludeBundle string) ([]string, error)
}

// CollisionDetector enforces tool-name uniqueness inside a tenant. It checks
// new tool names against every configured ToolNameSource and reports the
// subset that already exists.
type CollisionDetector struct {
	sources []ToolNameSource
}

// NewCollisionDetector constructs a detector backed by the given sources.
// The order is irrelevant — every source contributes to the union.
func NewCollisionDetector(sources ...ToolNameSource) *CollisionDetector {
	// Make a defensive copy so callers cannot mutate the slice underneath.
	cp := make([]ToolNameSource, len(sources))
	copy(cp, sources)
	return &CollisionDetector{sources: cp}
}

// Detect returns the subset of newToolNames already in use by any other
// source for the tenant. excludeBundle scopes-out the bundle currently being
// applied so re-applying does not produce a self-collision. Returns nil + nil
// error when there are no collisions.
func (d *CollisionDetector) Detect(ctx context.Context, tenantID, excludeBundle string, newToolNames []string) ([]string, error) {
	if len(newToolNames) == 0 {
		return nil, nil
	}
	existing := make(map[string]struct{})
	for _, src := range d.sources {
		names, err := src.ToolNamesForTenant(ctx, tenantID, excludeBundle)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			existing[n] = struct{}{}
		}
	}
	colliding := make([]string, 0)
	for _, n := range newToolNames {
		if _, ok := existing[n]; ok {
			colliding = append(colliding, n)
		}
	}
	sort.Strings(colliding)
	if len(colliding) == 0 {
		return nil, nil
	}
	return colliding, nil
}

// StaticToolNames is a ToolNameSource that always returns the same set,
// regardless of tenant. Used for built-in capability tools and engine
// builtins (memory_recall, memory_store, knowledge_search, etc.) which
// are universal.
type StaticToolNames struct {
	Names []string
}

func (s StaticToolNames) ToolNamesForTenant(_ context.Context, _, _ string) ([]string, error) {
	return s.Names, nil
}

// RegistryToolNames adapts a *Registry into a ToolNameSource. It returns
// the union of tool names from every cached bundle in the registry — used
// to spot collisions against bundles whose tools are already loaded into
// the in-memory registry. Note: the registry is lazy-loaded, so a fresh
// engine that has not yet served chat traffic returns an empty set. Pair
// with DBSchemaToolNames for authoritative cross-bundle coverage.
type RegistryToolNames struct {
	Provider *Provider
}

func (r RegistryToolNames) ToolNamesForTenant(ctx context.Context, tenantID, excludeBundle string) ([]string, error) {
	if r.Provider == nil {
		return nil, nil
	}
	reg, err := r.Provider.GetForTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return reg.AllToolNamesForTenantExceptBundle(excludeBundle), nil
}

// SchemaToolLister reads all KG schemas across a tenant's bundles —
// typically implemented by configrepo.GORMKGSchemaRepository.ListAllExceptBundle.
type SchemaToolLister interface {
	ListAllExceptBundle(ctx context.Context, tenantID, excludeBundle string) ([]*domain.KGEntitySchema, error)
}

// DBSchemaToolNames is a ToolNameSource backed by the persistence layer. It
// is the authoritative source of cross-bundle collision detection because it
// sees every persisted schema for the tenant, not just those cached in the
// in-memory registry.
type DBSchemaToolNames struct {
	Lister SchemaToolLister
}

func (d DBSchemaToolNames) ToolNamesForTenant(ctx context.Context, tenantID, excludeBundle string) ([]string, error) {
	if d.Lister == nil {
		return nil, nil
	}
	schemas, err := d.Lister.ListAllExceptBundle(ctx, tenantID, excludeBundle)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, s := range schemas {
		for _, t := range s.ToolNames() {
			if _, dup := seen[t]; dup {
				continue
			}
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out, nil
}
