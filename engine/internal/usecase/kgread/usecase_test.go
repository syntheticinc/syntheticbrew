package kgread

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

const (
	tenant = "tenant-1"
	bundle = "my-bundle"
)

// --- fixtures ---

func schemaJSONWithIndex(idField string, indexed ...string) []byte {
	props := fmt.Sprintf(`%q: {"type":"string"}`, idField)
	for _, f := range indexed {
		props += fmt.Sprintf(`,%q: {"type":"string","x-index":true}`, f)
	}
	return []byte(fmt.Sprintf(`{"type":"object","x-id-field":%q,"properties":{%s}}`, idField, props))
}

func newSchema(t *testing.T, entityType string, indexed ...string) *domain.KGEntitySchema {
	t.Helper()
	s, err := domain.NewKGEntitySchema(tenant, bundle, entityType, schemaJSONWithIndex("id", indexed...), "id", []string{"list", "get"}, "")
	if err != nil {
		t.Fatalf("NewKGEntitySchema: %v", err)
	}
	return s
}

// --- mocks ---

type mockBundleReader struct {
	list    []*domain.KGBundle
	listErr error
	get     map[string]*domain.KGBundle
	getErr  error
}

func (m *mockBundleReader) List(_ context.Context, _ string) ([]*domain.KGBundle, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.list, nil
}
func (m *mockBundleReader) Get(_ context.Context, _, name string) (*domain.KGBundle, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	b, ok := m.get[name]
	if !ok {
		return nil, pkgerrors.NotFound("bundle not found")
	}
	return b, nil
}

type mockSchemaReader struct {
	byBundle map[string][]*domain.KGEntitySchema
	listErr  error
	getErr   error
}

func (m *mockSchemaReader) ListByBundle(_ context.Context, _, name string) ([]*domain.KGEntitySchema, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.byBundle[name], nil
}
func (m *mockSchemaReader) Get(_ context.Context, _, name, entityType string) (*domain.KGEntitySchema, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	for _, s := range m.byBundle[name] {
		if s.EntityType == entityType {
			return s, nil
		}
	}
	return nil, pkgerrors.NotFound("schema not found")
}

type mockEntityReader struct {
	listFn   func(ctx context.Context, q ListEntitiesQuery) ([]*domain.KGEntity, int, error)
	listErr  error
	getErr   error
	entities map[string]*domain.KGEntity // key entity_id
	lastQ    ListEntitiesQuery
}

func (m *mockEntityReader) ListEntities(ctx context.Context, q ListEntitiesQuery) ([]*domain.KGEntity, int, error) {
	m.lastQ = q
	if m.listErr != nil {
		return nil, 0, m.listErr
	}
	if m.listFn != nil {
		return m.listFn(ctx, q)
	}
	return nil, 0, nil
}
func (m *mockEntityReader) GetEntity(_ context.Context, _, _, _, id string) (*domain.KGEntity, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	e, ok := m.entities[id]
	if !ok {
		return nil, pkgerrors.NotFound("entity not found")
	}
	return e, nil
}

// GetEntities mock for the batch get path. Returns found entities in input
// order plus not-found ids in input order, matching the contract.
func (m *mockEntityReader) GetEntities(_ context.Context, _, _, _ string, ids []string) ([]*domain.KGEntity, []string, error) {
	if m.getErr != nil {
		return nil, nil, m.getErr
	}
	var found []*domain.KGEntity
	var notFound []string
	for _, id := range ids {
		if e, ok := m.entities[id]; ok {
			found = append(found, e)
		} else {
			notFound = append(notFound, id)
		}
	}
	return found, notFound, nil
}

func newUsecase(b *mockBundleReader, s *mockSchemaReader, e *mockEntityReader) *Usecase {
	if b == nil {
		b = &mockBundleReader{}
	}
	if s == nil {
		s = &mockSchemaReader{}
	}
	if e == nil {
		e = &mockEntityReader{}
	}
	return New(b, s, e)
}

// --- ListBundles ---

func TestListBundles_Happy(t *testing.T) {
	bundles := []*domain.KGBundle{{TenantID: tenant, BundleName: "a", Version: "1"}}
	uc := newUsecase(&mockBundleReader{list: bundles}, nil, nil)
	got, err := uc.ListBundles(context.Background(), tenant)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("len: got %d", len(got))
	}
}

func TestListBundles_NoTenant(t *testing.T) {
	uc := newUsecase(nil, nil, nil)
	_, err := uc.ListBundles(context.Background(), "")
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

func TestListBundles_TenantFromCtx(t *testing.T) {
	uc := newUsecase(&mockBundleReader{list: []*domain.KGBundle{}}, nil, nil)
	ctx := domain.WithTenantID(context.Background(), tenant)
	if _, err := uc.ListBundles(ctx, ""); err != nil {
		t.Fatalf("expected ctx fallback to work, got %v", err)
	}
}

func TestListBundles_ReaderError(t *testing.T) {
	uc := newUsecase(&mockBundleReader{listErr: errors.New("DB boom")}, nil, nil)
	_, err := uc.ListBundles(context.Background(), tenant)
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- GetBundle ---

func TestGetBundle_Happy(t *testing.T) {
	br := &mockBundleReader{get: map[string]*domain.KGBundle{
		bundle: {TenantID: tenant, BundleName: bundle, Version: "1"},
	}}
	uc := newUsecase(br, nil, nil)
	got, err := uc.GetBundle(context.Background(), tenant, bundle)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got.BundleName != bundle {
		t.Errorf("name: got %q", got.BundleName)
	}
}

func TestGetBundle_InvalidName(t *testing.T) {
	uc := newUsecase(nil, nil, nil)
	_, err := uc.GetBundle(context.Background(), tenant, "BadName!")
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

func TestGetBundle_NotFoundPropagated(t *testing.T) {
	uc := newUsecase(&mockBundleReader{get: map[string]*domain.KGBundle{}}, nil, nil)
	_, err := uc.GetBundle(context.Background(), tenant, bundle)
	if !pkgerrors.Is(err, pkgerrors.CodeNotFound) {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestGetBundle_NoTenant(t *testing.T) {
	uc := newUsecase(nil, nil, nil)
	_, err := uc.GetBundle(context.Background(), "", bundle)
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

// --- ListSchemas ---

func TestListSchemas_Happy(t *testing.T) {
	sr := &mockSchemaReader{byBundle: map[string][]*domain.KGEntitySchema{
		bundle: {newSchema(t, "user")},
	}}
	uc := newUsecase(nil, sr, nil)
	got, err := uc.ListSchemas(context.Background(), tenant, bundle)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("len: got %d", len(got))
	}
}

func TestListSchemas_InvalidBundleName(t *testing.T) {
	uc := newUsecase(nil, nil, nil)
	_, err := uc.ListSchemas(context.Background(), tenant, "Bad!")
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

func TestListSchemas_ReaderError(t *testing.T) {
	uc := newUsecase(nil, &mockSchemaReader{listErr: errors.New("DB")}, nil)
	_, err := uc.ListSchemas(context.Background(), tenant, bundle)
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- GetSchema ---

func TestGetSchema_Happy(t *testing.T) {
	sr := &mockSchemaReader{byBundle: map[string][]*domain.KGEntitySchema{
		bundle: {newSchema(t, "user", "email")},
	}}
	uc := newUsecase(nil, sr, nil)
	got, err := uc.GetSchema(context.Background(), tenant, bundle, "user")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got.EntityType != "user" {
		t.Errorf("type: got %q", got.EntityType)
	}
}

func TestGetSchema_InvalidEntityType(t *testing.T) {
	uc := newUsecase(nil, nil, nil)
	_, err := uc.GetSchema(context.Background(), tenant, bundle, "Bad-Type!")
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

func TestGetSchema_NotFoundPropagated(t *testing.T) {
	sr := &mockSchemaReader{byBundle: map[string][]*domain.KGEntitySchema{
		bundle: {},
	}}
	uc := newUsecase(nil, sr, nil)
	_, err := uc.GetSchema(context.Background(), tenant, bundle, "ghost")
	if !pkgerrors.Is(err, pkgerrors.CodeNotFound) {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

// --- ListEntities ---

func TestListEntities_Happy(t *testing.T) {
	sr := &mockSchemaReader{byBundle: map[string][]*domain.KGEntitySchema{
		bundle: {newSchema(t, "user", "email")},
	}}
	er := &mockEntityReader{listFn: func(_ context.Context, q ListEntitiesQuery) ([]*domain.KGEntity, int, error) {
		return []*domain.KGEntity{{EntityID: "1"}}, 1, nil
	}}
	uc := newUsecase(nil, sr, er)
	items, total, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID:   tenant,
		BundleName: bundle,
		EntityType: "user",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(items) != 1 || total != 1 {
		t.Errorf("items=%d total=%d", len(items), total)
	}
}

func TestListEntities_DefaultLimit(t *testing.T) {
	er := &mockEntityReader{}
	uc := newUsecase(nil, nil, er)
	_, _, _ = uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: bundle, EntityType: "user",
		Limit: 0,
	})
	if er.lastQ.Limit != DefaultListLimit {
		t.Errorf("limit: got %d, want %d", er.lastQ.Limit, DefaultListLimit)
	}
}

func TestListEntities_ClampLimit(t *testing.T) {
	er := &mockEntityReader{}
	uc := newUsecase(nil, nil, er)
	_, _, _ = uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: bundle, EntityType: "user",
		Limit: 1000,
	})
	if er.lastQ.Limit != MaxListLimit {
		t.Errorf("limit: got %d, want %d", er.lastQ.Limit, MaxListLimit)
	}
}

func TestListEntities_NegativeOffset(t *testing.T) {
	uc := newUsecase(nil, nil, nil)
	_, _, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: bundle, EntityType: "user",
		Offset: -5,
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

func TestListEntities_FilterOnUnknownField(t *testing.T) {
	sr := &mockSchemaReader{byBundle: map[string][]*domain.KGEntitySchema{
		bundle: {newSchema(t, "user", "email")},
	}}
	er := &mockEntityReader{}
	uc := newUsecase(nil, sr, er)
	_, _, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: bundle, EntityType: "user",
		Filters: map[string]FilterSpec{"name": {Eq: "alice"}},
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
	if er.lastQ.EntityType != "" {
		t.Errorf("entity reader should not be called when filter check fails")
	}
}

func TestListEntities_FilterOnIndexedField(t *testing.T) {
	sr := &mockSchemaReader{byBundle: map[string][]*domain.KGEntitySchema{
		bundle: {newSchema(t, "user", "email")},
	}}
	er := &mockEntityReader{}
	uc := newUsecase(nil, sr, er)
	_, _, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: bundle, EntityType: "user",
		Filters: map[string]FilterSpec{"email": {Eq: "x@y"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := er.lastQ.Filters["email"]; got.Eq != "x@y" {
		t.Errorf("filter not passed through: %+v", er.lastQ.Filters)
	}
}

func TestListEntities_InvalidBundleName(t *testing.T) {
	uc := newUsecase(nil, nil, nil)
	_, _, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: "BAD!", EntityType: "user",
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

func TestListEntities_InvalidEntityType(t *testing.T) {
	uc := newUsecase(nil, nil, nil)
	_, _, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: bundle, EntityType: "BadType!",
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

func TestListEntities_SchemaLookupFails(t *testing.T) {
	sr := &mockSchemaReader{getErr: errors.New("DB read failed")}
	uc := newUsecase(nil, sr, &mockEntityReader{})
	_, _, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: bundle, EntityType: "user",
		Filters: map[string]FilterSpec{"email": {Eq: "x@y"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestListEntities_NoTenant(t *testing.T) {
	uc := newUsecase(nil, nil, nil)
	_, _, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		BundleName: bundle, EntityType: "user",
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

func TestListEntities_EntityReaderError(t *testing.T) {
	er := &mockEntityReader{listErr: errors.New("DB")}
	uc := newUsecase(nil, nil, er)
	_, _, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: bundle, EntityType: "user",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- 1.4.0 filter operator tests ---

// schemaJSONWithTypedFields produces a schema with mixed-type indexed properties
// (string, integer, date-time, enum) — needed for filter operator coverage.
func schemaJSONWithTypedFields() []byte {
	return []byte(`{
		"type": "object",
		"x-id-field": "code",
		"properties": {
			"code":       {"type": "string", "x-index": true},
			"title":      {"type": "string"},
			"industry":   {"type": "string", "x-index": true},
			"popularity": {"type": "string", "enum": ["very_high", "high", "normal", "low"], "x-index": true},
			"score":      {"type": "integer", "x-index": true},
			"created_at": {"type": "string", "format": "date-time", "x-index": true}
		}
	}`)
}

func newTypedSchema(t *testing.T) *domain.KGEntitySchema {
	t.Helper()
	s, err := domain.NewKGEntitySchema(tenant, bundle, "use_case", schemaJSONWithTypedFields(), "code", []string{"list", "get", "list_ids"}, "")
	if err != nil {
		t.Fatalf("NewKGEntitySchema: %v", err)
	}
	return s
}

func TestListEntities_RangeOnIntegerField_Accepted(t *testing.T) {
	sr := &mockSchemaReader{byBundle: map[string][]*domain.KGEntitySchema{bundle: {newTypedSchema(t)}}}
	er := &mockEntityReader{}
	uc := newUsecase(nil, sr, er)
	_, _, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: bundle, EntityType: "use_case",
		Filters: map[string]FilterSpec{"score": {Gte: 70, Lte: 95}},
	})
	if err != nil {
		t.Fatalf("range on integer should be accepted: %v", err)
	}
}

func TestListEntities_RangeOnDateField_Accepted(t *testing.T) {
	sr := &mockSchemaReader{byBundle: map[string][]*domain.KGEntitySchema{bundle: {newTypedSchema(t)}}}
	er := &mockEntityReader{}
	uc := newUsecase(nil, sr, er)
	_, _, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: bundle, EntityType: "use_case",
		Filters: map[string]FilterSpec{"created_at": {Gte: "2026-01-01"}},
	})
	if err != nil {
		t.Fatalf("range on date-time should be accepted: %v", err)
	}
}

func TestListEntities_RangeOnStringField_Rejected(t *testing.T) {
	sr := &mockSchemaReader{byBundle: map[string][]*domain.KGEntitySchema{bundle: {newTypedSchema(t)}}}
	er := &mockEntityReader{}
	uc := newUsecase(nil, sr, er)
	_, _, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: bundle, EntityType: "use_case",
		Filters: map[string]FilterSpec{"industry": {Gte: "PM"}},
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
	if er.lastQ.EntityType != "" {
		t.Errorf("repo should not be called when validation fails")
	}
}

func TestListEntities_RangeOnEnumField_Rejected(t *testing.T) {
	// Enum is type=string in JSON Schema (`enum: [...]` on a string property).
	// Range comparison on enum values is non-sensical; must reject.
	sr := &mockSchemaReader{byBundle: map[string][]*domain.KGEntitySchema{bundle: {newTypedSchema(t)}}}
	uc := newUsecase(nil, sr, &mockEntityReader{})
	_, _, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: bundle, EntityType: "use_case",
		Filters: map[string]FilterSpec{"popularity": {Gte: "high"}},
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("range on enum string should be rejected, got %v", err)
	}
}

func TestListEntities_InOnIndexedField_Accepted(t *testing.T) {
	sr := &mockSchemaReader{byBundle: map[string][]*domain.KGEntitySchema{bundle: {newTypedSchema(t)}}}
	er := &mockEntityReader{}
	uc := newUsecase(nil, sr, er)
	_, _, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: bundle, EntityType: "use_case",
		Filters: map[string]FilterSpec{"industry": {In: []any{"PM", "FB", "RT"}}},
	})
	if err != nil {
		t.Fatalf("IN on indexed string should be accepted: %v", err)
	}
	if got := er.lastQ.Filters["industry"]; len(got.In) != 3 {
		t.Errorf("IN values not passed through: %+v", got)
	}
}

func TestListEntities_OperatorOnNonIndexedField_Rejected(t *testing.T) {
	sr := &mockSchemaReader{byBundle: map[string][]*domain.KGEntitySchema{bundle: {newTypedSchema(t)}}}
	uc := newUsecase(nil, sr, &mockEntityReader{})
	_, _, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: bundle, EntityType: "use_case",
		Filters: map[string]FilterSpec{"title": {In: []any{"x"}}}, // title is NOT x-index
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput on non-indexed field, got %v", err)
	}
}

func TestListEntities_MixedOperators_Rejected(t *testing.T) {
	// Eq + Gte at once must reject — ambiguous semantics; parser layer (HTTP
	// + tool args) should produce exactly one operator family per field.
	sr := &mockSchemaReader{byBundle: map[string][]*domain.KGEntitySchema{bundle: {newTypedSchema(t)}}}
	uc := newUsecase(nil, sr, &mockEntityReader{})
	_, _, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: bundle, EntityType: "use_case",
		Filters: map[string]FilterSpec{"score": {Eq: 80, Gte: 70}},
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput on mixed operators, got %v", err)
	}
}

func TestListEntities_EmptyFilterSpec_Rejected(t *testing.T) {
	sr := &mockSchemaReader{byBundle: map[string][]*domain.KGEntitySchema{bundle: {newTypedSchema(t)}}}
	uc := newUsecase(nil, sr, &mockEntityReader{})
	_, _, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: bundle, EntityType: "use_case",
		Filters: map[string]FilterSpec{"score": {}}, // no Eq, no In, no range
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput on empty filter spec, got %v", err)
	}
}

// --- 1.4.0 sort tests ---

func TestListEntities_Sort_SingleField_PassedThrough(t *testing.T) {
	sr := &mockSchemaReader{byBundle: map[string][]*domain.KGEntitySchema{bundle: {newTypedSchema(t)}}}
	er := &mockEntityReader{}
	uc := newUsecase(nil, sr, er)
	_, _, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: bundle, EntityType: "use_case",
		Sort: []SortSpec{{Field: "score", Order: SortOrderDesc}},
	})
	if err != nil {
		t.Fatalf("single-field sort should be accepted: %v", err)
	}
	if got := er.lastQ.Sort; len(got) != 1 || got[0].Field != "score" || got[0].Order != "desc" {
		t.Errorf("sort not passed through: %+v", got)
	}
}

func TestListEntities_Sort_EnumField_EnrichedWithDeclarationOrder(t *testing.T) {
	// CRITICAL: when sort references an enum property, usecase must enrich
	// SortSpec.EnumValues with the schema's declaration-order values so the
	// repo can emit array_position(...) instead of alphabetical sort.
	sr := &mockSchemaReader{byBundle: map[string][]*domain.KGEntitySchema{bundle: {newTypedSchema(t)}}}
	er := &mockEntityReader{}
	uc := newUsecase(nil, sr, er)
	_, _, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: bundle, EntityType: "use_case",
		Sort: []SortSpec{{Field: "popularity", Order: SortOrderDesc}},
	})
	if err != nil {
		t.Fatalf("sort on enum field should be accepted: %v", err)
	}
	wantEnum := []string{"very_high", "high", "normal", "low"}
	got := er.lastQ.Sort[0].EnumValues
	if len(got) != len(wantEnum) {
		t.Fatalf("EnumValues not populated: got %v, want %v", got, wantEnum)
	}
	for i, v := range wantEnum {
		if got[i] != v {
			t.Errorf("EnumValues[%d]: got %q, want %q (declaration order, NOT alphabetical)", i, got[i], v)
		}
	}
}

func TestListEntities_Sort_NonEnumField_LeavesEnumValuesEmpty(t *testing.T) {
	// Non-enum fields must NOT have EnumValues populated — the repo would
	// otherwise emit array_position over an empty array which has no match.
	sr := &mockSchemaReader{byBundle: map[string][]*domain.KGEntitySchema{bundle: {newTypedSchema(t)}}}
	er := &mockEntityReader{}
	uc := newUsecase(nil, sr, er)
	_, _, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: bundle, EntityType: "use_case",
		Sort: []SortSpec{{Field: "score", Order: SortOrderAsc}},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(er.lastQ.Sort[0].EnumValues) != 0 {
		t.Errorf("non-enum sort field must have empty EnumValues, got %v", er.lastQ.Sort[0].EnumValues)
	}
}

func TestListEntities_Sort_NonIndexedField_Rejected(t *testing.T) {
	sr := &mockSchemaReader{byBundle: map[string][]*domain.KGEntitySchema{bundle: {newTypedSchema(t)}}}
	uc := newUsecase(nil, sr, &mockEntityReader{})
	_, _, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: bundle, EntityType: "use_case",
		Sort: []SortSpec{{Field: "title", Order: SortOrderAsc}}, // title is NOT x-index
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput on non-indexed sort field, got %v", err)
	}
}

func TestListEntities_Sort_InvalidOrder_Rejected(t *testing.T) {
	sr := &mockSchemaReader{byBundle: map[string][]*domain.KGEntitySchema{bundle: {newTypedSchema(t)}}}
	uc := newUsecase(nil, sr, &mockEntityReader{})
	_, _, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: bundle, EntityType: "use_case",
		Sort: []SortSpec{{Field: "score", Order: "ASCENDING"}}, // not "asc"|"desc"
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput on invalid order, got %v", err)
	}
}

func TestListEntities_Sort_DuplicateField_Rejected(t *testing.T) {
	sr := &mockSchemaReader{byBundle: map[string][]*domain.KGEntitySchema{bundle: {newTypedSchema(t)}}}
	uc := newUsecase(nil, sr, &mockEntityReader{})
	_, _, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: bundle, EntityType: "use_case",
		Sort: []SortSpec{
			{Field: "score", Order: SortOrderDesc},
			{Field: "score", Order: SortOrderAsc},
		},
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput on duplicate sort field, got %v", err)
	}
}

// --- 1.4.0 batch get tests ---

func TestGetEntities_HappyPath_OrderPreserved(t *testing.T) {
	er := &mockEntityReader{entities: map[string]*domain.KGEntity{
		"A": {EntityID: "A"},
		"B": {EntityID: "B"},
		"C": {EntityID: "C"},
	}}
	uc := newUsecase(nil, nil, er)
	res, err := uc.GetEntities(context.Background(), tenant, bundle, "use_case",
		[]string{"C", "A", "B"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(res.Entities) != 3 {
		t.Fatalf("entities count: got %d, want 3", len(res.Entities))
	}
	if res.Entities[0].EntityID != "C" || res.Entities[1].EntityID != "A" || res.Entities[2].EntityID != "B" {
		t.Errorf("order not preserved: %v", []string{res.Entities[0].EntityID, res.Entities[1].EntityID, res.Entities[2].EntityID})
	}
	if len(res.NotFound) != 0 {
		t.Errorf("expected empty NotFound, got %v", res.NotFound)
	}
}

func TestGetEntities_PartialMissing_ReportedInNotFound(t *testing.T) {
	er := &mockEntityReader{entities: map[string]*domain.KGEntity{
		"A": {EntityID: "A"},
	}}
	uc := newUsecase(nil, nil, er)
	res, err := uc.GetEntities(context.Background(), tenant, bundle, "use_case",
		[]string{"A", "B", "C"})
	if err != nil {
		t.Fatalf("partial missing must NOT error: %v", err)
	}
	if len(res.Entities) != 1 || res.Entities[0].EntityID != "A" {
		t.Errorf("found entities: got %+v, want [A]", res.Entities)
	}
	wantNotFound := []string{"B", "C"}
	if len(res.NotFound) != 2 || res.NotFound[0] != "B" || res.NotFound[1] != "C" {
		t.Errorf("NotFound: got %v, want %v", res.NotFound, wantNotFound)
	}
}

func TestGetEntities_AllMissing_ReturnsEmptyEntities(t *testing.T) {
	er := &mockEntityReader{entities: map[string]*domain.KGEntity{}}
	uc := newUsecase(nil, nil, er)
	res, err := uc.GetEntities(context.Background(), tenant, bundle, "use_case",
		[]string{"X", "Y", "Z"})
	if err != nil {
		t.Fatalf("all-missing must NOT error: %v", err)
	}
	if len(res.Entities) != 0 {
		t.Errorf("expected empty Entities, got %+v", res.Entities)
	}
	if len(res.NotFound) != 3 {
		t.Errorf("expected 3 NotFound entries, got %v", res.NotFound)
	}
}

func TestGetEntities_DuplicatesDeduped(t *testing.T) {
	er := &mockEntityReader{entities: map[string]*domain.KGEntity{
		"A": {EntityID: "A"},
		"B": {EntityID: "B"},
	}}
	uc := newUsecase(nil, nil, er)
	res, err := uc.GetEntities(context.Background(), tenant, bundle, "use_case",
		[]string{"A", "A", "B", "A"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// Entities deduplicated — A appears once, then B.
	if len(res.Entities) != 2 {
		t.Errorf("expected 2 deduped entities, got %d (%+v)", len(res.Entities), res.Entities)
	}
}

func TestGetEntities_EmptyArray_Rejected(t *testing.T) {
	uc := newUsecase(nil, nil, &mockEntityReader{})
	_, err := uc.GetEntities(context.Background(), tenant, bundle, "use_case", nil)
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput on empty ids, got %v", err)
	}
}

func TestGetEntities_TooMany_Rejected(t *testing.T) {
	tooMany := make([]string, MaxBatchGetIDs+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("id-%d", i)
	}
	uc := newUsecase(nil, nil, &mockEntityReader{})
	_, err := uc.GetEntities(context.Background(), tenant, bundle, "use_case", tooMany)
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput on >%d ids, got %v", MaxBatchGetIDs, err)
	}
}

func TestGetEntities_EmptyIDInArray_Rejected(t *testing.T) {
	uc := newUsecase(nil, nil, &mockEntityReader{})
	_, err := uc.GetEntities(context.Background(), tenant, bundle, "use_case",
		[]string{"A", "", "C"})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput on empty id, got %v", err)
	}
}

func TestGetEntities_InvalidBundleName_Rejected(t *testing.T) {
	uc := newUsecase(nil, nil, &mockEntityReader{})
	_, err := uc.GetEntities(context.Background(), tenant, "BAD!", "use_case", []string{"A"})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput on bad bundle, got %v", err)
	}
}

func TestListEntities_Sort_MultiField_AllPassedThrough(t *testing.T) {
	sr := &mockSchemaReader{byBundle: map[string][]*domain.KGEntitySchema{bundle: {newTypedSchema(t)}}}
	er := &mockEntityReader{}
	uc := newUsecase(nil, sr, er)
	_, _, err := uc.ListEntities(context.Background(), ListEntitiesQuery{
		TenantID: tenant, BundleName: bundle, EntityType: "use_case",
		Sort: []SortSpec{
			{Field: "popularity", Order: SortOrderDesc},
			{Field: "code", Order: SortOrderAsc},
		},
	})
	if err != nil {
		t.Fatalf("multi-field sort should be accepted: %v", err)
	}
	if len(er.lastQ.Sort) != 2 {
		t.Fatalf("expected 2 sort specs, got %d", len(er.lastQ.Sort))
	}
	if er.lastQ.Sort[0].Field != "popularity" || er.lastQ.Sort[1].Field != "code" {
		t.Errorf("multi-field order not preserved: %+v", er.lastQ.Sort)
	}
}

// --- GetEntity ---

func TestGetEntity_Happy(t *testing.T) {
	er := &mockEntityReader{entities: map[string]*domain.KGEntity{
		"u1": {EntityID: "u1"},
	}}
	uc := newUsecase(nil, nil, er)
	got, err := uc.GetEntity(context.Background(), tenant, bundle, "user", "u1")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got.EntityID != "u1" {
		t.Errorf("id: got %q", got.EntityID)
	}
}

func TestGetEntity_EmptyID(t *testing.T) {
	uc := newUsecase(nil, nil, nil)
	_, err := uc.GetEntity(context.Background(), tenant, bundle, "user", "")
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

func TestGetEntity_IDTooLong(t *testing.T) {
	uc := newUsecase(nil, nil, nil)
	long := make([]byte, domain.KGEntityMaxIDLength+1)
	for i := range long {
		long[i] = 'a'
	}
	_, err := uc.GetEntity(context.Background(), tenant, bundle, "user", string(long))
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

func TestGetEntity_InvalidBundle(t *testing.T) {
	uc := newUsecase(nil, nil, nil)
	_, err := uc.GetEntity(context.Background(), tenant, "BAD!", "user", "u1")
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

func TestGetEntity_InvalidEntityType(t *testing.T) {
	uc := newUsecase(nil, nil, nil)
	_, err := uc.GetEntity(context.Background(), tenant, bundle, "BadType!", "u1")
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

func TestGetEntity_NotFoundPropagated(t *testing.T) {
	er := &mockEntityReader{entities: map[string]*domain.KGEntity{}}
	uc := newUsecase(nil, nil, er)
	_, err := uc.GetEntity(context.Background(), tenant, bundle, "user", "ghost")
	if !pkgerrors.Is(err, pkgerrors.CodeNotFound) {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestGetEntity_NoTenant(t *testing.T) {
	uc := newUsecase(nil, nil, nil)
	_, err := uc.GetEntity(context.Background(), "", bundle, "user", "u1")
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

// --- sortedKeys helper ---

func TestSortedKeys_Deterministic(t *testing.T) {
	got := sortedKeys(map[string]struct{}{"zeta": {}, "alpha": {}, "mu": {}})
	want := []string{"alpha", "mu", "zeta"}
	if len(got) != 3 || got[0] != want[0] || got[2] != want[2] {
		t.Errorf("got %v, want %v", got, want)
	}
}

// --- indexedFieldsFromSchema bad path ---

func TestIndexedFieldsFromSchema_BadJSON(t *testing.T) {
	s := &domain.KGEntitySchema{SchemaJSON: []byte("{not valid")}
	_, err := indexedFieldsFromSchema(s)
	if err == nil {
		t.Fatal("expected parse error")
	}
}
