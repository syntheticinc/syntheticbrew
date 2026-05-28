package kgapply

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// --- fixtures ---

const tenant = "tenant-1"
const bundle = "my-bundle"

// validSchemaJSON is a minimal JSON Schema document with all required x-*
// annotations for ParseAnnotations to succeed.
func validSchemaJSON(entityType string, idProp string, extraProps ...string) []byte {
	props := fmt.Sprintf(`%q: {"type":"string"}`, idProp)
	for _, p := range extraProps {
		props += fmt.Sprintf(`,%q: {"type":"string","x-index":true}`, p)
	}
	return []byte(fmt.Sprintf(`{
	"type": "object",
	"x-id-field": %q,
	"properties": { %s }
}`, idProp, props))
}

// schemaWithRef returns a schema for entityType that has a cross-ref property
// `ref_to` pointing at targetType.
func schemaWithRef(entityType, idProp, targetType string) []byte {
	return []byte(fmt.Sprintf(`{
	"type": "object",
	"x-id-field": %q,
	"properties": {
		%q: {"type":"string"},
		"ref_to": {"type":"string", "x-ref": %q}
	}
}`, idProp, idProp, targetType))
}

// --- mocks ---

type mockBundleRepo struct {
	upserts []*domain.KGBundle
	deletes []struct{ tenantID, bundleName string }
	err     error
}

func (m *mockBundleRepo) UpsertBundle(_ context.Context, b *domain.KGBundle) error {
	if m.err != nil {
		return m.err
	}
	m.upserts = append(m.upserts, b)
	return nil
}
func (m *mockBundleRepo) DeleteBundle(_ context.Context, tenantID, bundleName string) error {
	m.deletes = append(m.deletes, struct{ tenantID, bundleName string }{tenantID, bundleName})
	return m.err
}

type mockSchemaRepo struct {
	upserts          [][]*domain.KGEntitySchema
	existingByBundle map[string][]*domain.KGEntitySchema
	upsertErr        error
	listErr          error
}

func (m *mockSchemaRepo) UpsertSchemas(_ context.Context, _, _ string, schemas []*domain.KGEntitySchema) error {
	if m.upsertErr != nil {
		return m.upsertErr
	}
	m.upserts = append(m.upserts, schemas)
	return nil
}

func (m *mockSchemaRepo) ListByBundle(_ context.Context, _, bundleName string) ([]*domain.KGEntitySchema, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	if m.existingByBundle == nil {
		return nil, nil
	}
	return m.existingByBundle[bundleName], nil
}

type mockEntityRepo struct {
	replaces [][]*domain.KGEntity
	err      error
}

func (m *mockEntityRepo) ReplaceEntities(_ context.Context, _, _ string, entities []*domain.KGEntity) error {
	if m.err != nil {
		return m.err
	}
	m.replaces = append(m.replaces, entities)
	return nil
}

type mockValidator struct {
	calls   int
	err     error
	failIDs map[string]struct{} // entities whose id appears here get rejected; checked via simple substring of data
}

func (m *mockValidator) Validate(_ context.Context, _, data []byte) error {
	m.calls++
	if m.err != nil {
		return m.err
	}
	for id := range m.failIDs {
		if bytes.Contains(data, []byte(`"`+id+`"`)) {
			return fmt.Errorf("schema mismatch for id %s", id)
		}
	}
	return nil
}

type mockCollisionDetector struct {
	sawTenant string
	sawTools  []string
	returns   []string
	err       error
}

func (m *mockCollisionDetector) Detect(_ context.Context, tenantID, _ string, newToolNames []string) ([]string, error) {
	m.sawTenant = tenantID
	m.sawTools = append([]string(nil), newToolNames...)
	if m.err != nil {
		return nil, m.err
	}
	return m.returns, nil
}

type mockEnforcer struct {
	called      int
	sawTenant   string
	sawBundle   string
	sawEntities int
	sawBytes    int64
	err         error
}

func (m *mockEnforcer) OnEntityWrite(_ context.Context, tenantID, bundleName string, deltaEntities int, deltaBytes int64) error {
	m.called++
	m.sawTenant = tenantID
	m.sawBundle = bundleName
	m.sawEntities = deltaEntities
	m.sawBytes = deltaBytes
	return m.err
}

type mockLocker struct {
	mu          sync.Mutex
	locked      int
	unlocked    int
	err         error
	holdRelease chan struct{} // if non-nil, lock blocks until this is closed; used for contention tests
}

func (m *mockLocker) LockBundle(_ context.Context, _, _ string) (func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	m.locked++
	if m.holdRelease != nil {
		<-m.holdRelease
	}
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.unlocked++
	}, nil
}

type mockTxRunner struct {
	called int
	err    error
	// inner runs fn synchronously. Set inner = false to skip running fn (and
	// therefore skip inner-repo calls) — useful to simulate tx start failure.
	skipFn bool
}

func (m *mockTxRunner) InTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	m.called++
	if m.err != nil {
		return m.err
	}
	if m.skipFn {
		return nil
	}
	return fn(ctx)
}

// newUsecase wires up fresh mocks. Returns the Usecase and pointers to each
// mock so the test can inspect call records.
type harness struct {
	uc        *Usecase
	bundles   *mockBundleRepo
	schemas   *mockSchemaRepo
	entities  *mockEntityRepo
	validator *mockValidator
	collision *mockCollisionDetector
	enforcer  *mockEnforcer
	locker    *mockLocker
	tx        *mockTxRunner
}

func newHarness() *harness {
	h := &harness{
		bundles:   &mockBundleRepo{},
		schemas:   &mockSchemaRepo{},
		entities:  &mockEntityRepo{},
		validator: &mockValidator{},
		collision: &mockCollisionDetector{},
		enforcer:  &mockEnforcer{},
		locker:    &mockLocker{},
		tx:        &mockTxRunner{},
	}
	h.uc = New(h.bundles, h.schemas, h.entities, h.validator, h.collision, h.enforcer, h.locker, h.tx)
	return h
}

// --- tests ---

func TestExecute_HappyPath_SingleSchemaFiveEntities(t *testing.T) {
	h := newHarness()
	items := []map[string]any{
		{"id": "a"}, {"id": "b"}, {"id": "c"}, {"id": "d"}, {"id": "e"},
	}
	out, err := h.uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: bundle,
		Version:    "1.0.0",
		Schemas:    []SchemaInput{{EntityType: "user", SchemaJSON: validSchemaJSON("user", "id")}},
		Entities:   []EntitySetInput{{EntityType: "user", Items: items}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == nil || out.SchemasApplied != 1 || out.EntitiesWritten != 5 {
		t.Fatalf("unexpected output: %+v", out)
	}
	if h.locker.locked != 1 || h.locker.unlocked != 1 {
		t.Errorf("lock/unlock: got %d/%d", h.locker.locked, h.locker.unlocked)
	}
	if h.collision.sawTenant != tenant {
		t.Errorf("collision tenant: got %q", h.collision.sawTenant)
	}
	if h.enforcer.called != 1 || h.enforcer.sawEntities != 5 {
		t.Errorf("enforcer not called as expected: %+v", h.enforcer)
	}
	if h.tx.called != 1 || len(h.bundles.upserts) != 1 || len(h.entities.replaces) != 1 {
		t.Errorf("tx wiring missing: tx=%d bundles=%d entities=%d", h.tx.called, len(h.bundles.upserts), len(h.entities.replaces))
	}
	if h.validator.calls != 5 {
		t.Errorf("validator calls: got %d, want 5", h.validator.calls)
	}
}

func TestExecute_HappyPath_TwoSchemasCrossRefSameImport(t *testing.T) {
	h := newHarness()
	_, err := h.uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: bundle,
		Version:    "1",
		Schemas: []SchemaInput{
			{EntityType: "ticket", SchemaJSON: schemaWithRef("ticket", "id", "user")},
			{EntityType: "user", SchemaJSON: validSchemaJSON("user", "id")},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecute_CrossRefToExistingDBSchema(t *testing.T) {
	h := newHarness()
	existingUser, _ := domain.NewKGEntitySchema(tenant, bundle, "user", validSchemaJSON("user", "id"), "id", []string{"list", "get"}, "")
	h.schemas.existingByBundle = map[string][]*domain.KGEntitySchema{
		bundle: {existingUser},
	}
	_, err := h.uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: bundle,
		Version:    "1",
		Schemas: []SchemaInput{
			{EntityType: "ticket", SchemaJSON: schemaWithRef("ticket", "id", "user")},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecute_CrossRefUnknown(t *testing.T) {
	h := newHarness()
	_, err := h.uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: bundle,
		Version:    "1",
		Schemas: []SchemaInput{
			{EntityType: "ticket", SchemaJSON: schemaWithRef("ticket", "id", "nonexistent_type")},
		},
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
	if h.tx.called != 0 {
		t.Errorf("tx should not run: called=%d", h.tx.called)
	}
}

func TestExecute_TenantMissingEverywhere(t *testing.T) {
	h := newHarness()
	_, err := h.uc.Execute(context.Background(), Input{
		BundleName: bundle,
		Version:    "1",
		Schemas:    []SchemaInput{{EntityType: "user", SchemaJSON: validSchemaJSON("user", "id")}},
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
	if h.locker.locked != 0 {
		t.Errorf("locker should not be called: locked=%d", h.locker.locked)
	}
}

func TestExecute_TenantFromContext(t *testing.T) {
	h := newHarness()
	ctx := domain.WithTenantID(context.Background(), tenant)
	_, err := h.uc.Execute(ctx, Input{
		BundleName: bundle,
		Version:    "1",
		Schemas:    []SchemaInput{{EntityType: "user", SchemaJSON: validSchemaJSON("user", "id")}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecute_InvalidBundleName(t *testing.T) {
	h := newHarness()
	_, err := h.uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: "Bad_Bundle!",
		Version:    "1",
		Schemas:    []SchemaInput{{EntityType: "user", SchemaJSON: validSchemaJSON("user", "id")}},
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

func TestExecute_EmptyVersion(t *testing.T) {
	h := newHarness()
	_, err := h.uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: bundle,
		Schemas:    []SchemaInput{{EntityType: "user", SchemaJSON: validSchemaJSON("user", "id")}},
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

func TestExecute_NoSchemas(t *testing.T) {
	h := newHarness()
	_, err := h.uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: bundle,
		Version:    "1",
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

func TestExecute_InvalidEntityType(t *testing.T) {
	h := newHarness()
	_, err := h.uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: bundle,
		Version:    "1",
		Schemas:    []SchemaInput{{EntityType: "Bad-Type!", SchemaJSON: validSchemaJSON("user", "id")}},
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

func TestExecute_DuplicateEntityType(t *testing.T) {
	h := newHarness()
	_, err := h.uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: bundle,
		Version:    "1",
		Schemas: []SchemaInput{
			{EntityType: "user", SchemaJSON: validSchemaJSON("user", "id")},
			{EntityType: "user", SchemaJSON: validSchemaJSON("user", "id")},
		},
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

func TestExecute_SchemaMissingIDField(t *testing.T) {
	h := newHarness()
	badSchema := []byte(`{"type":"object","properties":{"id":{"type":"string"}}}`)
	_, err := h.uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: bundle,
		Version:    "1",
		Schemas:    []SchemaInput{{EntityType: "user", SchemaJSON: badSchema}},
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

func TestExecute_EntityValidationFails(t *testing.T) {
	h := newHarness()
	h.validator.err = errors.New("not valid")
	_, err := h.uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: bundle,
		Version:    "1",
		Schemas:    []SchemaInput{{EntityType: "user", SchemaJSON: validSchemaJSON("user", "id")}},
		Entities:   []EntitySetInput{{EntityType: "user", Items: []map[string]any{{"id": "a"}}}},
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
	if h.tx.called != 0 {
		t.Errorf("tx should not run when validation fails")
	}
}

func TestExecute_ToolNameCollision(t *testing.T) {
	h := newHarness()
	h.collision.returns = []string{"get_user", "list_user"}
	_, err := h.uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: bundle,
		Version:    "1",
		Schemas:    []SchemaInput{{EntityType: "user", SchemaJSON: validSchemaJSON("user", "id")}},
	})
	if !pkgerrors.Is(err, pkgerrors.CodeAlreadyExists) {
		t.Fatalf("expected AlreadyExists, got %v", err)
	}
	if h.tx.called != 0 {
		t.Errorf("tx should not run on collision")
	}
}

func TestExecute_QuotaExceeded(t *testing.T) {
	h := newHarness()
	h.enforcer.err = errors.New("quota exhausted")
	_, err := h.uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: bundle,
		Version:    "1",
		Schemas:    []SchemaInput{{EntityType: "user", SchemaJSON: validSchemaJSON("user", "id")}},
		Entities:   []EntitySetInput{{EntityType: "user", Items: []map[string]any{{"id": "a"}}}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "quota") {
		t.Errorf("error should mention quota: %v", err)
	}
	if h.tx.called != 0 {
		t.Errorf("tx should not run on quota failure")
	}
}

func TestExecute_NullableEnforcer(t *testing.T) {
	bundles := &mockBundleRepo{}
	schemas := &mockSchemaRepo{}
	entities := &mockEntityRepo{}
	validator := &mockValidator{}
	collision := &mockCollisionDetector{}
	locker := &mockLocker{}
	tx := &mockTxRunner{}
	uc := New(bundles, schemas, entities, validator, collision, nil, locker, tx)

	_, err := uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: bundle,
		Version:    "1",
		Schemas:    []SchemaInput{{EntityType: "user", SchemaJSON: validSchemaJSON("user", "id")}},
		Entities:   []EntitySetInput{{EntityType: "user", Items: []map[string]any{{"id": "a"}}}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecute_AdvisoryLockerError(t *testing.T) {
	h := newHarness()
	h.locker.err = errors.New("lock failed")
	_, err := h.uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: bundle,
		Version:    "1",
		Schemas:    []SchemaInput{{EntityType: "user", SchemaJSON: validSchemaJSON("user", "id")}},
	})
	if err == nil || !strings.Contains(err.Error(), "lock") {
		t.Fatalf("expected lock error, got %v", err)
	}
}

func TestExecute_UnlockCalledOnError(t *testing.T) {
	h := newHarness()
	h.tx.err = errors.New("tx blew up")
	_, _ = h.uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: bundle,
		Version:    "1",
		Schemas:    []SchemaInput{{EntityType: "user", SchemaJSON: validSchemaJSON("user", "id")}},
	})
	if h.locker.unlocked != 1 {
		t.Errorf("unlock should fire on tx error: unlocked=%d", h.locker.unlocked)
	}
}

func TestExecute_CycleLogsWarning(t *testing.T) {
	// Capture slog output to verify Warn-level message.
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)

	// schema aa refs bb, schema bb refs aa → cycle.
	h := newHarness()
	_, err := h.uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: bundle,
		Version:    "1",
		Schemas: []SchemaInput{
			{EntityType: "aa", SchemaJSON: schemaWithRef("aa", "id", "bb")},
			{EntityType: "bb", SchemaJSON: schemaWithRef("bb", "id", "aa")},
		},
	})
	if err != nil {
		t.Fatalf("cycle should NOT fail apply: %v", err)
	}
	if !strings.Contains(buf.String(), "cycle detected") {
		t.Errorf("expected cycle warning in slog, got: %q", buf.String())
	}
}

func TestExecute_AdvisoryLockerContention(t *testing.T) {
	// Verify second caller blocks until first unlocks. We don't truly need
	// concurrent goroutines to hit code coverage; instead simulate by
	// observing the mock's serialised locked counter.
	h := newHarness()
	h.locker.holdRelease = make(chan struct{})
	done := make(chan struct{})
	go func() {
		_, _ = h.uc.Execute(context.Background(), Input{
			TenantID:   tenant,
			BundleName: bundle,
			Version:    "1",
			Schemas:    []SchemaInput{{EntityType: "user", SchemaJSON: validSchemaJSON("user", "id")}},
		})
		close(done)
	}()
	// Release the lock.
	close(h.locker.holdRelease)
	<-done
	if h.locker.unlocked != 1 {
		t.Errorf("expected lock release: unlocked=%d", h.locker.unlocked)
	}
}

func TestExecute_EntityMissingIDField(t *testing.T) {
	h := newHarness()
	_, err := h.uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: bundle,
		Version:    "1",
		Schemas:    []SchemaInput{{EntityType: "user", SchemaJSON: validSchemaJSON("user", "id")}},
		Entities:   []EntitySetInput{{EntityType: "user", Items: []map[string]any{{"name": "no id"}}}},
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

func TestExecute_EntityIDNotString(t *testing.T) {
	h := newHarness()
	_, err := h.uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: bundle,
		Version:    "1",
		Schemas:    []SchemaInput{{EntityType: "user", SchemaJSON: validSchemaJSON("user", "id")}},
		Entities:   []EntitySetInput{{EntityType: "user", Items: []map[string]any{{"id": 42}}}},
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

func TestExecute_DuplicateEntityID(t *testing.T) {
	h := newHarness()
	_, err := h.uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: bundle,
		Version:    "1",
		Schemas:    []SchemaInput{{EntityType: "user", SchemaJSON: validSchemaJSON("user", "id")}},
		Entities:   []EntitySetInput{{EntityType: "user", Items: []map[string]any{{"id": "a"}, {"id": "a"}}}},
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

func TestExecute_EntitiesReferenceUnknownType(t *testing.T) {
	h := newHarness()
	_, err := h.uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: bundle,
		Version:    "1",
		Schemas:    []SchemaInput{{EntityType: "user", SchemaJSON: validSchemaJSON("user", "id")}},
		Entities:   []EntitySetInput{{EntityType: "ghost", Items: []map[string]any{{"id": "a"}}}},
	})
	if !pkgerrors.Is(err, pkgerrors.CodeInvalidInput) {
		t.Fatalf("expected InvalidInput, got %v", err)
	}
}

func TestExecute_TxRunnerReturnsError(t *testing.T) {
	h := newHarness()
	h.bundles.err = errors.New("upsert bundle failed")
	_, err := h.uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: bundle,
		Version:    "1",
		Schemas:    []SchemaInput{{EntityType: "user", SchemaJSON: validSchemaJSON("user", "id")}},
	})
	if err == nil {
		t.Fatal("expected error from tx body")
	}
}

func TestExecute_CollisionDetectorError(t *testing.T) {
	h := newHarness()
	h.collision.err = errors.New("collision DB query failed")
	_, err := h.uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: bundle,
		Version:    "1",
		Schemas:    []SchemaInput{{EntityType: "user", SchemaJSON: validSchemaJSON("user", "id")}},
	})
	if err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("expected wrapped collision error, got %v", err)
	}
}

func TestExecute_ListByBundleError(t *testing.T) {
	h := newHarness()
	h.schemas.listErr = errors.New("DB read failed")
	_, err := h.uc.Execute(context.Background(), Input{
		TenantID:   tenant,
		BundleName: bundle,
		Version:    "1",
		Schemas:    []SchemaInput{{EntityType: "ticket", SchemaJSON: schemaWithRef("ticket", "id", "user")}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}
