package http

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/mcp"
)

// JSON-RPC 2.0 error codes (subset used by the MCP server endpoint).
const (
	jsonRPCParseError     = -32700
	jsonRPCInvalidRequest = -32600
	jsonRPCMethodNotFound = -32601
	jsonRPCInvalidParams  = -32602
	// jsonRPCInsufficientScope is an application-level error returned inside a
	// 200 JSON-RPC response body when the caller lacks the scope for a tool.
	jsonRPCInsufficientScope = -32002
)

// mcpProtocolVersion is the protocol version echoed to clients that do not
// send a recognized version in initialize.
const mcpProtocolVersion = "2025-03-26"

// maxMCPBodyBytes caps the JSON-RPC request body. Admin/provisioning payloads
// (system prompts, tool lists) are small; 1 MiB is generous headroom.
const maxMCPBodyBytes = 1 << 20

// mcpToolAuditor records a per-tool-call audit entry. Consumer-side interface:
// the app wiring adapts the existing audit logger. Nil disables DB audit
// (slog audit still always emits).
type mcpToolAuditor interface {
	RecordToolCall(ctx context.Context, toolName string, isError bool, durationMs int64)
}

// MCPServerHandler serves a single streamable-HTTP JSON-RPC 2.0 endpoint that
// exposes the admin_* and provisioning tools to external MCP clients. It routes
// tool calls through the raw builtin store (no ConfirmRequester) so external
// callers can never block on the SSE confirmation flow.
type MCPServerHandler struct {
	catalog *mcpToolCatalog
	auditor mcpToolAuditor
	version string
}

// NewMCPServerHandler builds the handler. store is the builtin tool store;
// auditor may be nil (slog-only audit). version is echoed in serverInfo.
func NewMCPServerHandler(store mcpToolStore, auditor mcpToolAuditor, version string) *MCPServerHandler {
	return &MCPServerHandler{
		catalog: newMCPToolCatalog(store),
		auditor: auditor,
		version: version,
	}
}

// initializeParams is the subset of the initialize params we read.
type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

// initializeResult is the initialize response body.
type initializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      serverInfo     `json:"serverInfo"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// toolsCallParams is the tools/call params.
type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ServeHTTP handles POST /api/v1/mcp/rpc. It decodes one JSON-RPC request,
// dispatches by method, and writes the JSON-RPC response. Malformed input is
// answered defensively — the handler never panics into a 500.
func (h *MCPServerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Cap the body: any authenticated token (including public widget chat keys)
	// can reach this route, and mcp.Request.Params fully materializes — an
	// uncapped body is a memory-exhaustion vector.
	r.Body = http.MaxBytesReader(w, r.Body, maxMCPBodyBytes)

	var req mcp.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Parse errors (and over-limit bodies) carry no id — respond per
		// JSON-RPC with a null-id error and HTTP 400 so clients see a
		// transport-level rejection.
		h.writeError(w, http.StatusBadRequest, nil, jsonRPCParseError, "parse error")
		return
	}

	if req.JSONRPC != "2.0" {
		h.writeError(w, http.StatusOK, req.ID, jsonRPCInvalidRequest, "jsonrpc must be \"2.0\"")
		return
	}

	switch req.Method {
	case "initialize":
		h.handleInitialize(w, &req)
	case "notifications/initialized", "initialized":
		// Notification: no id, no JSON-RPC response body.
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		h.handleToolsList(w, r.Context(), &req)
	case "tools/call":
		h.handleToolsCall(w, r.Context(), &req)
	default:
		h.writeError(w, http.StatusOK, req.ID, jsonRPCMethodNotFound, "method not found: "+req.Method)
	}
}

func (h *MCPServerHandler) handleInitialize(w http.ResponseWriter, req *mcp.Request) {
	proto := mcpProtocolVersion
	if raw := paramsRaw(req); raw != nil {
		var p initializeParams
		if err := json.Unmarshal(raw, &p); err == nil && p.ProtocolVersion != "" {
			// Echo the client's requested version — MCP negotiation lets the
			// server accept a version it recognizes; we accept any well-formed
			// value the client proposes.
			proto = p.ProtocolVersion
		}
	}
	result := initializeResult{
		ProtocolVersion: proto,
		Capabilities:    map[string]any{"tools": map[string]any{}},
		ServerInfo:      serverInfo{Name: "syntheticbrew-engine", Version: h.version},
	}
	h.writeResult(w, req.ID, result)
}

func (h *MCPServerHandler) handleToolsList(w http.ResponseWriter, ctx context.Context, req *mcp.Request) {
	// Gate catalog disclosure: widget chat keys are embedded in public HTML, so
	// a caller must hold at least one provision bit to enumerate the management
	// tool surface. tools/call stays independently per-tool scope-gated.
	if !hasScope(ctx, ScopeProvisionMask) {
		h.writeError(w, http.StatusOK, req.ID, jsonRPCInsufficientScope, "insufficient scope to list tools")
		return
	}

	names := h.catalog.names()
	sort.Strings(names)

	out := make([]mcp.MCPTool, 0, len(names))
	for _, name := range names {
		t := h.catalog.resolve(ctx, name)
		if t == nil {
			continue
		}
		info, err := t.Info(ctx)
		if err != nil || info == nil {
			slog.WarnContext(ctx, "mcp server: tool info failed, skipping", "tool", name, "error", err)
			continue
		}
		out = append(out, mcp.MCPTool{
			Name:        info.Name,
			Description: info.Desc,
			InputSchema: toolInputSchema(ctx, info),
		})
	}
	h.writeResult(w, req.ID, mcp.ToolsListResult{Tools: out})
}

func (h *MCPServerHandler) handleToolsCall(w http.ResponseWriter, ctx context.Context, req *mcp.Request) {
	raw := paramsRaw(req)
	if raw == nil {
		h.writeError(w, http.StatusOK, req.ID, jsonRPCInvalidParams, "missing params")
		return
	}
	var p toolsCallParams
	if err := json.Unmarshal(raw, &p); err != nil {
		h.writeError(w, http.StatusOK, req.ID, jsonRPCInvalidParams, "invalid params")
		return
	}
	if p.Name == "" {
		h.writeError(w, http.StatusOK, req.ID, jsonRPCInvalidParams, "tool name required")
		return
	}

	required, ok := h.catalog.requiredScope(p.Name)
	if !ok {
		// Unknown / non-allowlisted tool → tool-level error (isError), not a
		// protocol error: the method (tools/call) is valid.
		h.writeResult(w, req.ID, toolErrorResult("unknown tool: "+p.Name))
		return
	}

	if !hasScope(ctx, required) {
		h.writeError(w, http.StatusOK, req.ID, jsonRPCInsufficientScope, "insufficient scope for tool: "+p.Name)
		return
	}

	invoker := h.catalog.resolve(ctx, p.Name)
	if invoker == nil {
		h.writeResult(w, req.ID, toolErrorResult("tool not available: "+p.Name))
		return
	}

	argsJSON := string(p.Arguments)
	if argsJSON == "" || argsJSON == "null" {
		argsJSON = "{}"
	}

	start := time.Now()
	result, err := invoker.InvokableRun(ctx, argsJSON)
	durationMs := time.Since(start).Milliseconds()

	// A tool-returned error is a tool-level failure (isError:true), NOT a
	// protocol error — admin/provisioning tools return their errors as strings
	// with a nil error, so err is usually nil and the isError flag comes from
	// content inspection is unnecessary. When the tool does return a Go error
	// we still surface it as a tool error, never a 500.
	isError := err != nil
	text := result
	if err != nil {
		text = "tool execution failed: " + err.Error()
	}

	h.auditToolCall(ctx, p.Name, isError, durationMs)
	h.writeResult(w, req.ID, mcp.ToolCallResult{
		Content: []mcp.ToolContent{{Type: "text", Text: text}},
		IsError: isError,
	})
}

// auditToolCall emits a structured slog audit line for every tools/call and,
// when a DB auditor is wired, appends a persisted record.
func (h *MCPServerHandler) auditToolCall(ctx context.Context, toolName string, isError bool, durationMs int64) {
	slog.InfoContext(ctx, "mcp server tool call",
		"audit", true,
		"tool", toolName,
		"tenant_id", domain.TenantIDFromContext(ctx),
		"actor", domain.UserSubFromContext(ctx),
		"duration_ms", durationMs,
		"is_error", isError,
	)
	if h.auditor != nil {
		h.auditor.RecordToolCall(ctx, toolName, isError, durationMs)
	}
}

// --- helpers ---

// paramsRaw returns the raw JSON of req.Params. Params is decoded as
// interface{} by the shared wire type, so we re-marshal to json.RawMessage for
// typed unmarshalling. Returns nil when there are no params.
func paramsRaw(req *mcp.Request) json.RawMessage {
	if req.Params == nil {
		return nil
	}
	if raw, ok := req.Params.(json.RawMessage); ok {
		return raw
	}
	b, err := json.Marshal(req.Params)
	if err != nil {
		return nil
	}
	return b
}

// toolInputSchema renders the Eino tool ParamsOneOf into a JSON Schema object.
// On any failure it degrades to an empty object schema so tools/list never
// fails wholesale on one malformed tool.
func toolInputSchema(ctx context.Context, info *schema.ToolInfo) json.RawMessage {
	empty := json.RawMessage(`{"type":"object","properties":{}}`)
	if info.ParamsOneOf == nil {
		return empty
	}
	js, err := info.ToJSONSchema()
	if err != nil || js == nil {
		slog.WarnContext(ctx, "mcp server: params schema render failed", "tool", info.Name, "error", err)
		return empty
	}
	b, err := json.Marshal(js)
	if err != nil {
		return empty
	}
	return b
}

// hasScope reports whether the context scopes satisfy the required mask.
// ScopeAdmin is a superscope. A required mask with multiple bits requires all
// of them (the caller must hold every bit), matching REST RequireScope
// semantics for composite masks except that ScopeAdmin bypasses the check.
func hasScope(ctx context.Context, required int) bool {
	scopes, _ := ctx.Value(ContextKeyScopes).(int)
	if scopes&ScopeAdmin != 0 {
		return true
	}
	return scopes&required == required
}

func toolErrorResult(msg string) mcp.ToolCallResult {
	return mcp.ToolCallResult{
		Content: []mcp.ToolContent{{Type: "text", Text: msg}},
		IsError: true,
	}
}

func (h *MCPServerHandler) writeResult(w http.ResponseWriter, id interface{}, result interface{}) {
	raw, err := json.Marshal(result)
	if err != nil {
		h.writeError(w, http.StatusOK, id, jsonRPCInvalidRequest, "marshal result failed")
		return
	}
	writeJSON(w, http.StatusOK, mcp.Response{JSONRPC: "2.0", ID: id, Result: raw})
}

func (h *MCPServerHandler) writeError(w http.ResponseWriter, httpStatus int, id interface{}, code int, msg string) {
	writeJSON(w, httpStatus, mcp.Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &mcp.RPCError{Code: code, Message: msg},
	})
}
