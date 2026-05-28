package configrepo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lib/pq"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GORMKGEntityRepository implements entity CRUD + filtered list using GORM.
type GORMKGEntityRepository struct {
	db *gorm.DB
}

func NewGORMKGEntityRepository(db *gorm.DB) *GORMKGEntityRepository {
	return &GORMKGEntityRepository{db: db}
}

func (r *GORMKGEntityRepository) dbHandle(ctx context.Context) *gorm.DB {
	if tx, ok := txFromContext(ctx); ok {
		return tx
	}
	return r.db.WithContext(ctx)
}

// UpsertEntity persists a single entity row (create or replace).
func (r *GORMKGEntityRepository) UpsertEntity(ctx context.Context, e *domain.KGEntity) error {
	model := entityToModel(e)
	return r.dbHandle(ctx).Save(model).Error
}

// ReplaceEntities does delete-then-insert of all entities under
// (tenant_id, bundle_name). Used by kgapply for atomic bundle replace.
func (r *GORMKGEntityRepository) ReplaceEntities(
	ctx context.Context,
	tenantID, bundleName string,
	entities []*domain.KGEntity,
) error {
	db := r.dbHandle(ctx)
	if err := db.
		Where("tenant_id = ? AND bundle_name = ?", tenantID, bundleName).
		Delete(&models.KGEntityModel{}).Error; err != nil {
		return fmt.Errorf("clear entities for bundle %s: %w", bundleName, err)
	}
	if len(entities) == 0 {
		return nil
	}
	rows := make([]models.KGEntityModel, len(entities))
	for i, e := range entities {
		rows[i] = *entityToModel(e)
	}
	// CreateInBatches handles potentially large inserts efficiently.
	if err := db.CreateInBatches(rows, 500).Error; err != nil {
		return fmt.Errorf("insert entities for bundle %s: %w", bundleName, err)
	}
	return nil
}

// DeleteEntity removes a single entity row. Idempotent.
func (r *GORMKGEntityRepository) DeleteEntity(ctx context.Context, tenantID, bundleName, entityType, entityID string) error {
	return r.dbHandle(ctx).
		Where("tenant_id = ? AND bundle_name = ? AND entity_type = ? AND entity_id = ?",
			tenantID, bundleName, entityType, entityID).
		Delete(&models.KGEntityModel{}).Error
}

// GetEntity returns one entity by composite key. Returns (nil, nil) when
// not found.
func (r *GORMKGEntityRepository) GetEntity(ctx context.Context, tenantID, bundleName, entityType, entityID string) (*domain.KGEntity, error) {
	var row models.KGEntityModel
	if err := r.dbHandle(ctx).
		Where("tenant_id = ? AND bundle_name = ? AND entity_type = ? AND entity_id = ?",
			tenantID, bundleName, entityType, entityID).
		First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("get entity: %w", err)
	}
	return entityFromModel(row), nil
}

// FilterSpec mirrors kgread.FilterSpec. The repo layer cannot import the
// usecase package (layer hygiene), so we duplicate the type and the app
// adapter copies between them.
//
// Exactly one operator family per spec is expected; the validation lives in
// the usecase layer (kgread.validateFilterSpecs). Repo trusts the spec.
type FilterSpec struct {
	Eq  any
	In  []any
	Gte any
	Gt  any
	Lte any
	Lt  any

	// CastExpr is the SQL cast the repo emits for the range operands.
	// Populated by the usecase based on the schema's FieldType. Empty
	// defaults to "numeric" for backward compat with integer/number
	// range filters.
	CastExpr string
}

// IsRange reports whether the spec uses any of the four range operators.
func (s FilterSpec) IsRange() bool {
	return s.Gte != nil || s.Gt != nil || s.Lte != nil || s.Lt != nil
}

// SortSpec mirrors kgread.SortSpec — see usecase doc. Trusted to be
// validated by the usecase layer (field is x-index, order in {asc, desc}).
// EnumValues, when non-empty for a sort field, drives `array_position`-based
// declaration-order sort. Empty means natural sort on data->>'field'.
type SortSpec struct {
	Field      string
	Order      string
	EnumValues []string
}

// ListEntitiesQuery captures all filter inputs for the paginated list.
type ListEntitiesQuery struct {
	TenantID   string
	BundleName string
	EntityType string
	Filters    map[string]FilterSpec
	Sort       []SortSpec
	Limit      int
	Offset     int
}

// GetEntities returns entities matching any of the supplied IDs, plus the
// list of IDs that were NOT found. Result entity order matches input ID
// order via `array_position($1, entity_id)`. Duplicates are de-duped upstream
// in the usecase, so this method trusts the input list.
//
// Empty input is rejected by the usecase, so this method always queries.
func (r *GORMKGEntityRepository) GetEntities(
	ctx context.Context,
	tenantID, bundleName, entityType string,
	ids []string,
) ([]*domain.KGEntity, []string, error) {
	if len(ids) == 0 {
		return nil, nil, nil
	}
	idArray := pq.Array(ids)
	var rows []models.KGEntityModel
	if err := r.dbHandle(ctx).
		Where("tenant_id = ? AND bundle_name = ? AND entity_type = ? AND entity_id = ANY(?)",
			tenantID, bundleName, entityType, idArray).
		Clauses(clause.OrderBy{
			Expression: clause.Expr{SQL: "array_position(?, entity_id)", Vars: []any{idArray}},
		}).
		Find(&rows).Error; err != nil {
		return nil, nil, fmt.Errorf("batch get entities: %w", err)
	}

	foundSet := make(map[string]struct{}, len(rows))
	out := make([]*domain.KGEntity, 0, len(rows))
	for _, row := range rows {
		out = append(out, entityFromModel(row))
		foundSet[row.EntityID] = struct{}{}
	}

	// NotFound preserves input order, contains only IDs absent from result.
	var notFound []string
	for _, id := range ids {
		if _, ok := foundSet[id]; !ok {
			notFound = append(notFound, id)
		}
	}
	return out, notFound, nil
}

// ListEntities returns a paginated list of entities matching the filters.
// Equality filters use the JSONB @> containment operator backed by the
// generic GIN index. Operator filters (in/gte/gt/lte/lt) use parameterised
// data->>'field' extractions with explicit type casts driven by usecase
// validation — repo does not introspect the schema.
func (r *GORMKGEntityRepository) ListEntities(ctx context.Context, q ListEntitiesQuery) ([]*domain.KGEntity, int, error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Limit > 500 {
		q.Limit = 500
	}
	if q.Offset < 0 {
		q.Offset = 0
	}

	base := r.dbHandle(ctx).
		Model(&models.KGEntityModel{}).
		Where("tenant_id = ? AND bundle_name = ? AND entity_type = ?",
			q.TenantID, q.BundleName, q.EntityType)

	if len(q.Filters) > 0 {
		applied, err := applyFilterSpecs(base, q.Filters)
		if err != nil {
			return nil, 0, err
		}
		base = applied
	}

	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count entities: %w", err)
	}

	ordered, err := applySortSpecs(base, q.Sort)
	if err != nil {
		return nil, 0, err
	}

	var rows []models.KGEntityModel
	if err := ordered.
		Limit(q.Limit).
		Offset(q.Offset).
		Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list entities: %w", err)
	}

	out := make([]*domain.KGEntity, 0, len(rows))
	for _, row := range rows {
		out = append(out, entityFromModel(row))
	}
	return out, int(total), nil
}

// applyFilterSpecs builds WHERE clauses for the heterogeneous filter map.
// Equality filters collapse into a single JSONB @> clause for GIN coverage;
// operator filters become separate parameterised clauses.
//
// Field names are NOT user-controlled here — the usecase layer has already
// validated them against the schema's x-index whitelist. We still wrap each
// name in a parameter-aware Sprintf where unavoidable, and never concatenate
// caller values into SQL.
func applyFilterSpecs(base *gorm.DB, filters map[string]FilterSpec) (*gorm.DB, error) {
	eqMap := make(map[string]any)
	for name, spec := range filters {
		if !validIdentifier(name) {
			return nil, fmt.Errorf("invalid filter field name %q", name)
		}
		switch {
		case spec.Eq != nil:
			eqMap[name] = spec.Eq
		case len(spec.In) > 0:
			base = base.Where(fmt.Sprintf("data->>%s = ANY(?)", quoteLiteral(name)), pq.Array(pgStringArray(spec.In)))
		case spec.IsRange():
			cast := spec.CastExpr
			if cast == "" {
				cast = "numeric"
			}
			// Cast hint is set ONLY by the usecase enrichment after schema
			// validation. validIdentifier above guards the field name; the
			// cast literal here comes from a fixed set the usecase knows.
			if spec.Gte != nil {
				base = base.Where(fmt.Sprintf("(data->>%s)::%s >= ?", quoteLiteral(name), cast), spec.Gte)
			}
			if spec.Gt != nil {
				base = base.Where(fmt.Sprintf("(data->>%s)::%s > ?", quoteLiteral(name), cast), spec.Gt)
			}
			if spec.Lte != nil {
				base = base.Where(fmt.Sprintf("(data->>%s)::%s <= ?", quoteLiteral(name), cast), spec.Lte)
			}
			if spec.Lt != nil {
				base = base.Where(fmt.Sprintf("(data->>%s)::%s < ?", quoteLiteral(name), cast), spec.Lt)
			}
		}
	}
	if len(eqMap) > 0 {
		eqJSON, err := json.Marshal(eqMap)
		if err != nil {
			return nil, fmt.Errorf("marshal equality filters: %w", err)
		}
		base = base.Where("data @> ?::jsonb", string(eqJSON))
	}
	return base, nil
}

// validIdentifier matches a JSON Schema property name acceptable as a JSONB
// path component. The usecase layer's x-index whitelist already enforces this
// at a higher level, but we double-check at the SQL boundary as defence in
// depth — a regression in the whitelist must not lead to SQL injection.
func validIdentifier(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			continue
		case r >= 'A' && r <= 'Z':
			continue
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
			continue
		case r == '_':
			continue
		default:
			return false
		}
	}
	return true
}

// quoteLiteral wraps a JSON property name in single quotes for use inside
// `data->>'name'` expressions. validIdentifier has already gated the input
// to ASCII alnum + underscore, so no escaping needed beyond the quotes.
func quoteLiteral(name string) string {
	return "'" + name + "'"
}

// applySortSpecs builds an ORDER BY chain. When Sort is empty, fall back to
// the 1.3.0 default `entity_id ASC` for stable pagination. Enum sort columns
// produce `array_position(ARRAY['v1','v2',...], data->>'field')` so the
// agent gets declaration order — the contract documented in tool descriptions
// and asserted by integration tests.
//
// All field names are gated by validIdentifier (defence-in-depth even though
// the usecase already enforced x-index whitelisting). Direction strings are
// fixed-set switch — no caller-supplied text reaches SQL.
func applySortSpecs(base *gorm.DB, specs []SortSpec) (*gorm.DB, error) {
	if len(specs) == 0 {
		return base.Order("entity_id ASC"), nil
	}
	for _, s := range specs {
		if !validIdentifier(s.Field) {
			return nil, fmt.Errorf("invalid sort field name %q", s.Field)
		}
		dir, ok := normaliseSortDirection(s.Order)
		if !ok {
			return nil, fmt.Errorf("invalid sort order %q for field %q", s.Order, s.Field)
		}
		var expr string
		if len(s.EnumValues) > 0 {
			// Build ARRAY['v1','v2',...] with PostgreSQL-escaped string literals.
			// EnumValues come from the schema (server-side), NOT user input —
			// double-escape single quotes anyway as defence in depth.
			items := make([]string, len(s.EnumValues))
			for i, v := range s.EnumValues {
				items[i] = "'" + escapeSingleQuotes(v) + "'"
			}
			// Enum semantic: declaring `enum: [very_high, high, normal, low]`
			// reads "high to low". The user-facing convention is therefore:
			//   sort=desc → "highest first" → head of the declared array
			//   sort=asc  → "lowest first"  → tail of the declared array
			//
			// array_position assigns 1 to the head, len(arr) to the tail. So:
			//   sort=desc → ORDER BY array_position(...) ASC  (smallest position = head)
			//   sort=asc  → ORDER BY array_position(...) DESC (largest position = tail)
			//
			// Invert the SQL direction relative to the natural mapping. Without
			// this swap the agent gets reverse-declaration order and the surprise
			// breaks every popularity/severity-style ranking.
			enumDir := "ASC"
			if dir == "ASC" {
				enumDir = "DESC"
			}
			expr = fmt.Sprintf("array_position(ARRAY[%s], data->>%s) %s NULLS LAST",
				strings.Join(items, ","), quoteLiteral(s.Field), enumDir)
		} else {
			expr = fmt.Sprintf("data->>%s %s NULLS LAST", quoteLiteral(s.Field), dir)
		}
		base = base.Order(expr)
	}
	// Stable tiebreaker on entity_id so pagination doesn't shuffle rows that
	// share all sort-key values.
	return base.Order("entity_id ASC"), nil
}

// normaliseSortDirection maps lower-case "asc"/"desc" to the upper-case form
// safely emitted into SQL. Anything else returns ok=false so the caller can
// produce a clear error.
func normaliseSortDirection(order string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(order)) {
	case "asc":
		return "ASC", true
	case "desc":
		return "DESC", true
	}
	return "", false
}

// escapeSingleQuotes doubles single quotes for PostgreSQL string literals.
// EnumValues already pass through ParseAnnotations; this is defence-in-depth
// in case a customer schema has an enum string containing a quote character.
func escapeSingleQuotes(v string) string {
	return strings.ReplaceAll(v, "'", "''")
}

// pgStringArray converts heterogeneous IN values to a PostgreSQL text array
// representation that GORM will bind as a parameter. Values are stringified
// because data->>'field' returns text — comparison semantics stay consistent
// with the equality and range paths.
func pgStringArray(values []any) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == nil {
			continue
		}
		out = append(out, fmt.Sprintf("%v", v))
	}
	return out
}

func entityToModel(e *domain.KGEntity) *models.KGEntityModel {
	return &models.KGEntityModel{
		TenantID:   e.TenantID,
		BundleName: e.BundleName,
		EntityType: e.EntityType,
		EntityID:   e.EntityID,
		Data:       datatypes.JSON(e.Data),
		SchemaHash: e.SchemaHash,
	}
}

func entityFromModel(row models.KGEntityModel) *domain.KGEntity {
	return &domain.KGEntity{
		TenantID:   row.TenantID,
		BundleName: row.BundleName,
		EntityType: row.EntityType,
		EntityID:   row.EntityID,
		Data:       []byte(row.Data),
		SchemaHash: row.SchemaHash,
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
	}
}
