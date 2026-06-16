package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingChatService records which branch (Chat vs ResumeInterrupt) the
// handler invoked, so the admin-assistant tests can assert the HITL widget
// answer is routed to resume — not rejected as a missing message.
type recordingChatService struct {
	chatCalled   bool
	resumeCalled bool
	gotInterrupt string
}

func (f *recordingChatService) Chat(_ context.Context, _, _, _, _ string) (<-chan SSEEvent, error) {
	f.chatCalled = true
	ch := make(chan SSEEvent)
	close(ch)
	return ch, nil
}

func (f *recordingChatService) ResumeInterrupt(_ context.Context, _, _, _, interruptID string, _ json.RawMessage) (<-chan SSEEvent, error) {
	f.resumeCalled = true
	f.gotInterrupt = interruptID
	ch := make(chan SSEEvent)
	close(ch)
	return ch, nil
}

func newAdminAssistantTestHandler(svc ChatService) *AdminAssistantHandler {
	return NewAdminAssistantHandler(
		svc,
		func(_ context.Context) (string, error) { return testSchemaID, nil },
		fakeForwardHeaders,
		nil,
	)
}

func postAdminAssistant(h *AdminAssistantHandler, body string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Post("/api/v1/admin/assistant/chat", h.Chat)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/assistant/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// Regression for the prod bug "clicked the widget button, nothing happened, the
// chat froze". The builder AI Assistant posts the show_structured_output answer
// as resume_interrupt (no message) to /admin/assistant/chat. The handler used
// to require a message and ignored resume_interrupt entirely → 400 "message
// required" → the widget could never be resumed. It must route to ResumeInterrupt.
func TestAdminAssistant_ResumeInterrupt_RoutesToResume(t *testing.T) {
	svc := &recordingChatService{}
	h := newAdminAssistantTestHandler(svc)

	rec := postAdminAssistant(h, `{
		"user_sub":"test-user",
		"session_id":"22222222-2222-2222-2222-222222222222",
		"stream":false,
		"resume_interrupt":{"interrupt_id":"33333333-3333-3333-3333-333333333333","payload":{"answers":[]}}
	}`)

	require.NotEqual(t, http.StatusBadRequest, rec.Code, "resume must not be rejected as a missing message; body: %s", rec.Body.String())
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, svc.resumeCalled, "handler must route resume_interrupt to ResumeInterrupt")
	assert.False(t, svc.chatCalled, "handler must NOT treat a resume as a plain chat message")
	assert.Equal(t, "33333333-3333-3333-3333-333333333333", svc.gotInterrupt)
}

// The plain message path still works (no regression).
func TestAdminAssistant_PlainMessage_RoutesToChat(t *testing.T) {
	svc := &recordingChatService{}
	h := newAdminAssistantTestHandler(svc)

	rec := postAdminAssistant(h, `{"user_sub":"test-user","message":"hi","stream":false}`)

	assert.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.True(t, svc.chatCalled)
	assert.False(t, svc.resumeCalled)
}

// Neither message nor resume_interrupt → 400 (not a 500, not silent).
func TestAdminAssistant_EmptyBody_Returns400(t *testing.T) {
	svc := &recordingChatService{}
	h := newAdminAssistantTestHandler(svc)

	rec := postAdminAssistant(h, `{"user_sub":"test-user","stream":false}`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "required")
	assert.False(t, svc.chatCalled)
	assert.False(t, svc.resumeCalled)
}
