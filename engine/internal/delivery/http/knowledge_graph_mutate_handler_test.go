package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// mockKGMutateService captures last-call args + configurable returns.
type mockKGMutateService struct {
	createReturn *KGEntityInfo
	updateReturn *KGEntityInfo
	schemaReturn *KGSchemaInfo
	err          error

	bulkCalled       bool
	bulkBundle       string
	bulkReq          BulkImportRequest
	createBundle     string
	createType       string
	createData       map[string]any
	updateBundle     string
	updateType       string
	updateID         string
	updateData       map[string]any
	deleteEntityArgs [3]string
	deleteBundleArg  string
	upsertBundle     string
	upsertType       string
	upsertReq        UpsertSchemaRequest
}

func (m *mockKGMutateService) BulkImport(_ *http.Request, bundleName string, req BulkImportRequest) error {
	m.bulkCalled = true
	m.bulkBundle = bundleName
	m.bulkReq = req
	return m.err
}

func (m *mockKGMutateService) CreateEntity(_ *http.Request, bundleName, entityType string, data map[string]any) (*KGEntityInfo, error) {
	m.createBundle = bundleName
	m.createType = entityType
	m.createData = data
	return m.createReturn, m.err
}

func (m *mockKGMutateService) UpdateEntity(_ *http.Request, bundleName, entityType, entityID string, data map[string]any) (*KGEntityInfo, error) {
	m.updateBundle = bundleName
	m.updateType = entityType
	m.updateID = entityID
	m.updateData = data
	return m.updateReturn, m.err
}

func (m *mockKGMutateService) DeleteEntity(_ *http.Request, bundleName, entityType, entityID string) error {
	m.deleteEntityArgs = [3]string{bundleName, entityType, entityID}
	return m.err
}

func (m *mockKGMutateService) UpsertSchema(_ *http.Request, bundleName, entityType string, req UpsertSchemaRequest) (*KGSchemaInfo, error) {
	m.upsertBundle = bundleName
	m.upsertType = entityType
	m.upsertReq = req
	return m.schemaReturn, m.err
}

func (m *mockKGMutateService) DeleteBundle(_ *http.Request, bundleName string) error {
	m.deleteBundleArg = bundleName
	return m.err
}

func newKGMutateRouter(h *KGMutateHandler) *chi.Mux {
	r := chi.NewRouter()
	r.Post("/knowledge-graphs/{bundle}/import", h.BulkImport)
	r.Post("/knowledge-graphs/{bundle}/entities/{entity_type}", h.CreateEntity)
	r.Put("/knowledge-graphs/{bundle}/entities/{entity_type}/{id}", h.UpdateEntity)
	r.Delete("/knowledge-graphs/{bundle}/entities/{entity_type}/{id}", h.DeleteEntity)
	r.Put("/knowledge-graphs/{bundle}/schemas/{entity_type}", h.UpsertSchema)
	r.Delete("/knowledge-graphs/{bundle}", h.DeleteBundle)
	return r
}

func TestKGMutateHandler_BulkImport_OK(t *testing.T) {
	svc := &mockKGMutateService{}
	h := NewKGMutateHandler(svc)
	r := newKGMutateRouter(h)

	body := `{"version":"1","schemas":[{"entity_type":"industry","schema":{}}],"entities":[]}`
	req := httptest.NewRequest(http.MethodPost, "/knowledge-graphs/demo/import", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !svc.bulkCalled {
		t.Fatal("expected svc.BulkImport to be called")
	}
	if svc.bulkBundle != "demo" {
		t.Fatalf("bundle = %q, want demo", svc.bulkBundle)
	}
	if len(svc.bulkReq.Schemas) != 1 || svc.bulkReq.Schemas[0].EntityType != "industry" {
		t.Fatalf("schemas not parsed: %+v", svc.bulkReq.Schemas)
	}
}

func TestKGMutateHandler_BulkImport_MalformedJSON_400(t *testing.T) {
	svc := &mockKGMutateService{}
	h := NewKGMutateHandler(svc)
	r := newKGMutateRouter(h)

	req := httptest.NewRequest(http.MethodPost, "/knowledge-graphs/demo/import", strings.NewReader(`{not json`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	if svc.bulkCalled {
		t.Fatal("svc should not be called on malformed body")
	}
}

func TestKGMutateHandler_CreateEntity_FlatShape_201(t *testing.T) {
	svc := &mockKGMutateService{
		createReturn: &KGEntityInfo{BundleName: "demo", EntityType: "industry", EntityID: "X"},
	}
	h := NewKGMutateHandler(svc)
	r := newKGMutateRouter(h)

	body := `{"code":"X","name":"Y"}`
	req := httptest.NewRequest(http.MethodPost, "/knowledge-graphs/demo/entities/industry", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	if svc.createBundle != "demo" || svc.createType != "industry" {
		t.Fatalf("svc got bundle=%q type=%q", svc.createBundle, svc.createType)
	}
	if svc.createData["code"] != "X" || svc.createData["name"] != "Y" {
		t.Fatalf("data = %+v, want flat {code:X,name:Y}", svc.createData)
	}
}

func TestKGMutateHandler_CreateEntity_EmptyBody_400(t *testing.T) {
	svc := &mockKGMutateService{}
	h := NewKGMutateHandler(svc)
	r := newKGMutateRouter(h)

	req := httptest.NewRequest(http.MethodPost, "/knowledge-graphs/demo/entities/industry", strings.NewReader(`null`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(strings.ToLower(w.Body.String()), "non-empty") {
		t.Fatalf("expected error to mention 'non-empty', got %s", w.Body.String())
	}
}

func TestKGMutateHandler_CreateEntity_WrappedShape_PassesThrough(t *testing.T) {
	// Handler should treat the body verbatim as the entity. So {"data":{"code":"X"}}
	// is passed as-is to the service; schema validation downstream will reject it.
	svc := &mockKGMutateService{
		createReturn: &KGEntityInfo{BundleName: "demo", EntityType: "industry", EntityID: "wrapped"},
	}
	h := NewKGMutateHandler(svc)
	r := newKGMutateRouter(h)

	body := `{"data":{"code":"X"}}`
	req := httptest.NewRequest(http.MethodPost, "/knowledge-graphs/demo/entities/industry", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	// Must have received the "data" key verbatim (not unwrapped).
	if _, ok := svc.createData["data"]; !ok {
		t.Fatalf("expected svc to receive body verbatim with 'data' key; got %+v", svc.createData)
	}
}

func TestKGMutateHandler_UpdateEntity_FlatShape_200(t *testing.T) {
	svc := &mockKGMutateService{
		updateReturn: &KGEntityInfo{BundleName: "demo", EntityType: "industry", EntityID: "PM"},
	}
	h := NewKGMutateHandler(svc)
	r := newKGMutateRouter(h)

	body := `{"code":"PM","name":"renamed"}`
	req := httptest.NewRequest(http.MethodPut, "/knowledge-graphs/demo/entities/industry/PM", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if svc.updateID != "PM" {
		t.Fatalf("svc got id=%q, want PM", svc.updateID)
	}
	if svc.updateData["name"] != "renamed" {
		t.Fatalf("data = %+v, want flat with name=renamed", svc.updateData)
	}
}

func TestKGMutateHandler_DeleteEntity_204(t *testing.T) {
	svc := &mockKGMutateService{}
	h := NewKGMutateHandler(svc)
	r := newKGMutateRouter(h)

	req := httptest.NewRequest(http.MethodDelete, "/knowledge-graphs/demo/entities/industry/PM", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	if svc.deleteEntityArgs != [3]string{"demo", "industry", "PM"} {
		t.Fatalf("svc got %+v", svc.deleteEntityArgs)
	}
}

func TestKGMutateHandler_DeleteBundle_204(t *testing.T) {
	svc := &mockKGMutateService{}
	h := NewKGMutateHandler(svc)
	r := newKGMutateRouter(h)

	req := httptest.NewRequest(http.MethodDelete, "/knowledge-graphs/demo", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	if svc.deleteBundleArg != "demo" {
		t.Fatalf("svc got bundle=%q, want demo", svc.deleteBundleArg)
	}
}

func TestKGMutateHandler_UpsertSchema_OK(t *testing.T) {
	svc := &mockKGMutateService{
		schemaReturn: &KGSchemaInfo{BundleName: "demo", EntityType: "industry"},
	}
	h := NewKGMutateHandler(svc)
	r := newKGMutateRouter(h)

	body := `{"schema":{"type":"object"},"expose_tools":["list"]}`
	req := httptest.NewRequest(http.MethodPut, "/knowledge-graphs/demo/schemas/industry", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if svc.upsertBundle != "demo" || svc.upsertType != "industry" {
		t.Fatalf("svc got bundle=%q type=%q", svc.upsertBundle, svc.upsertType)
	}
	if len(svc.upsertReq.ExposeTools) != 1 || svc.upsertReq.ExposeTools[0] != "list" {
		t.Fatalf("expose_tools = %+v", svc.upsertReq.ExposeTools)
	}
	// Schema must round-trip as raw JSON.
	var parsedSchema map[string]any
	if err := json.Unmarshal(svc.upsertReq.Schema, &parsedSchema); err != nil {
		t.Fatalf("schema not raw json: %v", err)
	}
	if parsedSchema["type"] != "object" {
		t.Fatalf("schema = %+v", parsedSchema)
	}
}
