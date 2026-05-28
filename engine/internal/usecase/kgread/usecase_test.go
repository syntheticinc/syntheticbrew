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
		Filters: map[string]any{"name": "alice"},
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
		Filters: map[string]any{"email": "x@y"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if er.lastQ.Filters["email"] != "x@y" {
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
		Filters: map[string]any{"email": "x@y"},
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
