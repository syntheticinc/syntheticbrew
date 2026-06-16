package http

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// BuilderSchemaResolver returns the current builder-schema UUID.
// Evaluated on each request so a late seed (builder-schema created after
// HTTP startup) is picked up without restarting the server.
type BuilderSchemaResolver func(ctx context.Context) (string, error)

// SessionLastFetcher returns the most-recently-active session ID for a schema+user pair.
// Returns ("", nil) when no session exists.
type SessionLastFetcher interface {
	LastForSchema(ctx context.Context, schemaID, userSub string) (string, error)
}

// AdminAssistantHandler serves POST /api/v1/admin/assistant/chat.
// Admin-only endpoint for the builder-assistant — always chats against the
// seeded builder-schema. chat_enabled guard is applied by the underlying
// ChatService (builder-schema is seeded with chat_enabled=true).
type AdminAssistantHandler struct {
	service          ChatService
	resolveSchema    BuilderSchemaResolver
	forwardHeadersFn func(context.Context) []string
	sessions         SessionLastFetcher
}

// NewAdminAssistantHandler creates a new AdminAssistantHandler.
func NewAdminAssistantHandler(service ChatService, resolveSchema BuilderSchemaResolver, forwardHeadersFn func(context.Context) []string, sessions SessionLastFetcher) *AdminAssistantHandler {
	return &AdminAssistantHandler{service: service, resolveSchema: resolveSchema, forwardHeadersFn: forwardHeadersFn, sessions: sessions}
}

// LastSession handles GET /api/v1/admin/assistant/last-session.
// Returns {"session_id":"<uuid>"} for the most recent builder session of the current user,
// or 204 No Content if none exists yet.
func (h *AdminAssistantHandler) LastSession(w http.ResponseWriter, r *http.Request) {
	userSub := resolveUserSub(r, "")
	if userSub == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}

	schemaID, err := h.resolveSchema(r.Context())
	if err != nil || schemaID == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "builder schema not ready"})
		return
	}

	sid, err := h.sessions.LastForSchema(r.Context(), schemaID, userSub)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch session"})
		return
	}
	if sid == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"session_id": sid})
}

// adminAssistantRequest extends chatRequest with an optional schema context.
type adminAssistantRequest struct {
	chatRequest
	SchemaContext string `json:"schema_context,omitempty"`
}

// Chat handles admin assistant chat — same logic as ChatHandler.Chat but fixed to builder-assistant.
func (h *AdminAssistantHandler) Chat(w http.ResponseWriter, r *http.Request) {
	var req adminAssistantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// Caller must send exactly one of `message` / `resume_interrupt`. The HITL
	// widget answer (show_structured_output) posts resume_interrupt with no
	// message — without this branch the builder widget could never be resumed
	// (the click 400'd with "message required" and the turn appeared frozen).
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

	// Prepend schema context to the message so the assistant knows its working
	// scope (message path only — a resume continues an existing turn).
	if hasMessage && req.SchemaContext != "" {
		req.Message = "[Schema: " + req.SchemaContext + "]\n\n" + req.Message
	}

	ctx := h.buildRequestContext(r)
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

	userSub := resolveUserSub(r, req.UserSub)
	if userSub == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}

	schemaID, err := h.resolveSchema(r.Context())
	if err != nil || schemaID == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "builder schema not ready"})
		return
	}

	var events <-chan SSEEvent
	if hasResume {
		events, err = h.service.ResumeInterrupt(
			ctx, schemaID, userSub, req.SessionID,
			req.ResumeInterrupt.InterruptID, req.ResumeInterrupt.Payload,
		)
	} else {
		events, err = h.service.Chat(ctx, schemaID, req.Message, userSub, req.SessionID)
	}
	if err != nil {
		writeDomainError(w, err)
		return
	}

	// Non-streaming: collect all events and return JSON.
	if req.Stream != nil && !*req.Stream {
		h.handleNonStreaming(w, schemaID, events)
		return
	}

	// Streaming: SSE.
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

// handleNonStreaming collects SSE events and returns a single JSON response.
func (h *AdminAssistantHandler) handleNonStreaming(w http.ResponseWriter, schemaID string, events <-chan SSEEvent) {
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

// buildRequestContext extracts configured forward headers from the HTTP request.
func (h *AdminAssistantHandler) buildRequestContext(r *http.Request) context.Context {
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

	return domain.WithRequestContext(ctx, &domain.RequestContext{Headers: headers})
}
