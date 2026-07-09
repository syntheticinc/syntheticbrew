package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm"
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
// ResumeInterrupt handles the HITL `resume_interrupt` POST body branch.
type ChatService interface {
	Chat(ctx context.Context, schemaID, message, userSub, sessionID string) (<-chan SSEEvent, error)
	ResumeInterrupt(ctx context.Context, schemaID, userSub, sessionID, interruptID string, payload json.RawMessage) (<-chan SSEEvent, error)
}

// ChatHandler serves POST /api/v1/schemas/{name}/chat with SSE streaming.
//
// Engine 1.1.0 made the URL `{name}` segment a stable operator-facing handle
// (was UUID in 1.0.x). The handler resolves the name to a tenant-scoped UUID
// via SchemaNameResolver before invoking the chat dispatcher.
type ChatHandler struct {
	service          ChatService
	schemas          SchemaNameResolver
	forwardHeadersFn func(context.Context) []string // dynamic, ctx-scoped — returns current forward headers for the request's tenant
}

// NewChatHandler creates a new ChatHandler.
//
// schemas is the tenant-scoped name → UUID resolver — required to translate
// the URL `{name}` segment into the canonical schema UUID consumed by the
// chat dispatcher. forwardHeadersFn receives the request context (which
// carries tenant_id stamped by auth middleware) and returns that tenant's
// current union of forward_headers across MCP server configs. Called per
// request so config reloads / CRUD-driven refreshes take effect immediately.
func NewChatHandler(service ChatService, schemas SchemaNameResolver, forwardHeadersFn func(context.Context) []string) *ChatHandler {
	return &ChatHandler{service: service, schemas: schemas, forwardHeadersFn: forwardHeadersFn}
}

type chatRequest struct {
	Message         string                  `json:"message"`
	UserSub         string                  `json:"user_sub"` // fallback when no JWT present (tests/CE-local)
	SessionID       string                  `json:"session_id"`
	Stream          *bool                   `json:"stream,omitempty"`           // default true
	Headers         map[string]string       `json:"headers,omitempty"`          // optional headers forwarded to MCP tool calls
	ResumeInterrupt *resumeInterruptRequest `json:"resume_interrupt,omitempty"` // HITL resume — mutually exclusive with Message
}

// resumeInterruptRequest is the HITL `resume_interrupt` POST body branch.
type resumeInterruptRequest struct {
	InterruptID string          `json:"interrupt_id"`
	Payload     json.RawMessage `json:"payload"`
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

// maxChatBodyBytes caps the chat request body (a message + optional
// headers/BYOK). Every other write endpoint bounds its intake; chat was the
// one unbounded JSON decode, letting a single authenticated client exhaust
// memory with an oversized body.
const maxChatBodyBytes = 1 << 20 // 1 MB

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

	r.Body = http.MaxBytesReader(w, r.Body, maxChatBodyBytes)
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// Caller must send exactly one of `message` / `resume_interrupt`.
	hasMessage := req.Message != ""
	hasResume := req.ResumeInterrupt != nil && req.ResumeInterrupt.InterruptID != ""
	switch {
	case hasMessage && hasResume:
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "message and resume_interrupt are mutually exclusive",
		})
		return
	case !hasMessage && !hasResume:
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "message or resume_interrupt required",
		})
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

	var events <-chan SSEEvent
	var err2 error
	if hasResume {
		events, err2 = h.service.ResumeInterrupt(
			ctx, schemaID, userSub, req.SessionID,
			req.ResumeInterrupt.InterruptID, req.ResumeInterrupt.Payload,
		)
	} else {
		events, err2 = h.service.Chat(ctx, schemaID, req.Message, userSub, req.SessionID)
	}
	if err2 != nil {
		writeDomainError(w, err2)
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

// visitorSubRe bounds a body-supplied visitor id: short, URL/log-safe charset.
var visitorSubRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// resolveUserSub returns the effective end-user identifier for a chat request.
//
// Per actor type:
//
//	admin      → ctx sub only; the body field is NEVER trusted.
//	api_token  → ctx sub (token name) is the canonical identity; empty ctx
//	             sub → "" (caller 401s — regression guard, never the body).
//	             When the body field matches visitorSubRe and the combined
//	             id fits 255 bytes, the visitor is namespaced under the
//	             token: "<token-name>:<visitor>". Otherwise the token name
//	             alone (backward compatible).
//	(other)    → ctx sub when present, else the body field (CE local /
//	             public widget / tests).
//
// The api_token namespacing gives each website visitor a distinct identity
// while staying impersonation-safe: the token-name prefix means a client can
// never mint a sub equal to a JWT subject or another token's visitors, and
// rotating visitor ids only inflates the distinct-user count (conservative
// for any per-user or user-count limit). Per-visitor subs also make
// session/memory isolation stricter than the previous behavior where every
// visitor behind one token shared the token name.
//
// An empty result signals unauthenticated — caller must 401.
func resolveUserSub(r *http.Request, fallback string) string {
	ctxSub := domain.UserSubFromContext(r.Context())
	actorType, _ := r.Context().Value(ContextKeyActorType).(string)
	switch actorType {
	case "admin":
		return ctxSub
	case "api_token":
		if ctxSub == "" {
			// Authenticated but no UserSub stamped — never fall back to
			// the client-controlled body. Defensive: auth_middleware stamps
			// the token name for every api_token actor; if this fires it's
			// a middleware regression to surface, not a silent
			// impersonation (that was the 1.1.3 hole).
			return ""
		}
		// Namespace the per-visitor id under the token, but only when the token
		// name has no ':' — token creation forbids ':' in names, so a ':' here
		// means an unexpected/legacy name; refuse to namespace rather than risk
		// a "<name>:<visitor>" that collides with another principal's sub.
		if visitorSubRe.MatchString(fallback) &&
			!strings.Contains(ctxSub, ":") &&
			len(ctxSub)+1+len(fallback) <= 255 {
			return ctxSub + ":" + fallback
		}
		return ctxSub
	default:
		if ctxSub != "" {
			return ctxSub
		}
		return fallback
	}
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
		case "assistant_retract":
			// Drop fabricated prose from a HITL turn (errMsg preserved).
			message = ""
		case "tool_call":
			toolName, _ := data["tool"].(string)
			input, _ := data["content"].(string)
			lastTool = toolName
			toolCalls = append(toolCalls, toolCallEntry{Tool: toolName, Input: input})
			// Belt-and-suspenders retract in case the retract event hasn't arrived.
			if domain.IsHITLTool(toolName) {
				message = ""
			}
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
	forwardHeaders := h.forwardHeadersFn(ctx)
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
