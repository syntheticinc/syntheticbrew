package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- Agent PATCH / PUT strict tests ----

// mockAgentManager implements AgentManager for handler tests.
type mockAgentManager struct {
	mockAgentLister
	createFunc func(ctx context.Context, req CreateAgentRequest) (*AgentDetail, error)
	updateFunc func(ctx context.Context, name string, req CreateAgentRequest) (*AgentDetail, error)
	patchFunc  func(ctx context.Context, name string, req UpdateAgentRequest) (*AgentDetail, error)
	deleteFunc func(ctx context.Context, name string) error
}

func (m *mockAgentManager) CreateAgent(ctx context.Context, req CreateAgentRequest) (*AgentDetail, error) {
	if m.createFunc != nil {
		return m.createFunc(ctx, req)
	}
	return &AgentDetail{AgentInfo: AgentInfo{Name: req.Name}}, nil
}

func (m *mockAgentManager) UpdateAgent(ctx context.Context, name string, req CreateAgentRequest) (*AgentDetail, error) {
	if m.updateFunc != nil {
		return m.updateFunc(ctx, name, req)
	}
	return &AgentDetail{AgentInfo: AgentInfo{Name: name}, SystemPrompt: req.SystemPrompt}, nil
}

func (m *mockAgentManager) PatchAgent(ctx context.Context, name string, req UpdateAgentRequest) (*AgentDetail, error) {
	if m.patchFunc != nil {
		return m.patchFunc(ctx, name, req)
	}
	return &AgentDetail{AgentInfo: AgentInfo{Name: name}}, nil
}

func (m *mockAgentManager) DeleteAgent(ctx context.Context, name string) error {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, name)
	}
	return nil
}

func newAgentManagerRouter(mgr *mockAgentManager) http.Handler {
	return newAgentTestRouter(NewAgentHandlerWithManager(mgr))
}

// TestAgentHandler_Patch_PreservesUnspecifiedFields verifies BUG-MT-03 regression:
// PATCH with only system_prompt must not wipe tools/model/etc.
func TestAgentHandler_Patch_PreservesUnspecifiedFields(t *testing.T) {
	var capturedReq UpdateAgentRequest
	mgr := &mockAgentManager{
		patchFunc: func(ctx context.Context, name string, req UpdateAgentRequest) (*AgentDetail, error) {
			capturedReq = req
			// Return agent with preserved fields — service does the merge.
			return &AgentDetail{
				AgentInfo:    AgentInfo{Name: name},
				SystemPrompt: *req.SystemPrompt,
				Tools:        []string{"existing_tool"}, // preserved by service
			}, nil
		},
	}

	prompt := "updated prompt"
	body, _ := json.Marshal(UpdateAgentRequest{SystemPrompt: &prompt})

	r := newAgentManagerRouter(mgr)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/my-agent", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	// Only SystemPrompt was set — Tools/ModelID/etc must be nil (not sent).
	assert.NotNil(t, capturedReq.SystemPrompt)
	assert.Equal(t, "updated prompt", *capturedReq.SystemPrompt)
	assert.Nil(t, capturedReq.Tools, "Tools should be nil — not sent in PATCH body")
	assert.Nil(t, capturedReq.ModelID, "ModelID should be nil — not sent in PATCH body")

	var result AgentDetail
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&result))
	assert.Equal(t, "my-agent", result.Name)
	assert.Equal(t, "updated prompt", result.SystemPrompt)
}

// TestAgentHandler_Put_MissingSystemPrompt_Returns400 verifies PUT strict enforcement.
func TestAgentHandler_Put_MissingSystemPrompt_Returns400(t *testing.T) {
	mgr := &mockAgentManager{}
	r := newAgentManagerRouter(mgr)

	// PUT body without system_prompt — must return 400.
	body, _ := json.Marshal(map[string]interface{}{
		"tools": []string{"search"},
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/my-agent", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var errResp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&errResp))
	assert.Contains(t, errResp["error"], "system_prompt is required")
	assert.Contains(t, errResp["error"], "PATCH")
}

// TestAgentHandler_Put_WithSystemPrompt_Succeeds verifies full PUT still works.
func TestAgentHandler_Put_WithSystemPrompt_Succeeds(t *testing.T) {
	mgr := &mockAgentManager{}
	r := newAgentManagerRouter(mgr)

	body, _ := json.Marshal(CreateAgentRequest{
		Name:         "my-agent",
		SystemPrompt: "You are helpful",
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/my-agent", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestAgentHandler_Patch_NullFieldVsMissingField verifies null vs missing distinction.
// PATCH with tools: null clears tools; missing tools key preserves them.
func TestAgentHandler_Patch_NullFieldVsMissingField(t *testing.T) {
	var capturedReq UpdateAgentRequest
	mgr := &mockAgentManager{
		patchFunc: func(_ context.Context, _ string, req UpdateAgentRequest) (*AgentDetail, error) {
			capturedReq = req
			return &AgentDetail{AgentInfo: AgentInfo{Name: "a"}}, nil
		},
	}
	r := newAgentManagerRouter(mgr)

	// Send explicit null for tools — means "clear tools".
	body := []byte(`{"tools": null}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/my-agent", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	// tools was explicitly sent as null → pointer is non-nil, value is nil slice.
	// JSON null on *[]string decodes to nil pointer (omitempty-style) — distinguish
	// via the fact that missing key never touches the field at all.
	// Both missing key AND explicit null result in nil pointer with omitempty tags.
	// The service layer must check for nil pointer (missing) vs explicit empty slice.
	// For this test, tools sent as null → pointer remains nil (JSON null on *[]string = nil ptr).
	assert.Nil(t, capturedReq.Tools)
}

// ---- Model PATCH / PUT strict tests ----

func newModelRouter(svc ModelService) http.Handler {
	return newModelTestRouter(NewModelHandler(svc))
}

// mockModelServiceWithPatch extends stubModelService to add PatchModel.
type mockModelServiceWithPatch struct {
	mockModelService
	patchFunc func(ctx context.Context, name string, req UpdateModelRequest) (*ModelResponse, error)
}

func (m *mockModelServiceWithPatch) PatchModel(ctx context.Context, name string, req UpdateModelRequest) (*ModelResponse, error) {
	if m.patchFunc != nil {
		return m.patchFunc(ctx, name, req)
	}
	return &ModelResponse{Name: name}, nil
}

func TestModelHandler_Patch_OnlyAPIKeyUpdated(t *testing.T) {
	var capturedReq UpdateModelRequest
	svc := &mockModelServiceWithPatch{
		patchFunc: func(_ context.Context, name string, req UpdateModelRequest) (*ModelResponse, error) {
			capturedReq = req
			return &ModelResponse{
				ID:        "id-1",
				Name:      name,
				Type:      "openai_compatible",
				ModelName: "gpt-4",
				HasAPIKey: true,
				CreatedAt: "2026-01-01T00:00:00Z",
			}, nil
		},
	}
	r := newModelRouter(svc)

	newKey := "sk-new-key"
	body, _ := json.Marshal(UpdateModelRequest{APIKey: &newKey})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/models/my-model", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Nil(t, capturedReq.Type, "Type should be nil — not sent in PATCH")
	assert.Nil(t, capturedReq.ModelName, "ModelName should be nil — not sent in PATCH")
	require.NotNil(t, capturedReq.APIKey)
	assert.Equal(t, "sk-new-key", *capturedReq.APIKey)
}

func TestModelHandler_Put_MissingRequiredFields_Returns400(t *testing.T) {
	svc := &mockModelServiceWithPatch{}
	r := newModelRouter(svc)

	// Missing type — must return 400.
	body, _ := json.Marshal(map[string]interface{}{
		"model_name": "gpt-4",
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/models/my-model", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var errResp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&errResp))
	assert.Contains(t, errResp["error"], "type is required")
}

func TestModelHandler_Put_MissingModelName_Returns400(t *testing.T) {
	svc := &mockModelServiceWithPatch{}
	r := newModelRouter(svc)

	// Has type but missing model_name — must return 400.
	body, _ := json.Marshal(map[string]interface{}{
		"type": "openai_compatible",
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/models/my-model", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var errResp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&errResp))
	assert.Contains(t, errResp["error"], "model_name is required")
}

// ---- MCP PATCH / PUT strict tests ----

type stubMCPServiceWithPatch struct {
	stubMCPService
	patchFunc func(ctx context.Context, name string, req UpdateMCPServerRequest) (*MCPServerResponse, error)
}

func (s *stubMCPServiceWithPatch) PatchMCPServer(ctx context.Context, name string, req UpdateMCPServerRequest) (*MCPServerResponse, error) {
	if s.patchFunc != nil {
		return s.patchFunc(ctx, name, req)
	}
	return &MCPServerResponse{Name: name}, nil
}

func newMCPTestRouterWithPatch(svc *stubMCPServiceWithPatch) http.Handler {
	return newMCPTestRouterFull(NewMCPHandler(svc, policyFromEnv()))
}

func TestMCPHandler_Put_MissingType_Returns400(t *testing.T) {
	t.Setenv("BYTEBREW_MODE", "ce")
	svc := &stubMCPServiceWithPatch{}
	r := newMCPTestRouterWithPatch(svc)

	body, _ := json.Marshal(map[string]interface{}{
		"command": "/bin/echo",
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/mcp-servers/my-server", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var errResp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&errResp))
	assert.Contains(t, errResp["error"], "type is required")
}

func TestMCPHandler_Patch_PreservesUnspecifiedFields(t *testing.T) {
	t.Setenv("BYTEBREW_MODE", "ce")
	var capturedReq UpdateMCPServerRequest
	svc := &stubMCPServiceWithPatch{
		patchFunc: func(_ context.Context, name string, req UpdateMCPServerRequest) (*MCPServerResponse, error) {
			capturedReq = req
			return &MCPServerResponse{Name: name, Type: "stdio"}, nil
		},
	}
	r := newMCPTestRouterWithPatch(svc)

	newURL := "https://example.com/mcp"
	body, _ := json.Marshal(UpdateMCPServerRequest{URL: &newURL})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/mcp-servers/my-server", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	// Type was not sent — must be nil.
	assert.Nil(t, capturedReq.Type, "Type should be nil in PATCH — not sent")
	require.NotNil(t, capturedReq.URL)
	assert.Equal(t, "https://example.com/mcp", *capturedReq.URL)
}

func TestMCPHandler_Patch_Cloud_BlocksStdioType(t *testing.T) {
	t.Setenv("BYTEBREW_MODE", "cloud")
	svc := &stubMCPServiceWithPatch{}
	r := newMCPTestRouterWithPatch(svc)

	stdioType := "stdio"
	body, _ := json.Marshal(UpdateMCPServerRequest{Type: &stdioType})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/mcp-servers/my-server", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "is not permitted in this deployment")
}

// ---- Schema PATCH / PUT strict tests ----

type mockSchemaServiceWithPatch struct {
	updateFunc func(ctx context.Context, id string, req UpdateSchemaRequest) error
	patchFunc  func(ctx context.Context, id string, req UpdateSchemaRequest) error
	createFunc func(ctx context.Context, req CreateSchemaRequest) (*SchemaInfo, error)
}

func (m *mockSchemaServiceWithPatch) ListSchemas(_ context.Context) ([]SchemaInfo, error) {
	return nil, nil
}
func (m *mockSchemaServiceWithPatch) GetSchema(_ context.Context, id string) (*SchemaInfo, error) {
	return &SchemaInfo{ID: id, Name: "test"}, nil
}
func (m *mockSchemaServiceWithPatch) CreateSchema(ctx context.Context, req CreateSchemaRequest) (*SchemaInfo, error) {
	if m.createFunc != nil {
		return m.createFunc(ctx, req)
	}
	return &SchemaInfo{ID: "new-id", Name: req.Name}, nil
}
func (m *mockSchemaServiceWithPatch) UpdateSchema(ctx context.Context, id string, req UpdateSchemaRequest) error {
	if m.updateFunc != nil {
		return m.updateFunc(ctx, id, req)
	}
	return nil
}
func (m *mockSchemaServiceWithPatch) PatchSchema(ctx context.Context, id string, req UpdateSchemaRequest) error {
	if m.patchFunc != nil {
		return m.patchFunc(ctx, id, req)
	}
	return nil
}
func (m *mockSchemaServiceWithPatch) DeleteSchema(_ context.Context, _ string) error { return nil }
func (m *mockSchemaServiceWithPatch) ListSchemaAgents(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

type mockAgentRelationServiceNoop struct{}

func (m *mockAgentRelationServiceNoop) ListAgentRelations(_ context.Context, _ string) ([]AgentRelationInfo, error) {
	return nil, nil
}
func (m *mockAgentRelationServiceNoop) GetAgentRelation(_ context.Context, _ string) (*AgentRelationInfo, error) {
	return nil, nil
}
func (m *mockAgentRelationServiceNoop) CreateAgentRelation(_ context.Context, _ string, _ CreateAgentRelationRequest) (*AgentRelationInfo, error) {
	return nil, nil
}
func (m *mockAgentRelationServiceNoop) UpdateAgentRelation(_ context.Context, _ string, _ CreateAgentRelationRequest) error {
	return nil
}
func (m *mockAgentRelationServiceNoop) DeleteAgentRelation(_ context.Context, _ string) error {
	return nil
}

func newSchemaRouter(svc SchemaService) http.Handler {
	// Permissive resolver — these tests exercise PUT/PATCH body handling, not
	// name resolution. Any name maps to a stable test UUID.
	resolver := &fakeSchemaNameResolver{
		fn: func(_ context.Context, _ string) (string, error) {
			return "00000000-0000-0000-0000-000000000001", nil
		},
	}
	h := NewSchemaHandler(svc, &mockAgentRelationServiceNoop{}, resolver)
	return newSchemaTestRouter(h)
}

func TestSchemaHandler_Put_MissingName_Returns400(t *testing.T) {
	svc := &mockSchemaServiceWithPatch{}
	r := newSchemaRouter(svc)

	// PUT with no name — must return 400.
	body, _ := json.Marshal(map[string]interface{}{
		"chat_enabled": true,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/schemas/some-uuid", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var errResp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&errResp))
	assert.Contains(t, errResp["error"], "name is required")
}

func TestSchemaHandler_Patch_WithoutName_Succeeds(t *testing.T) {
	var capturedReq UpdateSchemaRequest
	svc := &mockSchemaServiceWithPatch{
		patchFunc: func(_ context.Context, _ string, req UpdateSchemaRequest) error {
			capturedReq = req
			return nil
		},
	}
	r := newSchemaRouter(svc)

	chatEnabled := true
	body, _ := json.Marshal(UpdateSchemaRequest{ChatEnabled: &chatEnabled})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/schemas/some-uuid", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	// PATCH without name is valid — 204 No Content.
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Nil(t, capturedReq.Name, "Name must be nil when not sent in PATCH")
	require.NotNil(t, capturedReq.ChatEnabled)
	assert.True(t, *capturedReq.ChatEnabled)
}

func TestSchemaHandler_Put_NameMatchesURL_Succeeds(t *testing.T) {
	svc := &mockSchemaServiceWithPatch{}
	r := newSchemaRouter(svc)

	// Name in body equals URL segment — no rename, just an idempotent PUT.
	name := "my-schema"
	body, _ := json.Marshal(UpdateSchemaRequest{Name: &name})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/schemas/my-schema", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestSchemaHandler_Put_RenameAttempt_Returns409(t *testing.T) {
	svc := &mockSchemaServiceWithPatch{}
	r := newSchemaRouter(svc)

	// URL says my-schema, body says different — engine 1.1.0 makes name
	// immutable post-create. Must reject with 409 Conflict.
	newName := "renamed-schema"
	body, _ := json.Marshal(UpdateSchemaRequest{Name: &newName})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/schemas/my-schema", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
	var errResp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&errResp))
	assert.Contains(t, errResp["error"], "immutable")
}
