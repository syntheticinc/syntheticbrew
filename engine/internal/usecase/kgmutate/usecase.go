// Package kgmutate implements granular CRUD usecases for individual Knowledge
// Graph entities, schemas, and entire bundles. These complement the atomic
// bulk-apply path (see kgapply): customers can add, update, or remove a single
// entity without re-applying the whole bundle, which is essential for ops
// teams without a CI/CD pipeline.
//
// Every mutation goes through the same validation pipeline as bulk apply
// (schema validation, quota enforcement, audit trail), so granular and bulk
// remain semantically equivalent — there is no path that bypasses the gates.
package kgmutate

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
	"github.com/syntheticinc/syntheticbrew/pkg/jsonschema"
)

// --- Consumer-side interfaces (implementations live in infrastructure) ---

// BundleRepository operates on bundle aggregates.
type BundleRepository interface {
	GetBundle(ctx context.Context, tenantID, bundleName string) (*domain.KGBundle, error)
	DeleteBundle(ctx context.Context, tenantID, bundleName string) error
	CountEntities(ctx context.Context, tenantID, bundleName string) (int, int64, error) // count, totalBytes
}

// SchemaRepository operates on entity-type schemas.
type SchemaRepository interface {
	GetSchema(ctx context.Context, tenantID, bundleName, entityType string) (*domain.KGEntitySchema, error)
	UpsertSchema(ctx context.Context, schema *domain.KGEntitySchema) error
}

// EntityRepository operates on entity instances.
type EntityRepository interface {
	GetEntity(ctx context.Context, tenantID, bundleName, entityType, entityID string) (*domain.KGEntity, error)
	UpsertEntity(ctx context.Context, entity *domain.KGEntity) error
	DeleteEntity(ctx context.Context, tenantID, bundleName, entityType, entityID string) error
}

// SchemaValidator validates an entity's data against its JSON Schema.
type SchemaValidator interface {
	Validate(ctx context.Context, schemaJSON []byte, entityData []byte) error
}

// CollisionDetector reports tool-name collisions inside a tenant. Used when
// upserting a schema (single-schema apply) since new tools may now clash with
// existing capability or MCP tools. excludeBundle lets the detector skip the
// bundle being upserted so re-applying does not flag its own existing schemas.
type CollisionDetector interface {
	Detect(ctx context.Context, tenantID, excludeBundle string, newToolNames []string) ([]string, error)
}

// QuotaEnforcer gates writes by tenant quota. nil enforcer => no enforcement
// (CE/EE default). Cloud implementations call the metering service.
type QuotaEnforcer interface {
	OnEntityWrite(ctx context.Context, tenantID, bundleName string, deltaEntities int, deltaBytes int64) error
}

// TransactionRunner runs fn inside a database transaction. Implementations
// inject a tx-bound handle into ctx so repositories pick it up via context.
type TransactionRunner interface {
	InTransaction(ctx context.Context, fn func(ctx context.Context) error) error
}

// BundleInvalidator is invoked after every mutation so the next chat session
// resolves fresh schemas + tool lists. Production wires kgtools.Provider.
// Nil is acceptable (no-op) — cached entries simply remain stale until the
// next process-level reload.
type BundleInvalidator interface {
	InvalidateBundle(tenantID, bundleName string)
}

// --- Input shapes ---

// CreateEntityInput describes a single-entity POST request.
type CreateEntityInput struct {
	TenantID   string
	BundleName string
	EntityType string
	Data       map[string]any
}

// UpdateEntityInput describes a single-entity PUT request. The entity_id is
// taken from the URL path (passed in EntityID); Data must contain the same
// value under the schema's x-id-field.
type UpdateEntityInput struct {
	TenantID   string
	BundleName string
	EntityType string
	EntityID   string
	Data       map[string]any
}

// UpsertSchemaInput describes a single-schema PUT request.
type UpsertSchemaInput struct {
	TenantID        string
	BundleName      string
	EntityType      string
	SchemaJSON      []byte
	ExposeTools     []string // optional override; empty => use annotations
	ToolDescription string
}

// --- Usecase ---

// Usecase coordinates granular Knowledge Graph mutations.
type Usecase struct {
	bundleRepo  BundleRepository
	schemaRepo  SchemaRepository
	entityRepo  EntityRepository
	validator   SchemaValidator
	collision   CollisionDetector
	enforcer    QuotaEnforcer // nullable
	txRunner    TransactionRunner
	invalidator BundleInvalidator // nullable; wired at startup
}

// SetInvalidator attaches a BundleInvalidator to the usecase. Called once at
// server startup; subsequent CRUD operations invalidate the per-bundle
// cache so the next chat session resolves fresh tools.
func (u *Usecase) SetInvalidator(inv BundleInvalidator) {
	u.invalidator = inv
}

func (u *Usecase) invalidate(tenantID, bundleName string) {
	if u.invalidator != nil {
		u.invalidator.InvalidateBundle(tenantID, bundleName)
	}
}

// New constructs the usecase with the given dependencies. enforcer may be nil
// to disable quota gates (CE/EE single-tenant default).
func New(
	bundleRepo BundleRepository,
	schemaRepo SchemaRepository,
	entityRepo EntityRepository,
	validator SchemaValidator,
	collision CollisionDetector,
	enforcer QuotaEnforcer,
	txRunner TransactionRunner,
) *Usecase {
	return &Usecase{
		bundleRepo: bundleRepo,
		schemaRepo: schemaRepo,
		entityRepo: entityRepo,
		validator:  validator,
		collision:  collision,
		enforcer:   enforcer,
		txRunner:   txRunner,
	}
}

// CreateEntity persists a new entity instance after schema validation and
// quota enforcement. Returns the materialised domain.KGEntity on success.
func (u *Usecase) CreateEntity(ctx context.Context, in CreateEntityInput) (*domain.KGEntity, error) {
	if err := validateContext(in.TenantID, in.BundleName, in.EntityType); err != nil {
		return nil, err
	}

	schema, err := u.schemaRepo.GetSchema(ctx, in.TenantID, in.BundleName, in.EntityType)
	if err != nil {
		return nil, fmt.Errorf("load schema %s/%s/%s: %w", in.TenantID, in.BundleName, in.EntityType, err)
	}
	if schema == nil {
		return nil, pkgerrors.NotFound(fmt.Sprintf("schema not found: %s/%s", in.BundleName, in.EntityType))
	}

	entity, err := u.buildEntity(ctx, schema, in.Data, "")
	if err != nil {
		return nil, err
	}

	if u.enforcer != nil {
		if err := u.enforcer.OnEntityWrite(ctx, in.TenantID, in.BundleName, 1, int64(len(entity.Data))); err != nil {
			return nil, fmt.Errorf("quota check failed: %w", err)
		}
	}

	if err := u.txRunner.InTransaction(ctx, func(txCtx context.Context) error {
		return u.entityRepo.UpsertEntity(txCtx, entity)
	}); err != nil {
		return nil, fmt.Errorf("persist entity: %w", err)
	}
	u.invalidate(entity.TenantID, entity.BundleName)

	slog.InfoContext(ctx, "kg entity created",
		"tenant_id", in.TenantID,
		"bundle_name", in.BundleName,
		"entity_type", in.EntityType,
		"entity_id", entity.EntityID,
	)
	return entity, nil
}

// UpdateEntity replaces an existing entity. The entity must exist; this path
// does not create a new entity. Use CreateEntity for that.
func (u *Usecase) UpdateEntity(ctx context.Context, in UpdateEntityInput) (*domain.KGEntity, error) {
	if err := validateContext(in.TenantID, in.BundleName, in.EntityType); err != nil {
		return nil, err
	}
	if in.EntityID == "" {
		return nil, pkgerrors.InvalidInput("entity_id is required")
	}

	schema, err := u.schemaRepo.GetSchema(ctx, in.TenantID, in.BundleName, in.EntityType)
	if err != nil {
		return nil, fmt.Errorf("load schema: %w", err)
	}
	if schema == nil {
		return nil, pkgerrors.NotFound(fmt.Sprintf("schema not found: %s/%s", in.BundleName, in.EntityType))
	}

	existing, err := u.entityRepo.GetEntity(ctx, in.TenantID, in.BundleName, in.EntityType, in.EntityID)
	if err != nil {
		return nil, fmt.Errorf("load existing entity: %w", err)
	}
	if existing == nil {
		return nil, pkgerrors.NotFound(fmt.Sprintf("entity not found: %s/%s/%s", in.BundleName, in.EntityType, in.EntityID))
	}

	entity, err := u.buildEntity(ctx, schema, in.Data, in.EntityID)
	if err != nil {
		return nil, err
	}
	if entity.EntityID != in.EntityID {
		return nil, pkgerrors.InvalidInput(fmt.Sprintf(
			"entity_id mismatch: URL says %q but data.%s says %q",
			in.EntityID, schema.IDField, entity.EntityID,
		))
	}

	if u.enforcer != nil {
		deltaBytes := int64(len(entity.Data)) - int64(len(existing.Data))
		if err := u.enforcer.OnEntityWrite(ctx, in.TenantID, in.BundleName, 0, deltaBytes); err != nil {
			return nil, fmt.Errorf("quota check failed: %w", err)
		}
	}

	if err := u.txRunner.InTransaction(ctx, func(txCtx context.Context) error {
		return u.entityRepo.UpsertEntity(txCtx, entity)
	}); err != nil {
		return nil, fmt.Errorf("persist entity: %w", err)
	}
	u.invalidate(entity.TenantID, entity.BundleName)

	slog.InfoContext(ctx, "kg entity updated",
		"tenant_id", in.TenantID,
		"bundle_name", in.BundleName,
		"entity_type", in.EntityType,
		"entity_id", entity.EntityID,
	)
	return entity, nil
}

// DeleteEntity removes one entity. Idempotent: returns nil if the entity does
// not exist (no quota effect either).
func (u *Usecase) DeleteEntity(ctx context.Context, tenantID, bundleName, entityType, entityID string) error {
	if err := validateContext(tenantID, bundleName, entityType); err != nil {
		return err
	}
	if entityID == "" {
		return pkgerrors.InvalidInput("entity_id is required")
	}

	existing, err := u.entityRepo.GetEntity(ctx, tenantID, bundleName, entityType, entityID)
	if err != nil {
		return fmt.Errorf("load existing entity: %w", err)
	}
	if existing == nil {
		return nil // idempotent
	}

	if u.enforcer != nil {
		if err := u.enforcer.OnEntityWrite(ctx, tenantID, bundleName, -1, -int64(len(existing.Data))); err != nil {
			return fmt.Errorf("quota check failed: %w", err)
		}
	}

	if err := u.txRunner.InTransaction(ctx, func(txCtx context.Context) error {
		return u.entityRepo.DeleteEntity(txCtx, tenantID, bundleName, entityType, entityID)
	}); err != nil {
		return fmt.Errorf("delete entity: %w", err)
	}
	u.invalidate(tenantID, bundleName)

	slog.InfoContext(ctx, "kg entity deleted",
		"tenant_id", tenantID,
		"bundle_name", bundleName,
		"entity_type", entityType,
		"entity_id", entityID,
	)
	return nil
}

// UpsertSchema applies a single schema (create or replace). Tool collisions
// are detected against the calculated tool names. The schema document is
// parsed for annotations to extract id_field, expose_tools, tool description.
func (u *Usecase) UpsertSchema(ctx context.Context, in UpsertSchemaInput) (*domain.KGEntitySchema, error) {
	if err := validateContext(in.TenantID, in.BundleName, in.EntityType); err != nil {
		return nil, err
	}

	bundle, err := u.bundleRepo.GetBundle(ctx, in.TenantID, in.BundleName)
	if err != nil {
		return nil, fmt.Errorf("load bundle: %w", err)
	}
	if bundle == nil {
		return nil, pkgerrors.NotFound(fmt.Sprintf("bundle not found: %s", in.BundleName))
	}

	annotations, err := jsonschema.ParseAnnotations(in.SchemaJSON)
	if err != nil {
		return nil, pkgerrors.InvalidInput(fmt.Sprintf("schema_invalid: %s", err.Error()))
	}

	expose := in.ExposeTools
	if len(expose) == 0 {
		expose = annotations.ExposeTools
	}
	description := in.ToolDescription
	if description == "" {
		description = annotations.ToolDescription
	}

	schema, err := domain.NewKGEntitySchema(
		in.TenantID, in.BundleName, in.EntityType,
		in.SchemaJSON, annotations.IDField, expose, description,
	)
	if err != nil {
		return nil, pkgerrors.InvalidInput(err.Error())
	}

	colliding, err := u.collision.Detect(ctx, in.TenantID, in.BundleName, schema.ToolNames())
	if err != nil {
		return nil, fmt.Errorf("collision detect: %w", err)
	}
	if len(colliding) > 0 {
		return nil, pkgerrors.InvalidInput(fmt.Sprintf(
			"tool_name_collision_in_tenant: %v already registered by another bundle, capability, or MCP server",
			colliding,
		))
	}

	if err := u.txRunner.InTransaction(ctx, func(txCtx context.Context) error {
		return u.schemaRepo.UpsertSchema(txCtx, schema)
	}); err != nil {
		return nil, fmt.Errorf("persist schema: %w", err)
	}
	u.invalidate(in.TenantID, in.BundleName)

	slog.InfoContext(ctx, "kg schema upserted",
		"tenant_id", in.TenantID,
		"bundle_name", in.BundleName,
		"entity_type", in.EntityType,
		"tools", schema.ToolNames(),
	)
	return schema, nil
}

// DeleteBundle removes an entire bundle (cascades through FK to schemas +
// entities). Idempotent: returns nil if bundle does not exist. Triggers
// negative quota delta proportional to the bundle's entity count.
func (u *Usecase) DeleteBundle(ctx context.Context, tenantID, bundleName string) error {
	if tenantID == "" {
		return pkgerrors.InvalidInput("tenant_id is required")
	}
	if !domain.ValidKGBundleName(bundleName) {
		return pkgerrors.InvalidInput(fmt.Sprintf("invalid bundle_name: %q", bundleName))
	}

	bundle, err := u.bundleRepo.GetBundle(ctx, tenantID, bundleName)
	if err != nil {
		return fmt.Errorf("load bundle: %w", err)
	}
	if bundle == nil {
		return nil // idempotent
	}

	count, bytes, err := u.bundleRepo.CountEntities(ctx, tenantID, bundleName)
	if err != nil {
		return fmt.Errorf("count entities: %w", err)
	}

	if u.enforcer != nil && count > 0 {
		if err := u.enforcer.OnEntityWrite(ctx, tenantID, bundleName, -count, -bytes); err != nil {
			return fmt.Errorf("quota check failed: %w", err)
		}
	}

	if err := u.txRunner.InTransaction(ctx, func(txCtx context.Context) error {
		return u.bundleRepo.DeleteBundle(txCtx, tenantID, bundleName)
	}); err != nil {
		return fmt.Errorf("delete bundle: %w", err)
	}
	u.invalidate(tenantID, bundleName)

	slog.InfoContext(ctx, "kg bundle deleted",
		"tenant_id", tenantID,
		"bundle_name", bundleName,
		"entities_removed", count,
	)
	return nil
}

// --- Internal helpers ---

func validateContext(tenantID, bundleName, entityType string) error {
	if tenantID == "" {
		return pkgerrors.InvalidInput("tenant_id is required")
	}
	if !domain.ValidKGBundleName(bundleName) {
		return pkgerrors.InvalidInput(fmt.Sprintf("invalid bundle_name: %q", bundleName))
	}
	if !domain.ValidKGEntityType(entityType) {
		return pkgerrors.InvalidInput(fmt.Sprintf("invalid entity_type: %q", entityType))
	}
	return nil
}

// buildEntity marshals raw data, validates against the schema, extracts the
// entity_id from the configured x-id-field, and constructs a domain.KGEntity.
// If expectedID is non-empty, it asserts that data's id_field value matches.
func (u *Usecase) buildEntity(
	ctx context.Context,
	schema *domain.KGEntitySchema,
	data map[string]any,
	expectedID string,
) (*domain.KGEntity, error) {
	if data == nil {
		return nil, pkgerrors.InvalidInput("entity data is required")
	}

	dataJSON, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal entity data: %w", err)
	}

	if err := u.validator.Validate(ctx, schema.SchemaJSON, dataJSON); err != nil {
		return nil, pkgerrors.InvalidInput(fmt.Sprintf("entity_invalid: %s", err.Error()))
	}

	idRaw, ok := data[schema.IDField]
	if !ok {
		return nil, pkgerrors.InvalidInput(fmt.Sprintf(
			"entity data missing id field %q (declared by schema's x-id-field)",
			schema.IDField,
		))
	}
	id, ok := idRaw.(string)
	if !ok {
		return nil, pkgerrors.InvalidInput(fmt.Sprintf(
			"id field %q must be a string, got %T",
			schema.IDField, idRaw,
		))
	}
	if id == "" {
		return nil, pkgerrors.InvalidInput(fmt.Sprintf("id field %q must be non-empty", schema.IDField))
	}
	_ = expectedID // caller asserts after we return; we still return the materialised entity_id

	entity, err := domain.NewKGEntity(
		schema.TenantID, schema.BundleName, schema.EntityType, id,
		dataJSON, schema.SchemaHash,
	)
	if err != nil {
		return nil, pkgerrors.InvalidInput(err.Error())
	}
	return entity, nil
}
