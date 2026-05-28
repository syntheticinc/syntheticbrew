// Package kgapply implements the atomic bulk-apply usecase for Knowledge
// Graph bundles. A customer pushes one bundle (schemas + entities); the
// engine validates every schema (annotations + cross-refs + tool collisions)
// and every entity (JSON Schema + id-field) before persisting all rows in a
// single transaction. The bundle apply is atomic: either everything lands or
// nothing changes.
package kgapply

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
	"github.com/syntheticinc/syntheticbrew/pkg/jsonschema"
)

// MaxBundleDataBytes caps the total entity-data size for a single bundle
// import. 10 MB matches the plan target and protects against accidental
// pathological bundles that would still pass the per-entity 100 KB cap.
const MaxBundleDataBytes = 10 * 1024 * 1024

// --- Consumer-side interfaces (implementations live in infrastructure) ---

// BundleRepository persists the bundle aggregate (manifest + version).
type BundleRepository interface {
	UpsertBundle(ctx context.Context, b *domain.KGBundle) error
	DeleteBundle(ctx context.Context, tenantID, bundleName string) error
}

// SchemaRepository persists entity schemas keyed by
// (tenant_id, bundle_name, entity_type).
type SchemaRepository interface {
	UpsertSchemas(ctx context.Context, tenantID, bundleName string, schemas []*domain.KGEntitySchema) error
	ListByBundle(ctx context.Context, tenantID, bundleName string) ([]*domain.KGEntitySchema, error)
}

// EntityRepository persists entities. ReplaceEntities does delete-then-insert
// of all entities under (tenant_id, bundle_name) within the calling
// transaction.
type EntityRepository interface {
	ReplaceEntities(ctx context.Context, tenantID, bundleName string, entities []*domain.KGEntity) error
}

// SchemaValidator validates an entity's data against its JSON Schema.
// Implementations wrap an external library (santhosh-tekuri/jsonschema).
type SchemaValidator interface {
	Validate(ctx context.Context, schemaJSON []byte, entityData []byte) error
}

// CollisionDetector reports which of the new tool names collide with
// existing capability, MCP, or other-bundle tools for the given tenant.
// excludeBundle lets the detector skip the bundle being applied so that
// re-applying a bundle does not flag its own previous schemas as collisions.
type CollisionDetector interface {
	Detect(ctx context.Context, tenantID, excludeBundle string, newToolNames []string) ([]string, error)
}

// QuotaEnforcer gates writes by tenant quota. Cloud implementations call the
// metering service; CE/EE may pass a nil enforcer to the Usecase constructor.
type QuotaEnforcer interface {
	OnEntityWrite(ctx context.Context, tenantID, bundleName string, deltaEntities int, deltaBytes int64) error
}

// AdvisoryLocker provides per-(tenant, bundle) serialisation around the
// apply operation. PostgreSQL implementation uses pg_advisory_xact_lock keyed
// by hash(tenant_id, bundle_name).
type AdvisoryLocker interface {
	LockBundle(ctx context.Context, tenantID, bundleName string) (unlock func(), err error)
}

// TransactionRunner runs fn inside a database transaction. Implementations
// inject a tx-bound handle into ctx so repositories pick it up via context.
type TransactionRunner interface {
	InTransaction(ctx context.Context, fn func(ctx context.Context) error) error
}

// BundleInvalidator is invoked after a successful apply to drop cached
// schemas + tool lists for the bundle so the next chat session sees the new
// tools. Production implementation wraps kgtools.Provider.InvalidateBundle.
// Nil is valid (no-op) for tests / pre-existing CE bootstraps.
type BundleInvalidator interface {
	InvalidateBundle(tenantID, bundleName string)
}

// --- Input / Output ---

// SchemaInput is one entity-type schema in a bundle apply request.
type SchemaInput struct {
	EntityType      string
	SchemaJSON      []byte
	ExposeTools     []string // optional override; empty => use annotations / default ["list","get"]
	ToolDescription string   // optional override; empty => use annotation / fallback
}

// EntitySetInput groups entities of one type to be applied. Items are raw
// maps; the usecase marshals them to JSON before validation/persistence.
type EntitySetInput struct {
	EntityType string
	Items      []map[string]any
}

// Input is the full bundle apply payload.
type Input struct {
	TenantID   string // optional; falls back to domain.TenantIDFromContext(ctx)
	BundleName string
	Version    string
	Schemas    []SchemaInput
	Entities   []EntitySetInput
}

// Output summarises a successful apply.
type Output struct {
	BundleName     string
	Version        string
	SchemasApplied int
	EntitiesWritten int
}

// --- Usecase ---

// Usecase orchestrates the bundle apply. All collaborators are interfaces;
// the production wiring lives in app.NewServer.
type Usecase struct {
	bundleRepo  BundleRepository
	schemaRepo  SchemaRepository
	entityRepo  EntityRepository
	validator   SchemaValidator
	collision   CollisionDetector
	enforcer    QuotaEnforcer // nullable (CE/EE)
	locker      AdvisoryLocker
	txRunner    TransactionRunner
	invalidator BundleInvalidator // nullable; production wires kgtools.Provider
}

// New constructs a Usecase. enforcer may be nil to disable quota enforcement
// (CE / EE on-prem). Every other dependency is required.
func New(
	bundleRepo BundleRepository,
	schemaRepo SchemaRepository,
	entityRepo EntityRepository,
	validator SchemaValidator,
	collision CollisionDetector,
	enforcer QuotaEnforcer,
	locker AdvisoryLocker,
	txRunner TransactionRunner,
) *Usecase {
	return &Usecase{
		bundleRepo: bundleRepo,
		schemaRepo: schemaRepo,
		entityRepo: entityRepo,
		validator:  validator,
		collision:  collision,
		enforcer:   enforcer,
		locker:     locker,
		txRunner:   txRunner,
	}
}

// SetInvalidator wires a BundleInvalidator into the usecase. Called once at
// server startup after the kgtools.Provider is constructed. Decoupled from
// the New constructor so the cyclic dependency between provider (which needs
// schema reads) and apply (which invalidates the provider) is avoided.
func (u *Usecase) SetInvalidator(inv BundleInvalidator) {
	u.invalidator = inv
}

// Execute validates the entire payload, then persists schemas + entities in
// one transaction under a per-(tenant, bundle) advisory lock. Returns an
// *Output summary on success; returns a typed pkgerrors.DomainError on
// validation failures and wrapped errors on infrastructure failures.
func (u *Usecase) Execute(ctx context.Context, in Input) (*Output, error) {
	tenantID, err := resolveTenantID(ctx, in.TenantID)
	if err != nil {
		return nil, err
	}

	if !domain.ValidKGBundleName(in.BundleName) {
		return nil, pkgerrors.InvalidInput(fmt.Sprintf("invalid bundle name %q", in.BundleName))
	}
	if in.Version == "" {
		return nil, pkgerrors.InvalidInput("version is required")
	}
	if len(in.Schemas) == 0 {
		return nil, pkgerrors.InvalidInput("at least one schema is required")
	}

	unlock, err := u.locker.LockBundle(ctx, tenantID, in.BundleName)
	if err != nil {
		return nil, fmt.Errorf("acquire bundle lock: %w", err)
	}
	defer unlock()

	schemasByType, annsByType, allToolNames, err := u.buildSchemas(tenantID, in)
	if err != nil {
		return nil, err
	}

	if err := u.validateCrossRefs(ctx, tenantID, in.BundleName, annsByType, schemasByType); err != nil {
		return nil, err
	}

	collisions, err := u.collision.Detect(ctx, tenantID, in.BundleName, allToolNames)
	if err != nil {
		return nil, fmt.Errorf("detect collisions: %w", err)
	}
	if len(collisions) > 0 {
		sort.Strings(collisions)
		return nil, pkgerrors.AlreadyExists(fmt.Sprintf("tool name collision: %v", collisions))
	}

	entitiesByType, totalEntities, totalBytes, err := u.buildEntities(ctx, tenantID, in, schemasByType)
	if err != nil {
		return nil, err
	}

	if totalBytes > MaxBundleDataBytes {
		return nil, pkgerrors.InvalidInput(fmt.Sprintf(
			"bundle entities total %d bytes exceeds max %d bytes (%d MB)",
			totalBytes, MaxBundleDataBytes, MaxBundleDataBytes/(1024*1024)))
	}

	if u.enforcer != nil {
		if err := u.enforcer.OnEntityWrite(ctx, tenantID, in.BundleName, totalEntities, totalBytes); err != nil {
			return nil, fmt.Errorf("quota check: %w", err)
		}
	}

	bundle, err := domain.NewKGBundle(tenantID, in.BundleName, in.Version, buildManifest(schemasByType, entitiesByType))
	if err != nil {
		return nil, pkgerrors.InvalidInput(fmt.Sprintf("build bundle: %v", err))
	}

	if hasCycle(annsByType) {
		slog.WarnContext(ctx, "kg cross-ref cycle detected",
			"tenant_id", tenantID, "bundle", in.BundleName)
	}

	err = u.txRunner.InTransaction(ctx, func(ctx context.Context) error {
		if err := u.bundleRepo.UpsertBundle(ctx, bundle); err != nil {
			return fmt.Errorf("upsert bundle: %w", err)
		}
		if err := u.schemaRepo.UpsertSchemas(ctx, tenantID, in.BundleName, flattenSchemas(schemasByType)); err != nil {
			return fmt.Errorf("upsert schemas: %w", err)
		}
		if err := u.entityRepo.ReplaceEntities(ctx, tenantID, in.BundleName, flattenEntities(entitiesByType)); err != nil {
			return fmt.Errorf("replace entities: %w", err)
		}
		return nil
	})
	if err != nil {
		slog.ErrorContext(ctx, "kg bundle apply failed",
			"error", err, "tenant_id", tenantID, "bundle", in.BundleName, "version", in.Version)
		return nil, err
	}

	// Drop cached schemas/tools for this bundle so the next chat session
	// resolves the freshly-applied schema. Cached tool list of in-flight
	// sessions remains the previous version (MVP — no hot reload).
	if u.invalidator != nil {
		u.invalidator.InvalidateBundle(tenantID, in.BundleName)
	}

	slog.InfoContext(ctx, "kg bundle applied",
		"tenant_id", tenantID, "bundle", in.BundleName,
		"version", in.Version, "schemas", len(schemasByType), "entities", totalEntities)

	return &Output{
		BundleName:      in.BundleName,
		Version:         in.Version,
		SchemasApplied:  len(schemasByType),
		EntitiesWritten: totalEntities,
	}, nil
}

// --- helpers ---

// resolveTenantID returns the explicit input tenantID, falling back to ctx.
// Empty result is an InvalidInput error.
func resolveTenantID(ctx context.Context, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if t := domain.TenantIDFromContext(ctx); t != "" {
		return t, nil
	}
	return "", pkgerrors.InvalidInput("tenant_id required (from input or context)")
}

// buildSchemas parses every input schema, builds domain entities, and
// collects the union of all auto-generated tool names.
func (u *Usecase) buildSchemas(tenantID string, in Input) (
	map[string]*domain.KGEntitySchema,
	map[string]*jsonschema.Annotations,
	[]string,
	error,
) {
	schemasByType := make(map[string]*domain.KGEntitySchema, len(in.Schemas))
	annsByType := make(map[string]*jsonschema.Annotations, len(in.Schemas))
	allToolNames := make([]string, 0, len(in.Schemas)*3)

	for _, s := range in.Schemas {
		if !domain.ValidKGEntityType(s.EntityType) {
			return nil, nil, nil, pkgerrors.InvalidInput(fmt.Sprintf("invalid entity_type %q", s.EntityType))
		}
		if _, exists := schemasByType[s.EntityType]; exists {
			return nil, nil, nil, pkgerrors.InvalidInput(fmt.Sprintf("duplicate entity_type %q in input", s.EntityType))
		}

		ann, err := jsonschema.ParseAnnotations(s.SchemaJSON)
		if err != nil {
			return nil, nil, nil, pkgerrors.InvalidInput(fmt.Sprintf("parse schema for %q: %v", s.EntityType, err))
		}

		exposeTools := s.ExposeTools
		if len(exposeTools) == 0 {
			exposeTools = ann.ExposeTools
		}
		desc := s.ToolDescription
		if desc == "" {
			desc = ann.ToolDescription
		}

		ks, err := domain.NewKGEntitySchema(tenantID, in.BundleName, s.EntityType, s.SchemaJSON, ann.IDField, exposeTools, desc)
		if err != nil {
			return nil, nil, nil, pkgerrors.InvalidInput(fmt.Sprintf("build schema %q: %v", s.EntityType, err))
		}

		schemasByType[s.EntityType] = ks
		annsByType[s.EntityType] = ann
		allToolNames = append(allToolNames, ks.ToolNames()...)
	}
	return schemasByType, annsByType, allToolNames, nil
}

// validateCrossRefs ensures every x-ref target resolves to an entity_type
// either in this import batch or already persisted in the bundle.
func (u *Usecase) validateCrossRefs(
	ctx context.Context,
	tenantID, bundleName string,
	annsByType map[string]*jsonschema.Annotations,
	schemasByType map[string]*domain.KGEntitySchema,
) error {
	existing, err := u.schemaRepo.ListByBundle(ctx, tenantID, bundleName)
	if err != nil {
		return fmt.Errorf("list existing schemas: %w", err)
	}
	known := make(map[string]struct{}, len(schemasByType)+len(existing))
	for k := range schemasByType {
		known[k] = struct{}{}
	}
	for _, e := range existing {
		known[e.EntityType] = struct{}{}
	}
	// Iterate in deterministic order so error messages are stable for tests.
	types := make([]string, 0, len(annsByType))
	for t := range annsByType {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, t := range types {
		ann := annsByType[t]
		for _, ref := range ann.Refs {
			if _, ok := known[ref.TargetType]; !ok {
				return pkgerrors.InvalidInput(fmt.Sprintf(
					"schema %q references unknown entity_type %q (property %q)",
					t, ref.TargetType, ref.Property))
			}
		}
	}
	return nil
}

// buildEntities marshals + validates every input entity and constructs
// domain.KGEntity rows. Returns totals for quota accounting.
func (u *Usecase) buildEntities(
	ctx context.Context,
	tenantID string,
	in Input,
	schemasByType map[string]*domain.KGEntitySchema,
) (map[string][]*domain.KGEntity, int, int64, error) {
	entitiesByType := make(map[string][]*domain.KGEntity, len(in.Entities))
	total := 0
	var totalBytes int64

	for _, set := range in.Entities {
		sch, ok := schemasByType[set.EntityType]
		if !ok {
			return nil, 0, 0, pkgerrors.InvalidInput(fmt.Sprintf("entities reference unknown entity_type %q", set.EntityType))
		}
		seenIDs := make(map[string]struct{}, len(set.Items))
		for i, item := range set.Items {
			idVal, ok := item[sch.IDField]
			if !ok {
				return nil, 0, 0, pkgerrors.InvalidInput(fmt.Sprintf("%s[%d] missing id field %q", set.EntityType, i, sch.IDField))
			}
			idStr, ok := idVal.(string)
			if !ok {
				return nil, 0, 0, pkgerrors.InvalidInput(fmt.Sprintf("%s[%d] id field %q must be string", set.EntityType, i, sch.IDField))
			}
			if _, dup := seenIDs[idStr]; dup {
				return nil, 0, 0, pkgerrors.InvalidInput(fmt.Sprintf("duplicate entity_id %q in %s", idStr, set.EntityType))
			}
			seenIDs[idStr] = struct{}{}

			dataBytes, err := json.Marshal(item)
			if err != nil {
				return nil, 0, 0, pkgerrors.InvalidInput(fmt.Sprintf("marshal %s[%d]: %v", set.EntityType, i, err))
			}
			if err := u.validator.Validate(ctx, sch.SchemaJSON, dataBytes); err != nil {
				return nil, 0, 0, pkgerrors.InvalidInput(fmt.Sprintf("validate %s[%d] (id=%s): %v", set.EntityType, i, idStr, err))
			}
			ent, err := domain.NewKGEntity(tenantID, in.BundleName, set.EntityType, idStr, dataBytes, sch.SchemaHash)
			if err != nil {
				return nil, 0, 0, pkgerrors.InvalidInput(fmt.Sprintf("build entity %s[%d]: %v", set.EntityType, i, err))
			}
			entitiesByType[set.EntityType] = append(entitiesByType[set.EntityType], ent)
			total++
			totalBytes += int64(len(dataBytes))
		}
	}
	return entitiesByType, total, totalBytes, nil
}

// buildManifest captures a deterministic summary of the bundle for storage on
// the kg_bundle row. Used by the admin UI to render a bundle's contents
// without scanning kg_entity / kg_entity_schema tables.
func buildManifest(
	schemas map[string]*domain.KGEntitySchema,
	entities map[string][]*domain.KGEntity,
) map[string]any {
	types := make([]string, 0, len(schemas))
	for t := range schemas {
		types = append(types, t)
	}
	sort.Strings(types)

	out := make([]map[string]any, 0, len(types))
	for _, t := range types {
		out = append(out, map[string]any{
			"entity_type":  t,
			"schema_hash":  schemas[t].SchemaHash,
			"entity_count": len(entities[t]),
		})
	}
	return map[string]any{
		"entity_types": out,
	}
}

// flattenSchemas returns schemas in deterministic order (sorted by entity_type).
func flattenSchemas(byType map[string]*domain.KGEntitySchema) []*domain.KGEntitySchema {
	types := make([]string, 0, len(byType))
	for t := range byType {
		types = append(types, t)
	}
	sort.Strings(types)
	out := make([]*domain.KGEntitySchema, 0, len(types))
	for _, t := range types {
		out = append(out, byType[t])
	}
	return out
}

// flattenEntities returns entities in deterministic order (sorted by
// entity_type, then by entity_id within each type).
func flattenEntities(byType map[string][]*domain.KGEntity) []*domain.KGEntity {
	types := make([]string, 0, len(byType))
	for t := range byType {
		types = append(types, t)
	}
	sort.Strings(types)
	var out []*domain.KGEntity
	for _, t := range types {
		ents := byType[t]
		sort.Slice(ents, func(i, j int) bool { return ents[i].EntityID < ents[j].EntityID })
		out = append(out, ents...)
	}
	return out
}

// hasCycle reports whether the cross-ref graph built from annsByType has a
// directed cycle. Self-references count. Returns true on first detected back
// edge.
func hasCycle(annsByType map[string]*jsonschema.Annotations) bool {
	// Build adjacency list deterministic order.
	nodes := make([]string, 0, len(annsByType))
	for t := range annsByType {
		nodes = append(nodes, t)
	}
	sort.Strings(nodes)

	adj := make(map[string][]string, len(nodes))
	for _, t := range nodes {
		targets := make([]string, 0, len(annsByType[t].Refs))
		for _, r := range annsByType[t].Refs {
			targets = append(targets, r.TargetType)
		}
		adj[t] = targets
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)
	colour := make(map[string]int, len(nodes))

	var visit func(string) bool
	visit = func(n string) bool {
		colour[n] = gray
		for _, next := range adj[n] {
			switch colour[next] {
			case gray:
				return true
			case white:
				if visit(next) {
					return true
				}
			}
		}
		colour[n] = black
		return false
	}

	for _, n := range nodes {
		if colour[n] == white {
			if visit(n) {
				return true
			}
		}
	}
	return false
}
