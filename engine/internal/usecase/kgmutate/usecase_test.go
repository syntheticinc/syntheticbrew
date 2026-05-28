package kgmutate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/kgmutate"
)

// --- Test fixtures + helpers ---

const validIndustrySchemaJSON = `{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"$id": "category",
	"type": "object",
	"x-id-field": "code",
	"x-tool-expose": ["list", "get"],
	"required": ["code", "name"],
	"properties": {
		"code": {"type": "string", "pattern": "^[A-Z]{2,4}$", "x-index": true},
		"name": {"type": "string", "minLength": 3}
	}
}`

func newSchemaFixture(t *testing.T) *domain.KGEntitySchema {
	t.Helper()
	s, err := domain.NewKGEntitySchema(
		"tenant-x", "bundle-x", "category",
		[]byte(validIndustrySchemaJSON), "code",
		[]string{"list", "get"}, "Industries",
	)
	if err != nil {
		t.Fatalf("schema fixture: %v", err)
	}
	return s
}

// --- Fakes implementing the consumer-side interfaces ---

type fakeBundleRepo struct {
	getBundleFn   func(ctx context.Context, tenantID, bundleName string) (*domain.KGBundle, error)
	deleteCalled  bool
	deleteErr     error
	countEntities func(ctx context.Context, tenantID, bundleName string) (int, int64, error)
}

func (f *fakeBundleRepo) GetBundle(ctx context.Context, tenantID, bundleName string) (*domain.KGBundle, error) {
	if f.getBundleFn != nil {
		return f.getBundleFn(ctx, tenantID, bundleName)
	}
	return &domain.KGBundle{TenantID: tenantID, BundleName: bundleName, Version: "1.0.0"}, nil
}
func (f *fakeBundleRepo) DeleteBundle(ctx context.Context, tenantID, bundleName string) error {
	f.deleteCalled = true
	return f.deleteErr
}
func (f *fakeBundleRepo) CountEntities(ctx context.Context, tenantID, bundleName string) (int, int64, error) {
	if f.countEntities != nil {
		return f.countEntities(ctx, tenantID, bundleName)
	}
	return 0, 0, nil
}

type fakeSchemaRepo struct {
	getFn      func(ctx context.Context, tenantID, bundleName, entityType string) (*domain.KGEntitySchema, error)
	upsertedTo *domain.KGEntitySchema
	upsertErr  error
}

func (f *fakeSchemaRepo) GetSchema(ctx context.Context, tenantID, bundleName, entityType string) (*domain.KGEntitySchema, error) {
	if f.getFn != nil {
		return f.getFn(ctx, tenantID, bundleName, entityType)
	}
	return nil, nil
}
func (f *fakeSchemaRepo) UpsertSchema(ctx context.Context, schema *domain.KGEntitySchema) error {
	f.upsertedTo = schema
	return f.upsertErr
}

type fakeEntityRepo struct {
	getFn       func(ctx context.Context, tenantID, bundleName, entityType, entityID string) (*domain.KGEntity, error)
	upsertedTo  *domain.KGEntity
	upsertErr   error
	deletedFor  string
	deleteErr   error
}

func (f *fakeEntityRepo) GetEntity(ctx context.Context, tenantID, bundleName, entityType, entityID string) (*domain.KGEntity, error) {
	if f.getFn != nil {
		return f.getFn(ctx, tenantID, bundleName, entityType, entityID)
	}
	return nil, nil
}
func (f *fakeEntityRepo) UpsertEntity(ctx context.Context, entity *domain.KGEntity) error {
	f.upsertedTo = entity
	return f.upsertErr
}
func (f *fakeEntityRepo) DeleteEntity(ctx context.Context, tenantID, bundleName, entityType, entityID string) error {
	f.deletedFor = entityID
	return f.deleteErr
}

type fakeValidator struct {
	err error
}

func (f *fakeValidator) Validate(ctx context.Context, schemaJSON, entityData []byte) error {
	return f.err
}

type fakeCollision struct {
	colliding []string
	err       error
}

func (f *fakeCollision) Detect(ctx context.Context, tenantID, excludeBundle string, names []string) ([]string, error) {
	return f.colliding, f.err
}

type fakeEnforcer struct {
	calls []int
	err   error
}

func (f *fakeEnforcer) OnEntityWrite(ctx context.Context, tenantID, bundleName string, delta int, bytes int64) error {
	f.calls = append(f.calls, delta)
	return f.err
}

type fakeTxRunner struct{ inTx bool }

func (f *fakeTxRunner) InTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	f.inTx = true
	return fn(ctx)
}

type fakeInvalidator struct{ calls []string }

func (f *fakeInvalidator) InvalidateBundle(tenantID, bundleName string) {
	f.calls = append(f.calls, tenantID+":"+bundleName)
}

// newUsecase constructs a usecase with all fakes; tests override specific
// fakes via the returned references.
func newUsecase() (
	*kgmutate.Usecase,
	*fakeBundleRepo,
	*fakeSchemaRepo,
	*fakeEntityRepo,
	*fakeValidator,
	*fakeCollision,
	*fakeEnforcer,
	*fakeTxRunner,
	*fakeInvalidator,
) {
	br := &fakeBundleRepo{}
	sr := &fakeSchemaRepo{}
	er := &fakeEntityRepo{}
	v := &fakeValidator{}
	c := &fakeCollision{}
	e := &fakeEnforcer{}
	tx := &fakeTxRunner{}
	inv := &fakeInvalidator{}
	uc := kgmutate.New(br, sr, er, v, c, e, tx)
	uc.SetInvalidator(inv)
	return uc, br, sr, er, v, c, e, tx, inv
}

// --- CreateEntity tests ---

func TestCreateEntity_Happy(t *testing.T) {
	t.Parallel()
	uc, _, sr, er, _, _, _, tx, inv := newUsecase()
	sr.getFn = func(ctx context.Context, tenantID, bundle, entityType string) (*domain.KGEntitySchema, error) {
		return newSchemaFixture(t), nil
	}

	entity, err := uc.CreateEntity(context.Background(), kgmutate.CreateEntityInput{
		TenantID:   "tenant-x",
		BundleName: "bundle-x",
		EntityType: "category",
		Data:       map[string]any{"code": "FW", "name": "Footwear"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entity.EntityID != "FW" {
		t.Errorf("EntityID: got %q, want \"FW\"", entity.EntityID)
	}
	if !tx.inTx {
		t.Error("expected transaction to be opened")
	}
	if er.upsertedTo == nil {
		t.Error("expected entity to be persisted")
	}
	if len(inv.calls) != 1 {
		t.Errorf("expected exactly one invalidator call, got %d", len(inv.calls))
	}
}

func TestCreateEntity_SchemaNotFound(t *testing.T) {
	t.Parallel()
	uc, _, sr, _, _, _, _, _, _ := newUsecase()
	sr.getFn = func(ctx context.Context, tenantID, bundle, entityType string) (*domain.KGEntitySchema, error) {
		return nil, nil // schema does not exist
	}
	_, err := uc.CreateEntity(context.Background(), kgmutate.CreateEntityInput{
		TenantID:   "tenant-x",
		BundleName: "bundle-x",
		EntityType: "category",
		Data:       map[string]any{"code": "FW", "name": "FW Long"},
	})
	if err == nil {
		t.Fatal("expected NotFound error")
	}
}

func TestCreateEntity_InvalidInput(t *testing.T) {
	t.Parallel()
	uc, _, _, _, _, _, _, _, _ := newUsecase()
	cases := []kgmutate.CreateEntityInput{
		{TenantID: "", BundleName: "b", EntityType: "category", Data: map[string]any{"code": "FW"}},
		{TenantID: "t", BundleName: "Bad-Name", EntityType: "category", Data: map[string]any{"code": "FW"}},
		{TenantID: "t", BundleName: "good-name", EntityType: "BadType", Data: map[string]any{"code": "FW"}},
	}
	for _, in := range cases {
		_, err := uc.CreateEntity(context.Background(), in)
		if err == nil {
			t.Errorf("expected error for input %+v", in)
		}
	}
}

func TestCreateEntity_QuotaRejected(t *testing.T) {
	t.Parallel()
	uc, _, sr, _, _, _, e, _, _ := newUsecase()
	sr.getFn = func(ctx context.Context, tenantID, bundle, entityType string) (*domain.KGEntitySchema, error) {
		return newSchemaFixture(t), nil
	}
	e.err = errors.New("quota exhausted")
	_, err := uc.CreateEntity(context.Background(), kgmutate.CreateEntityInput{
		TenantID:   "tenant-x",
		BundleName: "bundle-x",
		EntityType: "category",
		Data:       map[string]any{"code": "FW", "name": "Footwear"},
	})
	if err == nil {
		t.Fatal("expected quota error")
	}
}

func TestCreateEntity_DataMissingIDField(t *testing.T) {
	t.Parallel()
	uc, _, sr, _, _, _, _, _, _ := newUsecase()
	sr.getFn = func(ctx context.Context, tenantID, bundle, entityType string) (*domain.KGEntitySchema, error) {
		return newSchemaFixture(t), nil
	}
	_, err := uc.CreateEntity(context.Background(), kgmutate.CreateEntityInput{
		TenantID:   "tenant-x",
		BundleName: "bundle-x",
		EntityType: "category",
		Data:       map[string]any{"name": "no code field here"},
	})
	if err == nil {
		t.Fatal("expected missing-id-field error")
	}
}

// --- UpdateEntity tests ---

func TestUpdateEntity_Happy(t *testing.T) {
	t.Parallel()
	uc, _, sr, er, _, _, _, _, inv := newUsecase()
	sr.getFn = func(ctx context.Context, tenantID, bundle, entityType string) (*domain.KGEntitySchema, error) {
		return newSchemaFixture(t), nil
	}
	er.getFn = func(ctx context.Context, tenantID, bundle, entityType, id string) (*domain.KGEntity, error) {
		return &domain.KGEntity{
			TenantID: "tenant-x", BundleName: "bundle-x", EntityType: "category",
			EntityID: "FW", Data: []byte(`{"code":"FW","name":"Old Name"}`),
			SchemaHash: "h",
		}, nil
	}
	entity, err := uc.UpdateEntity(context.Background(), kgmutate.UpdateEntityInput{
		TenantID:   "tenant-x",
		BundleName: "bundle-x",
		EntityType: "category",
		EntityID:   "FW",
		Data:       map[string]any{"code": "FW", "name": "Updated Name"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entity == nil {
		t.Fatal("expected entity")
	}
	if len(inv.calls) != 1 {
		t.Errorf("expected exactly 1 invalidate call, got %d", len(inv.calls))
	}
}

func TestUpdateEntity_NotFound(t *testing.T) {
	t.Parallel()
	uc, _, sr, _, _, _, _, _, _ := newUsecase()
	sr.getFn = func(ctx context.Context, tenantID, bundle, entityType string) (*domain.KGEntitySchema, error) {
		return newSchemaFixture(t), nil
	}
	// entityRepo.getFn returns nil — entity not found
	_, err := uc.UpdateEntity(context.Background(), kgmutate.UpdateEntityInput{
		TenantID: "t", BundleName: "good-bundle", EntityType: "category", EntityID: "ANY",
		Data: map[string]any{"code": "FW", "name": "Footwear"},
	})
	if err == nil {
		t.Fatal("expected NotFound error")
	}
}

func TestUpdateEntity_IDMismatch(t *testing.T) {
	t.Parallel()
	uc, _, sr, er, _, _, _, _, _ := newUsecase()
	sr.getFn = func(ctx context.Context, tenantID, bundle, entityType string) (*domain.KGEntitySchema, error) {
		return newSchemaFixture(t), nil
	}
	er.getFn = func(ctx context.Context, tenantID, bundle, entityType, id string) (*domain.KGEntity, error) {
		return &domain.KGEntity{
			TenantID: "t", BundleName: "good-bundle", EntityType: "category", EntityID: "FW",
			Data: []byte(`{"code":"FW"}`), SchemaHash: "h",
		}, nil
	}
	// URL says FW, body says DIFF — must reject
	_, err := uc.UpdateEntity(context.Background(), kgmutate.UpdateEntityInput{
		TenantID: "t", BundleName: "good-bundle", EntityType: "category", EntityID: "FW",
		Data: map[string]any{"code": "DIFF", "name": "Different ID"},
	})
	if err == nil {
		t.Fatal("expected id_mismatch error")
	}
}

// --- DeleteEntity tests ---

func TestDeleteEntity_Happy(t *testing.T) {
	t.Parallel()
	uc, _, _, er, _, _, e, _, inv := newUsecase()
	er.getFn = func(ctx context.Context, tenantID, bundle, entityType, id string) (*domain.KGEntity, error) {
		return &domain.KGEntity{
			TenantID: "t", BundleName: "b", EntityType: "category", EntityID: "FW",
			Data: []byte(`{"code":"FW"}`), SchemaHash: "h",
		}, nil
	}
	err := uc.DeleteEntity(context.Background(), "t", "good-bundle", "category", "FW")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if er.deletedFor != "FW" {
		t.Errorf("expected delete called for FW, got %q", er.deletedFor)
	}
	if len(e.calls) != 1 || e.calls[0] != -1 {
		t.Errorf("expected enforcer call with delta=-1, got %v", e.calls)
	}
	if len(inv.calls) != 1 {
		t.Errorf("expected exactly 1 invalidate call, got %d", len(inv.calls))
	}
}

func TestDeleteEntity_Idempotent(t *testing.T) {
	t.Parallel()
	uc, _, _, er, _, _, e, _, _ := newUsecase()
	// entityRepo.getFn returns nil — entity does not exist
	err := uc.DeleteEntity(context.Background(), "t", "good-bundle", "category", "GHOST")
	if err != nil {
		t.Fatalf("delete should be idempotent: %v", err)
	}
	if er.deletedFor != "" {
		t.Error("expected no delete call when entity missing")
	}
	if len(e.calls) != 0 {
		t.Error("expected no quota call when entity missing")
	}
}

func TestDeleteEntity_EmptyID(t *testing.T) {
	t.Parallel()
	uc, _, _, _, _, _, _, _, _ := newUsecase()
	err := uc.DeleteEntity(context.Background(), "t", "good-bundle", "category", "")
	if err == nil {
		t.Fatal("expected error for empty entity_id")
	}
}

// --- UpsertSchema tests ---

func TestUpsertSchema_Happy(t *testing.T) {
	t.Parallel()
	uc, _, sr, _, _, _, _, _, inv := newUsecase()
	_, err := uc.UpsertSchema(context.Background(), kgmutate.UpsertSchemaInput{
		TenantID:   "t",
		BundleName: "good-bundle",
		EntityType: "category",
		SchemaJSON: []byte(validIndustrySchemaJSON),
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if sr.upsertedTo == nil {
		t.Error("expected schema to be persisted")
	}
	if len(inv.calls) != 1 {
		t.Errorf("expected exactly 1 invalidate call, got %d", len(inv.calls))
	}
}

func TestUpsertSchema_BundleNotFound(t *testing.T) {
	t.Parallel()
	uc, br, _, _, _, _, _, _, _ := newUsecase()
	br.getBundleFn = func(ctx context.Context, tenantID, bundleName string) (*domain.KGBundle, error) {
		return nil, nil
	}
	_, err := uc.UpsertSchema(context.Background(), kgmutate.UpsertSchemaInput{
		TenantID: "t", BundleName: "good-bundle", EntityType: "category",
		SchemaJSON: []byte(validIndustrySchemaJSON),
	})
	if err == nil {
		t.Fatal("expected bundle-not-found error")
	}
}

func TestUpsertSchema_BadJSON(t *testing.T) {
	t.Parallel()
	uc, _, _, _, _, _, _, _, _ := newUsecase()
	_, err := uc.UpsertSchema(context.Background(), kgmutate.UpsertSchemaInput{
		TenantID: "t", BundleName: "good-bundle", EntityType: "category",
		SchemaJSON: []byte(`not json at all`),
	})
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestUpsertSchema_Collision(t *testing.T) {
	t.Parallel()
	uc, _, _, _, _, c, _, _, _ := newUsecase()
	c.colliding = []string{"list_industry"}
	_, err := uc.UpsertSchema(context.Background(), kgmutate.UpsertSchemaInput{
		TenantID: "t", BundleName: "good-bundle", EntityType: "category",
		SchemaJSON: []byte(validIndustrySchemaJSON),
	})
	if err == nil {
		t.Fatal("expected collision error")
	}
}

// --- DeleteBundle tests ---

func TestDeleteBundle_Happy(t *testing.T) {
	t.Parallel()
	uc, br, _, _, _, _, e, _, inv := newUsecase()
	br.countEntities = func(ctx context.Context, tenantID, bundleName string) (int, int64, error) {
		return 5, 1024, nil
	}
	err := uc.DeleteBundle(context.Background(), "t", "good-bundle")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !br.deleteCalled {
		t.Error("expected delete called")
	}
	if len(e.calls) != 1 || e.calls[0] != -5 {
		t.Errorf("expected enforcer with delta=-5, got %v", e.calls)
	}
	if len(inv.calls) != 1 {
		t.Errorf("expected exactly 1 invalidate call, got %d", len(inv.calls))
	}
}

func TestDeleteBundle_Idempotent(t *testing.T) {
	t.Parallel()
	uc, br, _, _, _, _, _, _, _ := newUsecase()
	br.getBundleFn = func(ctx context.Context, tenantID, bundleName string) (*domain.KGBundle, error) {
		return nil, nil
	}
	err := uc.DeleteBundle(context.Background(), "t", "ghost-bundle")
	if err != nil {
		t.Fatalf("delete should be idempotent: %v", err)
	}
	if br.deleteCalled {
		t.Error("expected no delete call when bundle missing")
	}
}

func TestDeleteBundle_BadName(t *testing.T) {
	t.Parallel()
	uc, _, _, _, _, _, _, _, _ := newUsecase()
	err := uc.DeleteBundle(context.Background(), "t", "BadName")
	if err == nil {
		t.Fatal("expected invalid-input error")
	}
}
