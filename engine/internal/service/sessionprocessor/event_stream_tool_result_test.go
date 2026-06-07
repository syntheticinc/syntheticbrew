package sessionprocessor

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pb "github.com/syntheticinc/syntheticbrew/api/proto/gen"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	apperrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

type mockPublisher struct {
	events []*pb.SessionEvent
}

func (m *mockPublisher) PublishEvent(_ string, event *pb.SessionEvent) {
	m.events = append(m.events, event)
}

type mockStore struct {
	appends []mockStoreEntry
}

type mockStoreEntry struct {
	sessionID string
	eventType string
	content   string
}

func (m *mockStore) Append(sessionID, eventType string, evt *pb.SessionEvent, _ map[string]interface{}) (string, error) {
	content := ""
	if evt != nil {
		content = evt.Content
	}
	m.appends = append(m.appends, mockStoreEntry{sessionID: sessionID, eventType: eventType, content: content})
	return "mock-event-id", nil
}

func TestSend_ToolResult_UsesFullResultFromMetadata(t *testing.T) {
	pub := &mockPublisher{}
	stream := NewEventStream("session-1", pub, &mockStore{}, nil)

	fullResult := "device1: iPhone 14 Pro\ndevice2: Pixel 8\ndevice3: Samsung Galaxy S24\ndevice4: OnePlus 12\ndevice5: Xiaomi 14"
	preview := "device1: iPhone 14 Pro..."

	err := stream.Send(&domain.AgentEvent{
		Type:    domain.EventTypeToolResult,
		Content: preview,
		Step:    1,
		Metadata: map[string]interface{}{
			"tool_name":   "device.list",
			"full_result": fullResult,
		},
	})

	require.NoError(t, err)
	require.Len(t, pub.events, 1)

	evt := pub.events[0]
	assert.Equal(t, pb.SessionEventType_SESSION_EVENT_TOOL_EXECUTION_END, evt.Type)
	assert.Equal(t, fullResult, evt.Content, "Content should be the full result, not the truncated preview")
	assert.NotEqual(t, preview, evt.Content)
}

func TestSend_ToolResult_FallsBackToContent(t *testing.T) {
	pub := &mockPublisher{}
	stream := NewEventStream("session-1", pub, &mockStore{}, nil)

	content := "result without full_result metadata"

	err := stream.Send(&domain.AgentEvent{
		Type:    domain.EventTypeToolResult,
		Content: content,
		Step:    2,
		Metadata: map[string]interface{}{
			"tool_name": "device.list",
		},
	})

	require.NoError(t, err)
	require.Len(t, pub.events, 1)

	evt := pub.events[0]
	assert.Equal(t, pb.SessionEventType_SESSION_EVENT_TOOL_EXECUTION_END, evt.Type)
	assert.Equal(t, content, evt.Content, "Content should fall back to event.Content when full_result is absent")
}

func TestSend_Answer_SkipsSSEWhenAlreadyStreamed(t *testing.T) {
	pub := &mockPublisher{}
	store := &mockStore{}
	stream := NewEventStream("session-1", pub, store, nil)

	content := "This text was already sent via message_delta chunks"
	err := stream.Send(&domain.AgentEvent{
		Type:    domain.EventTypeAnswer,
		Content: content,
		Metadata: map[string]interface{}{
			"already_streamed": true,
		},
	})

	require.NoError(t, err)
	assert.Empty(t, pub.events, "Should NOT publish SSE when already_streamed=true")
	// Bug 1 regression guard: streamed final message MUST land in the store so
	// GET /sessions/{id}/messages returns it on reload. Exactly once — not
	// zero (the original bug) and not more than once (duplicate-persist guard).
	require.Len(t, store.appends, 1, "Should persist exactly one row for already_streamed=true answer")
	assert.Equal(t, "answer", store.appends[0].eventType)
	assert.Equal(t, content, store.appends[0].content)
}

func TestSend_Answer_PublishesWhenNotStreamed(t *testing.T) {
	pub := &mockPublisher{}
	stream := NewEventStream("session-1", pub, &mockStore{}, nil)

	err := stream.Send(&domain.AgentEvent{
		Type:    domain.EventTypeAnswer,
		Content: "Non-streaming answer",
	})

	require.NoError(t, err)
	require.Len(t, pub.events, 1)
	assert.Equal(t, pb.SessionEventType_SESSION_EVENT_ANSWER, pub.events[0].Type)
	assert.Equal(t, "Non-streaming answer", pub.events[0].Content)
}

func TestSend_ToolResult_PreservesSummary(t *testing.T) {
	pub := &mockPublisher{}
	stream := NewEventStream("session-1", pub, &mockStore{}, nil)

	fullResult := "device1: iPhone 14 Pro\ndevice2: Pixel 8\ndevice3: Samsung Galaxy S24"
	summary := "3 devices found"

	err := stream.Send(&domain.AgentEvent{
		Type:    domain.EventTypeToolResult,
		Content: "device1: iPhone...",
		Step:    3,
		Metadata: map[string]interface{}{
			"tool_name":   "device.list",
			"full_result": fullResult,
			"summary":     summary,
		},
	})

	require.NoError(t, err)
	require.Len(t, pub.events, 1)

	evt := pub.events[0]
	assert.Equal(t, fullResult, evt.Content, "Content should be the full result")
	assert.Equal(t, summary, evt.ToolResultSummary, "ToolResultSummary should be the summary")
}

func TestPublishError_CarriesTypedCodeAndCuratedMessage(t *testing.T) {
	pub := &mockPublisher{}
	stream := NewEventStream("session-1", pub, &mockStore{}, nil)

	inner := apperrors.Unavailable("Service temporarily unavailable — please try again in a few seconds.", errorsNew("circuit breaker open for external-service"))
	stream.PublishError(apperrors.Wrap(inner, apperrors.CodeInternal, "agent stream failed"))

	require.Len(t, pub.events, 1)
	evt := pub.events[0]
	assert.Equal(t, pb.SessionEventType_SESSION_EVENT_ERROR, evt.Type)
	require.NotNil(t, evt.ErrorDetail)
	assert.Equal(t, apperrors.CodeUnavailable, evt.ErrorDetail.Code, "deepest typed code must surface, not generic internal")
	assert.Equal(t, "Service temporarily unavailable — please try again in a few seconds.", evt.Content, "curated user message, not the wrapped technical chain")
	assert.NotContains(t, evt.Content, "circuit breaker open", "raw technical detail must not leak to the client")
}

func errorsNew(s string) error { return fmt.Errorf("%s", s) }
