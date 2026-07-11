package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/service/mcp"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// stubMCPService is an MCPService that records calls; Create/Update return
// a zero response unless the test configures otherwise. Useful for verifying
// that a handler short-circuits before reaching the service layer.
type stubMCPService struct {
	createCalls       []CreateMCPServerRequest
	updateCalls       []CreateMCPServerRequest
	patchCalls        []string
	deleteCalls       []string
	refreshCalls      []string
	refreshToolsCount int
	refreshErr        error
}

func (s *stubMCPService) ListMCPServers(ctx context.Context) ([]MCPServerResponse, error) {
	return nil, nil
}

func (s *stubMCPService) CreateMCPServer(ctx context.Context, req CreateMCPServerRequest) (*MCPServerResponse, error) {
	s.createCalls = append(s.createCalls, req)
	return &MCPServerResponse{ID: "id-1", Name: req.Name, Type: req.Type}, nil
}

func (s *stubMCPService) UpdateMCPServer(ctx context.Context, name string, req CreateMCPServerRequest) (*MCPServerResponse, error) {
	s.updateCalls = append(s.updateCalls, req)
	return &MCPServerResponse{ID: "id-1", Name: name, Type: req.Type}, nil
}

func (s *stubMCPService) DeleteMCPServer(ctx context.Context, name string) error {
	s.deleteCalls = append(s.deleteCalls, name)
	return nil
}

func (s *stubMCPService) PatchMCPServer(ctx context.Context, name string, req UpdateMCPServerRequest) (*MCPServerResponse, error) {
	s.patchCalls = append(s.patchCalls, name)
	return &MCPServerResponse{Name: name}, nil
}

func (s *stubMCPService) RefreshMCPServer(ctx context.Context, name string) (int, error) {
	s.refreshCalls = append(s.refreshCalls, name)
	if s.refreshErr != nil {
		return 0, s.refreshErr
	}
	return s.refreshToolsCount, nil
}

// policyFromEnv returns the transport policy that matches the current
// SYNTHETICBREW_MODE env var — mirrors the production wiring in server.go.
func policyFromEnv() mcp.TransportPolicy {
	if os.Getenv("SYNTHETICBREW_MODE") == "cloud" {
		return mcp.RestrictedTransportPolicy{}
	}
	return mcp.PermissiveTransportPolicy{}
}

// newMCPTestRouter returns a chi router that mirrors the production wiring
// in routes_register.go (full /api/v1/mcp-servers paths) so tests exercise
// the same routes the server registers at boot.
func newMCPTestRouter(svc *stubMCPService) http.Handler {
	return newMCPTestRouterFull(NewMCPHandler(svc, policyFromEnv()))
}

// TestMCPHandler_Create_CE_AllowsStdio verifies that stdio MCP servers are
// accepted in the default CE deployment mode (no SYNTHETICBREW_MODE env var).
func TestMCPHandler_Create_CE_AllowsStdio(t *testing.T) {
	t.Setenv("SYNTHETICBREW_MODE", "ce")

	svc := &stubMCPService{}
	router := newMCPTestRouter(svc)

	body, _ := json.Marshal(CreateMCPServerRequest{
		Name:    "test-stdio",
		Type:    "stdio",
		Command: "/bin/echo",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	require.Len(t, svc.createCalls, 1, "service should be called in CE mode")
	assert.Equal(t, "stdio", svc.createCalls[0].Type)
}

// TestMCPHandler_Create_Cloud_BlocksStdio verifies that stdio transport is
// rejected with 400 in multi-tenant deployment mode (the security gate that
// prevents arbitrary RCE on the hosted engine host).
func TestMCPHandler_Create_Cloud_BlocksStdio(t *testing.T) {
	t.Setenv("SYNTHETICBREW_MODE", "cloud")

	svc := &stubMCPService{}
	router := newMCPTestRouter(svc)

	body, _ := json.Marshal(CreateMCPServerRequest{
		Name:    "test-stdio-cloud",
		Type:    "stdio",
		Command: "/bin/sh",
		Args:    []string{"-c", "whoami"},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "is not permitted in this deployment")
	assert.Len(t, svc.createCalls, 0, "service must not be called when transport is blocked")
}

// TestMCPHandler_Create_RejectsDocker verifies docker transport is rejected
// regardless of deployment mode. DBML mcp_servers.type does not include
// "docker" — the CHECK constraint would fail at INSERT time if we let it
// through, so the handler must reject it up front.
func TestMCPHandler_Create_RejectsDocker(t *testing.T) {
	t.Setenv("SYNTHETICBREW_MODE", "ce")

	svc := &stubMCPService{}
	router := newMCPTestRouter(svc)

	body, _ := json.Marshal(CreateMCPServerRequest{
		Name:    "test-docker",
		Type:    "docker",
		Command: "image:tag",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid transport type")
	assert.Len(t, svc.createCalls, 0)
}

// TestMCPHandler_Create_Cloud_AllowsHTTP verifies HTTP/SSE transports still
// work in multi-tenant mode (they stay network-bound and don't spawn processes).
func TestMCPHandler_Create_Cloud_AllowsHTTP(t *testing.T) {
	t.Setenv("SYNTHETICBREW_MODE", "cloud")

	svc := &stubMCPService{}
	router := newMCPTestRouter(svc)

	body, _ := json.Marshal(CreateMCPServerRequest{
		Name: "test-http-cloud",
		Type: "http",
		URL:  "https://example.com/mcp",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	require.Len(t, svc.createCalls, 1)
	assert.Equal(t, "http", svc.createCalls[0].Type)
}

// TestHandlerCRUD_NotifiesService_OnSuccess verifies that the handler
// reaches the MCPService for every successful CRUD verb (Create / Update /
// Patch / Delete). The actual auto-reconnect lives in the service-layer
// adapter (mcpServiceHTTPAdapter), so this only enforces the
// handler→service contract — service-level wiring is exercised in the
// app-layer integration test.
func TestHandlerCRUD_NotifiesService_OnSuccess(t *testing.T) {
	t.Setenv("SYNTHETICBREW_MODE", "ce")

	svc := &stubMCPService{}
	router := newMCPTestRouter(svc)

	// Create.
	createBody, _ := json.Marshal(CreateMCPServerRequest{Name: "chirp-tools", Type: "http", URL: "http://upstream/v1"})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(createBody)))
	require.Equal(t, http.StatusCreated, rec.Code)

	// Update (PUT — full replace).
	updateBody, _ := json.Marshal(CreateMCPServerRequest{Name: "chirp-tools", Type: "http", URL: "http://upstream/v2"})
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/mcp-servers/chirp-tools", bytes.NewReader(updateBody)))
	require.Equal(t, http.StatusOK, rec.Code)

	// Patch (partial).
	patchURL := "http://upstream/v3"
	patchBody, _ := json.Marshal(UpdateMCPServerRequest{URL: &patchURL})
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/v1/mcp-servers/chirp-tools", bytes.NewReader(patchBody)))
	require.Equal(t, http.StatusOK, rec.Code)

	// Delete.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/mcp-servers/chirp-tools", nil))
	require.Equal(t, http.StatusNoContent, rec.Code)

	require.Len(t, svc.createCalls, 1, "service should receive Create on POST")
	require.Len(t, svc.updateCalls, 1, "service should receive Update on PUT")
	require.Len(t, svc.patchCalls, 1, "service should receive Patch on PATCH")
	require.Len(t, svc.deleteCalls, 1, "service should receive Delete on DELETE")
	assert.Equal(t, "chirp-tools", svc.patchCalls[0])
	assert.Equal(t, "chirp-tools", svc.deleteCalls[0])
}

// TestHandler_Refresh_RoutesToService verifies POST /{name}/refresh reaches
// the service with the correct name and returns the tools_count payload.
// 200 path: stub returns a fixed count; handler echoes it. 404 path: stub
// returns a NotFound DomainError, handler maps to 404.
func TestHandler_Refresh_RoutesToService(t *testing.T) {
	t.Setenv("SYNTHETICBREW_MODE", "ce")

	t.Run("200 with tools_count", func(t *testing.T) {
		svc := &stubMCPService{refreshToolsCount: 7}
		router := newMCPTestRouter(svc)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers/chirp-tools/refresh", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		require.Len(t, svc.refreshCalls, 1)
		assert.Equal(t, "chirp-tools", svc.refreshCalls[0])

		var body struct {
			Name       string `json:"name"`
			ToolsCount int    `json:"tools_count"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, "chirp-tools", body.Name)
		assert.Equal(t, 7, body.ToolsCount)
	})

	t.Run("404 when not registered", func(t *testing.T) {
		svc := &stubMCPService{refreshErr: pkgerrors.NotFound("mcp server not registered: chirp-tools")}
		router := newMCPTestRouter(svc)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers/chirp-tools/refresh", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code)
		require.Len(t, svc.refreshCalls, 1)
	})
}

// TestMCPHandler_Update_Cloud_BlocksStdio verifies the update path has the
// same guard as create.
func TestMCPHandler_Update_Cloud_BlocksStdio(t *testing.T) {
	t.Setenv("SYNTHETICBREW_MODE", "cloud")

	svc := &stubMCPService{}
	router := newMCPTestRouter(svc)

	body, _ := json.Marshal(CreateMCPServerRequest{
		Name:    "existing",
		Type:    "stdio",
		Command: "/bin/sh",
	})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/mcp-servers/existing", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "is not permitted in this deployment")
	assert.Len(t, svc.updateCalls, 0)
}
