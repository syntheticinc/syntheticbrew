package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// mockKGReadService captures the last-call args and returns configurable values.
type mockKGReadService struct {
	// returns
	bundles     []KGBundleInfo
	bundle      *KGBundleInfo
	schemas     []KGSchemaInfo
	schema      *KGSchemaInfo
	entitiesRes *KGEntitiesListResponse
	entity      *KGEntityInfo
	err         error

	// captured args
	lastBundleName string
	lastEntityType string
	lastEntityID   string
	lastFilters    map[string]any
	lastLimit      int
	lastOffset     int
}

func (m *mockKGReadService) ListBundles(_ *http.Request) ([]KGBundleInfo, error) {
	return m.bundles, m.err
}

func (m *mockKGReadService) GetBundle(_ *http.Request, bundleName string) (*KGBundleInfo, error) {
	m.lastBundleName = bundleName
	return m.bundle, m.err
}

func (m *mockKGReadService) ListSchemas(_ *http.Request, bundleName string) ([]KGSchemaInfo, error) {
	m.lastBundleName = bundleName
	return m.schemas, m.err
}

func (m *mockKGReadService) GetSchema(_ *http.Request, bundleName, entityType string) (*KGSchemaInfo, error) {
	m.lastBundleName = bundleName
	m.lastEntityType = entityType
	return m.schema, m.err
}

func (m *mockKGReadService) ListEntities(_ *http.Request, bundleName, entityType string, filters map[string]any, limit, offset int) (*KGEntitiesListResponse, error) {
	m.lastBundleName = bundleName
	m.lastEntityType = entityType
	m.lastFilters = filters
	m.lastLimit = limit
	m.lastOffset = offset
	return m.entitiesRes, m.err
}

func (m *mockKGReadService) GetEntity(_ *http.Request, bundleName, entityType, entityID string) (*KGEntityInfo, error) {
	m.lastBundleName = bundleName
	m.lastEntityType = entityType
	m.lastEntityID = entityID
	return m.entity, m.err
}

// newKGReadRouter wires the handler routes for tests using chi so URL params work.
func newKGReadRouter(h *KGReadHandler) *chi.Mux {
	r := chi.NewRouter()
	r.Get("/knowledge-graphs", h.ListBundles)
	r.Get("/knowledge-graphs/{bundle}", h.GetBundle)
	r.Get("/knowledge-graphs/{bundle}/schemas", h.ListSchemas)
	r.Get("/knowledge-graphs/{bundle}/schemas/{entity_type}", h.GetSchema)
	r.Get("/knowledge-graphs/{bundle}/entities/{entity_type}", h.ListEntities)
	r.Get("/knowledge-graphs/{bundle}/entities/{entity_type}/{id}", h.GetEntity)
	return r
}

func TestKGReadHandler_ListBundles_OK(t *testing.T) {
	svc := &mockKGReadService{
		bundles: []KGBundleInfo{
			{BundleName: "demo", Version: "1.0"},
		},
	}
	h := NewKGReadHandler(svc)
	r := newKGReadRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/knowledge-graphs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got []KGBundleInfo
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, w.Body.String())
	}
	if len(got) != 1 || got[0].BundleName != "demo" {
		t.Fatalf("unexpected body: %+v", got)
	}
}

func TestKGReadHandler_ListBundles_ServiceError_500(t *testing.T) {
	svc := &mockKGReadService{err: errors.New("boom")}
	h := NewKGReadHandler(svc)
	r := newKGReadRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/knowledge-graphs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// non-domain error → 500 (per domainErrorToHTTPStatus mapping)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", w.Code, w.Body.String())
	}
}

func TestKGReadHandler_GetBundle_OK(t *testing.T) {
	svc := &mockKGReadService{
		bundle: &KGBundleInfo{BundleName: "demo", Version: "2"},
	}
	h := NewKGReadHandler(svc)
	r := newKGReadRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/knowledge-graphs/demo", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if svc.lastBundleName != "demo" {
		t.Fatalf("svc got bundleName=%q, want demo", svc.lastBundleName)
	}
	var got KGBundleInfo
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Version != "2" {
		t.Fatalf("unexpected body: %+v", got)
	}
}

func TestKGReadHandler_GetSchemas_OK(t *testing.T) {
	svc := &mockKGReadService{
		schemas: []KGSchemaInfo{
			{BundleName: "demo", EntityType: "industry"},
		},
	}
	h := NewKGReadHandler(svc)
	r := newKGReadRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/knowledge-graphs/demo/schemas", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if svc.lastBundleName != "demo" {
		t.Fatalf("svc got bundleName=%q, want demo", svc.lastBundleName)
	}
}

func TestKGReadHandler_GetSchemaByType_OK(t *testing.T) {
	svc := &mockKGReadService{
		schema: &KGSchemaInfo{BundleName: "demo", EntityType: "industry", IDField: "code"},
	}
	h := NewKGReadHandler(svc)
	r := newKGReadRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/knowledge-graphs/demo/schemas/industry", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if svc.lastBundleName != "demo" || svc.lastEntityType != "industry" {
		t.Fatalf("svc got bundle=%q type=%q", svc.lastBundleName, svc.lastEntityType)
	}
}

func TestKGReadHandler_ListEntities_DefaultPagination_OK(t *testing.T) {
	svc := &mockKGReadService{
		entitiesRes: &KGEntitiesListResponse{
			Items: []KGEntityInfo{}, Total: 0, Limit: 50, Offset: 0,
		},
	}
	h := NewKGReadHandler(svc)
	r := newKGReadRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/knowledge-graphs/demo/entities/industry", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if svc.lastLimit != 50 {
		t.Fatalf("default limit = %d, want 50", svc.lastLimit)
	}
	if svc.lastOffset != 0 {
		t.Fatalf("default offset = %d, want 0", svc.lastOffset)
	}
}

func TestKGReadHandler_ListEntities_LimitTooHigh_400(t *testing.T) {
	svc := &mockKGReadService{
		entitiesRes: &KGEntitiesListResponse{Items: []KGEntityInfo{}},
	}
	h := NewKGReadHandler(svc)
	r := newKGReadRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/knowledge-graphs/demo/entities/industry?limit=501", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(strings.ToLower(w.Body.String()), "limit") {
		t.Fatalf("expected error to mention 'limit', got %s", w.Body.String())
	}
}

func TestKGReadHandler_ListEntities_LimitTooLow_400(t *testing.T) {
	svc := &mockKGReadService{}
	h := NewKGReadHandler(svc)
	r := newKGReadRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/knowledge-graphs/demo/entities/industry?limit=0", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestKGReadHandler_ListEntities_LimitNonNumeric_400(t *testing.T) {
	svc := &mockKGReadService{}
	h := NewKGReadHandler(svc)
	r := newKGReadRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/knowledge-graphs/demo/entities/industry?limit=abc", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestKGReadHandler_ListEntities_FilterParsing(t *testing.T) {
	svc := &mockKGReadService{
		entitiesRes: &KGEntitiesListResponse{Items: []KGEntityInfo{}},
	}
	h := NewKGReadHandler(svc)
	r := newKGReadRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/knowledge-graphs/demo/entities/industry?filter[code]=PM&filter[name]=foo", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got, want := fmt.Sprint(svc.lastFilters["code"]), "PM"; got != want {
		t.Fatalf("filter[code] = %q, want %q (full=%+v)", got, want, svc.lastFilters)
	}
	if got, want := fmt.Sprint(svc.lastFilters["name"]), "foo"; got != want {
		t.Fatalf("filter[name] = %q, want %q (full=%+v)", got, want, svc.lastFilters)
	}
}

func TestKGReadHandler_GetEntity_NotFound_404(t *testing.T) {
	svc := &mockKGReadService{entity: nil}
	h := NewKGReadHandler(svc)
	r := newKGReadRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/knowledge-graphs/demo/entities/industry/PM", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

func TestKGReadHandler_GetEntity_Found_200(t *testing.T) {
	svc := &mockKGReadService{
		entity: &KGEntityInfo{
			BundleName: "demo", EntityType: "industry", EntityID: "PM",
			Data: json.RawMessage(`{"code":"PM"}`),
		},
	}
	h := NewKGReadHandler(svc)
	r := newKGReadRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/knowledge-graphs/demo/entities/industry/PM", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if svc.lastEntityID != "PM" {
		t.Fatalf("svc got entityID=%q, want PM", svc.lastEntityID)
	}
}
