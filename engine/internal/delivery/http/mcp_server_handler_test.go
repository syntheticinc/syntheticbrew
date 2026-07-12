package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/mcp"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools"
)

// recordingTool is a stub tool that records the ToolDependencies it was built
// with and returns a fixed payload. It runs immediately — it never blocks on
// confirmation — which is exactly what the MCP server endpoint must guarantee.
type recordingTool struct {
	name       string
	gotDeps    *tools.ToolDependencies
	ranWith    *string
	returnText string
}

func (t *recordingTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.name,
		Desc: "stub " + t.name,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"x": {Type: schema.String, Desc: "x", Required: false},
		}),
	}, nil
}

func (t *recordingTool) InvokableRun(_ context.Context, args string, _ ...tool.Option) (string, error) {
	captured := args
	t.ranWith = &captured
	return t.returnText, nil
}

// newTestStore builds a builtin store with a stub factory per name. The factory
// closure captures a per-name recordingTool and stamps the ToolDependencies it
// was invoked with, so a test can assert the catalog never injects a
// ConfirmRequester on the raw store path.
func newTestStore(names ...string) (*tools.BuiltinToolStore, map[string]*recordingTool) {
	store := tools.NewBuiltinToolStore()
	recs := make(map[string]*recordingTool, len(names))
	for _, n := range names {
		rec := &recordingTool{name: n, returnText: "ok:" + n}
		recs[n] = rec
		name := n
		store.Register(name, func(deps tools.ToolDependencies) tool.InvokableTool {
			d := deps
			recs[name].gotDeps = &d
			return recs[name]
		})
	}
	return store, recs
}

// ctxWithScopes returns a context carrying the given scope mask, mimicking what
// the auth middleware installs before the handler runs.
func ctxWithScopes(mask int) context.Context {
	return context.WithValue(context.Background(), ContextKeyScopes, mask)
}

func doRPC(t *testing.T, h *MCPServerHandler, ctx context.Context, body string) (*httptest.ResponseRecorder, mcp.Response) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/rpc", strings.NewReader(body)).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var resp mcp.Response
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	}
	return rec, resp
}

func TestMCPServer_MalformedJSON_ParseError(t *testing.T) {
	store, _ := newTestStore("admin_list_agents")
	h := NewMCPServerHandler(store, nil, "test")

	rec, resp := doRPC(t, h, ctxWithScopes(ScopeAdmin), `{"jsonrpc":`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	require.NotNil(t, resp.Error)
	assert.Equal(t, jsonRPCParseError, resp.Error.Code)
}

func TestMCPServer_BadJSONRPCVersion_InvalidRequest(t *testing.T) {
	store, _ := newTestStore("admin_list_agents")
	h := NewMCPServerHandler(store, nil, "test")

	rec, resp := doRPC(t, h, ctxWithScopes(ScopeAdmin), `{"jsonrpc":"1.0","id":1,"method":"initialize"}`)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, resp.Error)
	assert.Equal(t, jsonRPCInvalidRequest, resp.Error.Code)
}

func TestMCPServer_Initialize_EchoesProtocol(t *testing.T) {
	store, _ := newTestStore()
	h := NewMCPServerHandler(store, nil, "9.9.9")

	rec, resp := doRPC(t, h, ctxWithScopes(ScopeAdmin),
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.Nil(t, resp.Error)
	var result initializeResult
	require.NoError(t, json.Unmarshal(resp.Result, &result))
	assert.Equal(t, "2024-11-05", result.ProtocolVersion)
	assert.Equal(t, "9.9.9", result.ServerInfo.Version)
}

func TestMCPServer_UnknownMethod_MethodNotFound(t *testing.T) {
	store, _ := newTestStore()
	h := NewMCPServerHandler(store, nil, "test")

	rec, resp := doRPC(t, h, ctxWithScopes(ScopeAdmin),
		`{"jsonrpc":"2.0","id":1,"method":"does/not/exist"}`)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, resp.Error)
	assert.Equal(t, jsonRPCMethodNotFound, resp.Error.Code)
}

// TestMCPServer_ToolsList_NoRuntimeLeak asserts the tools/list surface is the
// fixed allowlist only. A runtime tool (show_structured_output) registered in
// the underlying store must never appear.
func TestMCPServer_ToolsList_NoRuntimeLeak(t *testing.T) {
	store, _ := newTestStore("admin_list_agents", "provision_agent", "show_structured_output", "memory_recall")
	h := NewMCPServerHandler(store, nil, "test")

	_, resp := doRPC(t, h, ctxWithScopes(ScopeAdmin),
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)

	require.Nil(t, resp.Error)
	var result mcp.ToolsListResult
	require.NoError(t, json.Unmarshal(resp.Result, &result))

	names := make(map[string]bool)
	for _, tl := range result.Tools {
		names[tl.Name] = true
	}
	assert.True(t, names["admin_list_agents"], "allowlisted admin tool present")
	assert.True(t, names["provision_agent"], "allowlisted provisioning tool present")
	assert.False(t, names["show_structured_output"], "runtime tool must NOT leak")
	assert.False(t, names["memory_recall"], "runtime tool must NOT leak")
}

// TestMCPServer_NoHang_RawStorePath is the no-hang guard: an external tools/call
// must resolve the tool through the raw store with a ZERO-VALUE ToolDependencies
// (nil ConfirmRequester) and run it directly. If the catalog ever injected a
// ConfirmRequester, external callers could block forever on SSE confirmation.
func TestMCPServer_NoHang_RawStorePath(t *testing.T) {
	store, recs := newTestStore("admin_create_agent")
	h := NewMCPServerHandler(store, nil, "test")

	done := make(chan struct{})
	var rec *httptest.ResponseRecorder
	var resp mcp.Response
	go func() {
		rec, resp = doRPC(t, h, ctxWithScopes(ScopeProvisionMask),
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"admin_create_agent","arguments":{"x":"y"}}}`)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("tools/call hung — external call blocked (confirmation path leaked into raw store)")
	}

	assert.Equal(t, http.StatusOK, rec.Code)
	require.Nil(t, resp.Error)

	// The tool actually ran, and it was built with a zero-value deps struct —
	// specifically NO ConfirmRequester.
	got := recs["admin_create_agent"]
	require.NotNil(t, got.ranWith, "tool must have executed")
	require.NotNil(t, got.gotDeps, "factory must have been invoked")
	assert.Nil(t, got.gotDeps.ConfirmRequester, "raw store path must never inject a ConfirmRequester")

	var result mcp.ToolCallResult
	require.NoError(t, json.Unmarshal(resp.Result, &result))
	assert.False(t, result.IsError)
	require.Len(t, result.Content, 1)
	assert.Equal(t, "ok:admin_create_agent", result.Content[0].Text)
}

// TestMCPServer_ScopeGating_DeleteRequiresManage verifies the scope model: a
// provision-only token may call non-delete tools but is rejected on delete
// tools, which require ScopeManage.
func TestMCPServer_ScopeGating_DeleteRequiresManage(t *testing.T) {
	store, _ := newTestStore("admin_create_agent", "admin_delete_agent")
	h := NewMCPServerHandler(store, nil, "test")

	// Provision-only token: create allowed.
	_, createResp := doRPC(t, h, ctxWithScopes(ScopeProvisionMask),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"admin_create_agent","arguments":{}}}`)
	require.Nil(t, createResp.Error, "provision token may create")

	// Provision-only token: delete rejected with insufficient-scope.
	_, delResp := doRPC(t, h, ctxWithScopes(ScopeProvisionMask),
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"admin_delete_agent","arguments":{}}}`)
	require.NotNil(t, delResp.Error, "provision token must NOT delete")
	assert.Equal(t, jsonRPCInsufficientScope, delResp.Error.Code)

	// Manage token: delete allowed.
	_, delOK := doRPC(t, h, ctxWithScopes(ScopeManageMask),
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"admin_delete_agent","arguments":{}}}`)
	require.Nil(t, delOK.Error, "manage token may delete")

	// Admin superscope: delete allowed.
	_, delAdmin := doRPC(t, h, ctxWithScopes(ScopeAdmin),
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"admin_delete_agent","arguments":{}}}`)
	require.Nil(t, delAdmin.Error, "admin superscope may delete")
}

// TestMCPServer_UnknownTool_ToolError verifies calling a non-allowlisted tool
// returns a tool-level error (isError), not a protocol error, and never leaks a
// runtime tool even if it exists in the store.
func TestMCPServer_UnknownTool_ToolError(t *testing.T) {
	store, _ := newTestStore("show_structured_output")
	h := NewMCPServerHandler(store, nil, "test")

	_, resp := doRPC(t, h, ctxWithScopes(ScopeAdmin),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"show_structured_output","arguments":{}}}`)

	// Non-allowlisted → tool-level error result, not a JSON-RPC protocol error.
	require.Nil(t, resp.Error)
	var result mcp.ToolCallResult
	require.NoError(t, json.Unmarshal(resp.Result, &result))
	assert.True(t, result.IsError)
	require.Len(t, result.Content, 1)
	assert.Contains(t, result.Content[0].Text, "unknown tool")
}

// TestMCPServer_MarkedResult_SetsIsError is the BUG B guard: an admin tool that
// signals an application failure via the [ERROR] marker (returned as a normal
// (string, nil) result, per the engine tool convention) must surface as MCP
// isError:true so a programmatic client can tell success from failure. A
// genuine success and the quota sentinel stay isError:false.
func TestMCPServer_MarkedResult_SetsIsError(t *testing.T) {
	tests := []struct {
		name        string
		returnText  string
		wantIsError bool
	}{
		{"validation error is marked", "[ERROR] name is required", true},
		{"not found is marked", "[ERROR] Model not found: bogus-id", true},
		{"db failure is marked", "[ERROR] Failed to create model: one or more fields have an invalid value", true},
		{"happy path is not an error", "Model \"gpt\" created (id=abc).", false},
		{"empty list is not an error", "No models configured.", false},
		{"quota sentinel is not an error", "[quota:schema_limit_reached] Your plan's schema limit is reached.", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, recs := newTestStore("admin_create_model")
			recs["admin_create_model"].returnText = tt.returnText
			h := NewMCPServerHandler(store, nil, "test")

			_, resp := doRPC(t, h, ctxWithScopes(ScopeProvisionMask),
				`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"admin_create_model","arguments":{}}}`)

			require.Nil(t, resp.Error)
			var result mcp.ToolCallResult
			require.NoError(t, json.Unmarshal(resp.Result, &result))
			assert.Equal(t, tt.wantIsError, result.IsError, "isError for %q", tt.returnText)
			// The human-readable message is always preserved in the content so the
			// model still reads it — isError is additive, not a replacement.
			require.Len(t, result.Content, 1)
			assert.Equal(t, tt.returnText, result.Content[0].Text)
		})
	}
}

// TestMCPServer_MissingToolName_InvalidParams verifies an empty tool name is a
// protocol-level invalid-params error.
func TestMCPServer_MissingToolName_InvalidParams(t *testing.T) {
	store, _ := newTestStore()
	h := NewMCPServerHandler(store, nil, "test")

	_, resp := doRPC(t, h, ctxWithScopes(ScopeAdmin),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":""}}`)

	require.NotNil(t, resp.Error)
	assert.Equal(t, jsonRPCInvalidParams, resp.Error.Code)
}
