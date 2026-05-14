package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
)

// jsonRPCRequest represents a JSON-RPC 2.0 request.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonRPCResponse represents a JSON-RPC 2.0 response.
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// toolCallParams holds the params for tools/call.
type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// MCPHandler handles MCP JSON-RPC requests over HTTP.
type MCPHandler struct {
	logFile string
}

// NewMCPHandler creates a new MCPHandler.
func NewMCPHandler(logFile string) *MCPHandler {
	return &MCPHandler{logFile: logFile}
}

// Handle processes POST /mcp requests.
func (h *MCPHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Log the incoming request (headers + body).
	if logErr := logRequest(h.logFile, r.Header, body); logErr != nil {
		log.Printf("failed to log request: %v", logErr)
	}

	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      nil,
			Error:   &jsonRPCError{Code: -32700, Message: "parse error"},
		})
		return
	}

	resp := h.dispatch(req, r.Header)
	writeJSON(w, resp)
}

// dispatch routes a JSON-RPC request to the appropriate handler.
func (h *MCPHandler) dispatch(req jsonRPCRequest, headers http.Header) jsonRPCResponse {
	switch req.Method {
	case "initialize":
		return h.handleInitialize(req)
	case "notifications/initialized":
		// Notification — return empty 200 (no JSON-RPC response needed),
		// but we still return a valid response for simplicity.
		return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]string{}}
	case "tools/list":
		return h.handleToolsList(req)
	case "tools/call":
		return h.handleToolsCall(req, headers)
	default:
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)},
		}
	}
}

func (h *MCPHandler) handleInitialize(req jsonRPCRequest) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo": map[string]string{
				"name":    "mock-mcp-http",
				"version": "1.0",
			},
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
		},
	}
}

func (h *MCPHandler) handleToolsList(req jsonRPCRequest) jsonRPCResponse {
	tools := []map[string]any{
		{
			"name":        "echo_headers",
			"description": "Returns all request headers (X-* and Authorization)",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			"name":        "echo_message",
			"description": "Returns input message unchanged",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message": map[string]string{
						"type":        "string",
						"description": "Message to echo back",
					},
				},
				"required": []string{"message"},
			},
		},
		// chirp 2026-05-14 RED fixtures — used by integration repro for Issues 2 and 3.
		// device.list — dotted name; valid MCP convention, rejected by OpenAI's
		// ^[a-zA-Z0-9_-]+$ regex when routed to openai/openai-compatible providers.
		{
			"name":        "device.list",
			"description": "RED fixture: dotted MCP tool name (OpenAI rejects it).",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		// device_list_bare — underscore name (valid for OpenAI) with bare
		// {"type":"object"} schema that omits "properties". OpenAI rejects this
		// shape; mcp-go emits it via spec but eino-ext/libs/acl/openai re-serializes
		// the JSONSchema and drops the empty properties map.
		{
			"name":        "device_list_bare",
			"description": "RED fixture: object schema without properties field.",
			"inputSchema": map[string]any{
				"type": "object",
			},
		},
	}

	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  map[string]any{"tools": tools},
	}
}

func (h *MCPHandler) handleToolsCall(req jsonRPCRequest, headers http.Header) jsonRPCResponse {
	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: -32602, Message: "invalid params"},
		}
	}

	switch params.Name {
	case "echo_headers":
		return h.callEchoHeaders(req.ID, headers)
	case "echo_message":
		return h.callEchoMessage(req.ID, params.Arguments)
	case "device.list", "device_list_bare":
		return h.callDeviceListStub(req.ID, params.Name)
	default:
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: -32602, Message: fmt.Sprintf("unknown tool: %s", params.Name)},
		}
	}
}

// callEchoHeaders returns all X-* and Authorization headers from the request.
func (h *MCPHandler) callEchoHeaders(id json.RawMessage, headers http.Header) jsonRPCResponse {
	filtered := make(map[string]string)

	// Collect header names and sort for deterministic output.
	var names []string
	for name := range headers {
		canonical := http.CanonicalHeaderKey(name)
		if strings.HasPrefix(canonical, "X-") || canonical == "Authorization" {
			names = append(names, canonical)
		}
	}
	sort.Strings(names)

	for _, name := range names {
		filtered[name] = headers.Get(name)
	}

	text, _ := json.Marshal(filtered)

	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: map[string]any{
			"content": []map[string]string{
				{"type": "text", "text": string(text)},
			},
		},
	}
}

// callDeviceListStub returns a fixed device list response. Tool body is irrelevant
// for the RED scenarios — these fixtures exist to exercise tool schema/name
// validation paths, not the tool execution result.
func (h *MCPHandler) callDeviceListStub(id json.RawMessage, name string) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: map[string]any{
			"content": []map[string]string{
				{"type": "text", "text": fmt.Sprintf("[%s] []", name)},
			},
		},
	}
}

// callEchoMessage returns the message argument as-is.
func (h *MCPHandler) callEchoMessage(id json.RawMessage, arguments json.RawMessage) jsonRPCResponse {
	var args struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(arguments, &args); err != nil {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &jsonRPCError{Code: -32602, Message: "invalid arguments: message required"},
		}
	}

	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: map[string]any{
			"content": []map[string]string{
				{"type": "text", "text": args.Message},
			},
		},
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("failed to write response: %v", err)
	}
}
