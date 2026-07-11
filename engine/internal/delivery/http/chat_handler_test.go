package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

func TestHandleNonStreaming_EmptyMessageEventDoesNotOverwrite(t *testing.T) {
	tests := []struct {
		name     string
		events   []SSEEvent
		wantMsg  string
		wantSID  string
		wantTool int
		wantErr  string
	}{
		{
			name: "trailing empty message event does not erase answer",
			events: []SSEEvent{
				sseEvent("message_delta", map[string]interface{}{"content": "Hello"}),
				sseEvent("message_delta", map[string]interface{}{"content": " world!"}),
				sseEvent("message", map[string]interface{}{"content": "Hello world!"}),
				// Engine sends this trailing "completion signal" with empty content
				sseEvent("message", map[string]interface{}{"content": ""}),
				sseEvent("done", map[string]interface{}{"session_id": "sess-1"}),
			},
			wantMsg: "Hello world!",
			wantSID: "sess-1",
		},
		{
			name: "single message event with content works normally",
			events: []SSEEvent{
				sseEvent("message", map[string]interface{}{"content": "Hi there"}),
				sseEvent("done", map[string]interface{}{"session_id": "sess-2"}),
			},
			wantMsg: "Hi there",
			wantSID: "sess-2",
		},
		{
			name: "only deltas without final message works",
			events: []SSEEvent{
				sseEvent("message_delta", map[string]interface{}{"content": "chunk1"}),
				sseEvent("message_delta", map[string]interface{}{"content": "chunk2"}),
				sseEvent("done", map[string]interface{}{"session_id": "sess-3"}),
			},
			wantMsg: "chunk1chunk2",
			wantSID: "sess-3",
		},
		{
			name: "message replaces accumulated deltas",
			events: []SSEEvent{
				sseEvent("message_delta", map[string]interface{}{"content": "chunk1"}),
				sseEvent("message_delta", map[string]interface{}{"content": "chunk2"}),
				sseEvent("message", map[string]interface{}{"content": "full answer"}),
				sseEvent("done", map[string]interface{}{"session_id": "sess-4"}),
			},
			wantMsg: "full answer",
			wantSID: "sess-4",
		},
		{
			name: "tool calls are collected",
			events: []SSEEvent{
				sseEvent("tool_call", map[string]interface{}{"tool": "search", "content": "query"}),
				sseEvent("tool_result", map[string]interface{}{"tool": "search", "content": "results"}),
				sseEvent("message", map[string]interface{}{"content": "Done"}),
				sseEvent("done", map[string]interface{}{"session_id": "sess-5"}),
			},
			wantMsg:  "Done",
			wantSID:  "sess-5",
			wantTool: 1,
		},
		{
			name: "only empty message events result in empty message",
			events: []SSEEvent{
				sseEvent("message", map[string]interface{}{"content": ""}),
				sseEvent("done", map[string]interface{}{"session_id": "sess-6"}),
			},
			wantMsg: "",
			wantSID: "sess-6",
		},
		{
			name: "error event with no message uses error as message",
			events: []SSEEvent{
				sseEvent("error", map[string]interface{}{"content": "exceeds max steps"}),
				sseEvent("done", map[string]interface{}{"session_id": "sess-7"}),
			},
			wantMsg: "exceeds max steps",
			wantSID: "sess-7",
			wantErr: "exceeds max steps",
		},
		{
			name: "error event with message keeps both",
			events: []SSEEvent{
				sseEvent("message", map[string]interface{}{"content": "partial answer"}),
				sseEvent("error", map[string]interface{}{"content": "model timeout"}),
				sseEvent("done", map[string]interface{}{"session_id": "sess-8"}),
			},
			wantMsg: "partial answer",
			wantSID: "sess-8",
			wantErr: "model timeout",
		},
		{
			name: "error event with message field instead of content",
			events: []SSEEvent{
				sseEvent("error", map[string]interface{}{"message": "rate limited", "code": "429"}),
				sseEvent("done", map[string]interface{}{"session_id": "sess-9"}),
			},
			wantMsg: "rate limited",
			wantSID: "sess-9",
			wantErr: "rate limited",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := make(chan SSEEvent, len(tt.events))
			for _, e := range tt.events {
				ch <- e
			}
			close(ch)

			w := httptest.NewRecorder()
			h := &ChatHandler{}
			h.handleNonStreaming(w, "test-schema", ch)

			var resp nonStreamResponse
			err := json.Unmarshal(w.Body.Bytes(), &resp)
			require.NoError(t, err)

			assert.Equal(t, tt.wantMsg, resp.Message)
			assert.Equal(t, tt.wantSID, resp.SessionID)
			assert.Equal(t, "test-schema", resp.SchemaID)
			assert.Len(t, resp.ToolCalls, tt.wantTool)
			assert.Equal(t, tt.wantErr, resp.Error)
		})
	}
}

// sseEvent creates an SSEEvent with JSON-encoded data.
func sseEvent(eventType string, data map[string]interface{}) SSEEvent {
	jsonBytes, _ := json.Marshal(data)
	return SSEEvent{
		Type: eventType,
		Data: string(jsonBytes),
	}
}

// chatRequestWithActor builds a request carrying the auth-middleware context
// values (actor type + stamped UserSub) that resolveUserSub reads.
func chatRequestWithActor(actorType, ctxSub string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/schemas/test/chat", nil)
	ctx := req.Context()
	if actorType != "" {
		ctx = context.WithValue(ctx, ContextKeyActorType, actorType)
	}
	if ctxSub != "" {
		ctx = domain.WithUserSub(ctx, ctxSub)
	}
	return req.WithContext(ctx)
}

func TestResolveUserSub(t *testing.T) {
	// 190-char token name + ":" + 64-char visitor = exactly 255 (boundary).
	longToken := strings.Repeat("t", 190)
	overToken := strings.Repeat("t", 191)
	maxVisitor := strings.Repeat("v", 64)

	tests := []struct {
		name      string
		actorType string
		ctxSub    string
		fallback  string
		want      string
	}{
		{
			name:      "api_token with valid visitor id namespaces under token",
			actorType: "api_token",
			ctxSub:    "web-widget",
			fallback:  "visitor-42",
			want:      "web-widget:visitor-42",
		},
		{
			name:      "api_token with path-traversal charset falls back to token name",
			actorType: "api_token",
			ctxSub:    "web-widget",
			fallback:  "../x",
			want:      "web-widget",
		},
		{
			// A ctxSub containing ':' (unexpected/legacy name, or an external
			// JWT subject) must NOT be namespaced — the combined id could
			// collide with another principal's canonical sub.
			name:      "api_token whose ctxSub contains colon is not namespaced",
			actorType: "api_token",
			ctxSub:    "team:alice",
			fallback:  "visitor-42",
			want:      "team:alice",
		},
		{
			name:      "api_token with non-ASCII visitor falls back to token name",
			actorType: "api_token",
			ctxSub:    "web-widget",
			fallback:  "тест",
			want:      "web-widget",
		},
		{
			name:      "api_token with 65-char visitor falls back to token name",
			actorType: "api_token",
			ctxSub:    "web-widget",
			fallback:  strings.Repeat("a", 65),
			want:      "web-widget",
		},
		{
			name:      "api_token with empty body returns token name",
			actorType: "api_token",
			ctxSub:    "web-widget",
			fallback:  "",
			want:      "web-widget",
		},
		{
			name:      "api_token without ctx sub returns empty (regression guard)",
			actorType: "api_token",
			ctxSub:    "",
			fallback:  "visitor-42",
			want:      "",
		},
		{
			name:      "api_token combined id at 255 bytes is namespaced",
			actorType: "api_token",
			ctxSub:    longToken,
			fallback:  maxVisitor,
			want:      longToken + ":" + maxVisitor,
		},
		{
			name:      "api_token combined id over 255 bytes falls back to token name",
			actorType: "api_token",
			ctxSub:    overToken,
			fallback:  maxVisitor,
			want:      overToken,
		},
		{
			name:      "admin ignores body field",
			actorType: "admin",
			ctxSub:    "admin-sub",
			fallback:  "visitor-42",
			want:      "admin-sub",
		},
		{
			name:      "admin without ctx sub returns empty",
			actorType: "admin",
			ctxSub:    "",
			fallback:  "visitor-42",
			want:      "",
		},
		{
			name:      "no actor with ctx sub prefers ctx",
			actorType: "",
			ctxSub:    "ctx-sub",
			fallback:  "body-sub",
			want:      "ctx-sub",
		},
		{
			name:      "no actor without ctx sub falls back to body",
			actorType: "",
			ctxSub:    "",
			fallback:  "body-sub",
			want:      "body-sub",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := chatRequestWithActor(tt.actorType, tt.ctxSub)
			assert.Equal(t, tt.want, resolveUserSub(req, tt.fallback))
		})
	}
}
