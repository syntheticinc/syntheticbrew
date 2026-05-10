package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/syntheticinc/bytebrew/engine/internal/domain"
	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/llm"
)

// propagateBYOK translates the http-layer BYOK context keys (set by
// BYOKMiddleware) into a single llm.BYOKCredentials value attached via
// llm.WithBYOKCredentials. The downstream turn executor factory reads
// from there to build an ad-hoc per-end-user ChatModel (V2 §5.8).
func propagateBYOK(ctx context.Context) context.Context {
	provider, _ := ctx.Value(ContextKeyBYOKProvider).(string)
	apiKey, _ := ctx.Value(ContextKeyBYOKAPIKey).(string)
	if provider == "" || apiKey == "" {
		return ctx
	}
	model, _ := ctx.Value(ContextKeyBYOKModel).(string)
	baseURL, _ := ctx.Value(ContextKeyBYOKBaseURL).(string)
	return llm.WithBYOKCredentials(ctx, &llm.BYOKCredentials{
		Provider: provider,
		APIKey:   apiKey,
		Model:    model,
		BaseURL:  baseURL,
	})
}

// ChatService handles schema chat sessions via SSE.
//
// V2: chat is addressed by schema id, not agent name. The schema's
// entry_agent_id resolves the orchestrator; chat_enabled gates access.
type ChatService interface {
	Chat(ctx context.Context, schemaID, message, userSub, sessionID string) (<-chan SSEEvent, error)
}

// ChatHandler serves POST /api/v1/schemas/{name}/chat with SSE streaming.
//
// Engine 1.1.0 made the URL `{name}` segment a stable operator-facing handle
// (was UUID in 1.0.x). The handler resolves the name to a tenant-scoped UUID
// via SchemaNameResolver before invoking the chat dispatcher.
type ChatHandler struct {
	service          ChatService
	schemas          SchemaNameResolver
	forwardHeadersFn func() []string // dynamic — returns current forward headers
}

// NewChatHandler creates a new ChatHandler.
//
// schemas is the tenant-scoped name → UUID resolver — required to translate
// the URL `{name}` segment into the canonical schema UUID consumed by the
// chat dispatcher. forwardHeadersFn returns the current union of all
// forward_headers across MCP server configs (called per request so that
// config reloads take effect immediately).
func NewChatHandler(service ChatService, schemas SchemaNameResolver, forwardHeadersFn func() []string) *ChatHandler {
	return &ChatHandler{service: service, schemas: schemas, forwardHeadersFn: forwardHeadersFn}
}

type chatRequest struct {
	Message   string            `json:"message"`
	UserSub   string            `json:"user_sub"`            // fallback when no JWT present (tests/CE-local)
	SessionID string            `json:"session_id"`
	Stream    *bool             `json:"stream,omitempty"`    // default true
	Headers   map[string]string `json:"headers,omitempty"`   // optional headers forwarded to MCP tool calls
}

type nonStreamResponse struct {
	SessionID string          `json:"session_id,omitempty"`
	SchemaID  string          `json:"schema_id"`
	Message   string          `json:"message"`
	Error     string          `json:"error,omitempty"`
	ToolCalls []toolCallEntry `json:"tool_calls,omitempty"`
}

type toolCallEntry struct {
	Tool   string `json:"tool"`
	Input  string `json:"input,omitempty"`
	Output string `json:"output,omitempty"`
}

// Chat handles SSE streaming or non-streaming chat.
//
// URL `{name}` is the operator-declared schema name (engine 1.1.0+).
// Resolved to UUID via tenant-scoped lookup; non-existent or invalid names
// return 404 (does not leak existence across tenants).
func (h *ChatHandler) Chat(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	schemaID, err := resolveSchemaNameToUUID(r.Context(), h.schemas, name)
	if err != nil {
		writeNameLookupError(r.Context(), w, "schema", name, err)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.Message == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message required"})
		return
	}

	userSub := resolveUserSub(r, req.UserSub)
	if userSub == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}

	ctx := h.buildRequestContext(r)
	// Lift BYOK context keys into the canonical llm.BYOKCredentials value
	// before handing off to the chat service. Downstream layers read them
	// from there to build an ad-hoc per-end-user ChatModel (V2 §5.8).
	ctx = propagateBYOK(ctx)
	if len(req.Headers) > 0 {
		existing := domain.GetRequestContext(ctx)
		merged := make(map[string]string, len(req.Headers))
		if existing != nil {
			for k, v := range existing.Headers {
				merged[k] = v
			}
		}
		for k, v := range req.Headers {
			merged[k] = v
		}
		ctx = domain.WithRequestContext(ctx, &domain.RequestContext{Headers: merged})
	}

	events, err := h.service.Chat(ctx, schemaID, req.Message, userSub, req.SessionID)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	// Non-streaming: collect all events → return JSON
	if req.Stream != nil && !*req.Stream {
		h.handleNonStreaming(w, schemaID, events)
		return
	}

	// Streaming: SSE.
	//
	// Go's net/http buffers small responses and sets Content-Length, which breaks
	// SSE. Headers + initial flush + chunked encoding commit immediately so
	// downstream clients start reading events without waiting for the whole body.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	_ = rc.SetReadDeadline(time.Time{})

	flush := findFlusher(w)

	w.Header().Del("Content-Length")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.WriteHeader(http.StatusOK)
	flush()

	_, _ = io.WriteString(w, ": ok\n\n")
	flush()

	for event := range events {
		_, _ = io.WriteString(w, "event: "+event.Type+"\ndata: "+event.Data+"\n\n")
		flush()
	}
}

// resolveUserSub returns the authenticated end-user identifier.
//
// Preference order:
//   1. UserSub from auth context (JWT `sub` claim, or api_token name —
//      auth middleware populates this for both authenticated actor types).
//   2. Anonymous fallback to request body field — ONLY for unauthenticated
//      requests (CE local / public widget).
//
// Authenticated actors (api_token, admin JWT) MUST NOT fall back to the
// body field: that path was the chirp 1.1.3 impersonation hole — an
// api_token holder with ScopeChat could create sessions under any
// user_sub by setting it in the request body. Auth middleware now
// stamps the canonical identity into ctx for both branches; if it's
// missing for an authenticated actor we treat the request as unauth
// (caller returns 401) rather than honouring the body.
//
// An empty result signals unauthenticated — caller must 401.
func resolveUserSub(r *http.Request, fallback string) string {
	if sub := domain.UserSubFromContext(r.Context()); sub != "" {
		return sub
	}
	actorType, _ := r.Context().Value(ContextKeyActorType).(string)
	if actorType == "api_token" || actorType == "admin" {
		// Authenticated but no UserSub stamped — never fall back to
		// client-controlled body. Defensive: post-Phase-0 this should
		// be unreachable because both branches stamp ctx; if it ever
		// fires it's a regression in auth_middleware to surface, not a
		// silent impersonation.
		return ""
	}
	return fallback
}

// handleNonStreaming collects SSE events and returns a single JSON response.
func (h *ChatHandler) handleNonStreaming(w http.ResponseWriter, schemaID string, events <-chan SSEEvent) {
	var (
		message   string
		errMsg    string
		toolCalls []toolCallEntry
		sessionID string
		lastTool  string
	)

	for event := range events {
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(event.Data), &data); err != nil {
			continue
		}

		switch event.Type {
		case "message", "message_delta":
			if content, ok := data["content"].(string); ok {
				if event.Type == "message" {
					if content != "" {
						message = content
					}
				} else {
					message += content
				}
			}
		case "tool_call":
			toolName, _ := data["tool"].(string)
			input, _ := data["content"].(string)
			lastTool = toolName
			toolCalls = append(toolCalls, toolCallEntry{Tool: toolName, Input: input})
		case "tool_result":
			output, _ := data["content"].(string)
			for i := len(toolCalls) - 1; i >= 0; i-- {
				if toolCalls[i].Tool == lastTool && toolCalls[i].Output == "" {
					toolCalls[i].Output = output
					break
				}
			}
		case "error":
			if content, ok := data["content"].(string); ok && content != "" {
				errMsg = content
			} else if msg, ok := data["message"].(string); ok && msg != "" {
				errMsg = msg
			}
		case "done":
			if sid, ok := data["session_id"].(string); ok {
				sessionID = sid
			}
		}
	}

	if message == "" && errMsg != "" {
		message = errMsg
	}

	resp := nonStreamResponse{
		SessionID: sessionID,
		SchemaID:  schemaID,
		Message:   message,
		Error:     errMsg,
		ToolCalls: toolCalls,
	}
	writeJSON(w, http.StatusOK, resp)
}

// buildRequestContext extracts configured forward headers from the HTTP request
// and stores them in a domain.RequestContext within the request's Go context.
func (h *ChatHandler) buildRequestContext(r *http.Request) context.Context {
	ctx := r.Context()
	forwardHeaders := h.forwardHeadersFn()
	if len(forwardHeaders) == 0 {
		return ctx
	}

	headers := make(map[string]string)
	for _, name := range forwardHeaders {
		if val := r.Header.Get(name); val != "" {
			headers[name] = val
		}
	}
	if len(headers) == 0 {
		return ctx
	}

	rc := &domain.RequestContext{Headers: headers}
	return domain.WithRequestContext(ctx, rc)
}

// findFlusher unwraps a ResponseWriter to find http.Flusher.
// Chi middleware wraps the ResponseWriter, hiding the Flusher interface.
// Returns a no-op function if Flusher is not available.
func findFlusher(w http.ResponseWriter) func() {
	type unwrapper interface{ Unwrap() http.ResponseWriter }
	for {
		if f, ok := w.(http.Flusher); ok {
			return f.Flush
		}
		if u, ok := w.(unwrapper); ok {
			w = u.Unwrap()
			continue
		}
		slog.WarnContext(context.Background(), "[SSE] Flush not available — middleware may be wrapping ResponseWriter without Unwrap()")
		return func() {}
	}
}
