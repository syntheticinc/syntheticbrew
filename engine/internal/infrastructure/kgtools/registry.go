// Package kgtools wires Knowledge Graph schemas to MCP-style auto-generated
// tools at runtime. The Provider holds a per-tenant Registry of schemas
// loaded from the database, and exposes a ResolveToolsForBundles method
// that the capabilities.KnowledgeGraphsCapability calls every chat turn.
package kgtools

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// SchemaReader is the minimal interface kgtools needs to load schemas from
// the persistence layer. Implemented by configrepo.GORMKGSchemaRepository.
type SchemaReader interface {
	// ListByBundle returns all schemas for one bundle (tenant + bundle).
	ListByBundle(ctx context.Context, tenantID, bundleName string) ([]*domain.KGEntitySchema, error)
}

// AgentBundleReader resolves the list of bundles an agent is bound to.
// Implemented by configrepo (reads capabilities.config -> 'bundles' for the
// agent's knowledge_graphs capability row).
type AgentBundleReader interface {
	BundlesForAgent(ctx context.Context, tenantID, agentID string) ([]string, error)
}

// Registry holds the per-tenant tool registry, indexed by bundle name.
// Each entry caches the entity schemas + derived tool names for that bundle.
type Registry struct {
	mu      sync.RWMutex
	bundles map[string]*bundleEntry
}

type bundleEntry struct {
	schemas []*domain.KGEntitySchema
	tools   []string // sorted, deduplicated
}

// NewRegistry constructs an empty per-tenant registry.
func NewRegistry() *Registry {
	return &Registry{bundles: make(map[string]*bundleEntry)}
}

// Set replaces the cached entry for one bundle. Called by the Provider when
// a bundle is first loaded for a tenant.
func (r *Registry) Set(bundleName string, schemas []*domain.KGEntitySchema) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bundles[bundleName] = newBundleEntry(schemas)
}

// Invalidate drops the cached entry for one bundle. Called when the bundle
// is updated through the apply/mutate path.
func (r *Registry) Invalidate(bundleName string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.bundles, bundleName)
}

// ToolsForBundles returns the deduplicated sorted tool list aggregated over
// the named bundles. Bundle names that have no cached entry are skipped
// (caller is expected to populate the cache via the Provider).
func (r *Registry) ToolsForBundles(bundles []string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, b := range bundles {
		entry, ok := r.bundles[b]
		if !ok {
			continue
		}
		for _, t := range entry.tools {
			if _, dup := seen[t]; dup {
				continue
			}
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// AllToolNamesForTenant returns the union of tool names currently cached for
// the tenant. Used by the collision detector to spot conflicts at apply time.
func (r *Registry) AllToolNamesForTenant() []string {
	return r.AllToolNamesForTenantExceptBundle("")
}

// AllToolNamesForTenantExceptBundle returns the union of tool names currently
// cached for the tenant, skipping any bundle whose name matches excludeBundle.
// The collision detector passes the bundle being re-applied so its own cached
// tools do not register as self-collisions.
func (r *Registry) AllToolNamesForTenantExceptBundle(excludeBundle string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for name, entry := range r.bundles {
		if name == excludeBundle {
			continue
		}
		for _, t := range entry.tools {
			if _, dup := seen[t]; dup {
				continue
			}
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

func newBundleEntry(schemas []*domain.KGEntitySchema) *bundleEntry {
	seen := make(map[string]struct{})
	tools := make([]string, 0)
	for _, s := range schemas {
		for _, t := range s.ToolNames() {
			if _, dup := seen[t]; dup {
				continue
			}
			seen[t] = struct{}{}
			tools = append(tools, t)
		}
	}
	sort.Strings(tools)
	return &bundleEntry{schemas: schemas, tools: tools}
}

// schemaFmtError wraps a non-domain error with context for clearer logs.
func schemaFmtError(action string, err error) error {
	return fmt.Errorf("kgtools.%s: %w", action, err)
}
