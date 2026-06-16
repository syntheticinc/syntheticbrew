package sessionprocessor

import (
	"context"
	"encoding/json"
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

// tenantRecordingInterruptCreator records the ctx its Create was called with so
// a test can assert the interrupts row is stamped with the session's tenant.
type tenantRecordingInterruptCreator struct {
	called bool
	tenant string
}

func (c *tenantRecordingInterruptCreator) Create(ctx context.Context, _ *domain.Interrupt) error {
	c.called = true
	c.tenant = domain.TenantIDFromContext(ctx)
	return nil
}

// Regression for the Cloud HITL bug: the interrupts row was created with
// context.Background() → resolved to the CE default tenant → a multi-tenant
// (Cloud) resume lookup (tenant-scoped) returned 404 "interrupt not found".
// The row must be stamped with the session's tenant carried by the EventStream
// ctx (the processing goroutine preserves context values via WithoutCancel).
func TestSend_InterruptRequest_CreatedUnderSessionTenant(t *testing.T) {
	rec := &tenantRecordingInterruptCreator{}
	const tenant = "11111111-1111-1111-1111-111111111111"
	ctx := domain.WithTenantID(context.Background(), tenant)
	stream := NewEventStream(ctx, "session-cloud", &mockPublisher{}, &mockStore{}, rec)

	err := stream.Send(&domain.AgentEvent{
		Type:     domain.EventTypeInterruptRequest,
		Content:  `{"interrupt_id":"int-1"}`,
		Metadata: map[string]interface{}{"interrupt_id": "int-1"},
	})
	require.NoError(t, err)
	require.True(t, rec.called, "interrupt row must be created on interrupt_request")
	assert.Equal(t, tenant, rec.tenant,
		"interrupts row must be stamped with the session tenant, not context.Background()")
}

func TestSend_ToolResult_UsesFullResultFromMetadata(t *testing.T) {
	pub := &mockPublisher{}
	stream := NewEventStream(context.Background(), "session-1",pub, &mockStore{}, nil)

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
	stream := NewEventStream(context.Background(), "session-1",pub, &mockStore{}, nil)

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
	stream := NewEventStream(context.Background(), "session-1",pub, store, nil)

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
	stream := NewEventStream(context.Background(), "session-1",pub, &mockStore{}, nil)

	err := stream.Send(&domain.AgentEvent{
		Type:    domain.EventTypeAnswer,
		Content: "Non-streaming answer",
	})

	require.NoError(t, err)
	require.Len(t, pub.events, 1)
	assert.Equal(t, pb.SessionEventType_SESSION_EVENT_ANSWER, pub.events[0].Type)
	assert.Equal(t, "Non-streaming answer", pub.events[0].Content)
}

func TestSend_Answer_SkipsEmptyContent(t *testing.T) {
	pub := &mockPublisher{}
	store := &mockStore{}
	stream := NewEventStream(context.Background(), "session-1",pub, store, nil)

	err := stream.Send(&domain.AgentEvent{
		Type:       domain.EventTypeAnswer,
		Content:    "",
		IsComplete: true,
	})

	require.NoError(t, err)
	assert.Empty(t, pub.events, "Should NOT publish an empty final answer (blank assistant bubble)")
	assert.Empty(t, store.appends, "Should NOT persist an empty answer")
}

func TestSend_ToolResult_PreservesSummary(t *testing.T) {
	pub := &mockPublisher{}
	stream := NewEventStream(context.Background(), "session-1",pub, &mockStore{}, nil)

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
	stream := NewEventStream(context.Background(), "session-1",pub, &mockStore{}, nil)

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

// processingStoppedTokens decodes the token-usage JSON folded into a
// PROCESSING_STOPPED event's Content. Returns nil when Content is empty.
func processingStoppedTokens(t *testing.T, evt *pb.SessionEvent) map[string]int {
	t.Helper()
	require.Equal(t, pb.SessionEventType_SESSION_EVENT_PROCESSING_STOPPED, evt.Type)
	if evt.Content == "" {
		return nil
	}
	var data map[string]int
	require.NoError(t, json.Unmarshal([]byte(evt.Content), &data))
	return data
}

func TestPublishProcessingStopped_IncludesCachedAndBreakdownTokens(t *testing.T) {
	pub := &mockPublisher{}
	stream := NewEventStream(context.Background(), "session-1",pub, &mockStore{}, nil)

	// token_usage is captured (not broadcast) then folded into ProcessingStopped.
	err := stream.Send(&domain.AgentEvent{
		Type: domain.EventTypeTokenUsage,
		Metadata: map[string]interface{}{
			"total_tokens":         5000,
			"context_tokens":       4800,
			"prompt_tokens":        4600,
			"completion_tokens":    400,
			"cached_prompt_tokens": 4622,
		},
	})
	require.NoError(t, err)
	assert.Empty(t, pub.events, "token_usage must be captured, not broadcast")

	stream.PublishProcessingStopped()
	require.Len(t, pub.events, 1)

	data := processingStoppedTokens(t, pub.events[0])
	require.NotNil(t, data)
	assert.Equal(t, 5000, data["total_tokens"])
	assert.Equal(t, 4800, data["context_tokens"])
	assert.Equal(t, 4600, data["prompt_tokens"])
	assert.Equal(t, 400, data["completion_tokens"])
	assert.Equal(t, 4622, data["cached_prompt_tokens"])
}

func TestPublishProcessingStopped_OmitsCachedWhenZero(t *testing.T) {
	pub := &mockPublisher{}
	stream := NewEventStream(context.Background(), "session-1",pub, &mockStore{}, nil)

	// No cached_prompt_tokens key (EmitTokenUsage emits it only when >0).
	err := stream.Send(&domain.AgentEvent{
		Type: domain.EventTypeTokenUsage,
		Metadata: map[string]interface{}{
			"total_tokens":      5000,
			"prompt_tokens":     4600,
			"completion_tokens": 400,
		},
	})
	require.NoError(t, err)

	stream.PublishProcessingStopped()
	require.Len(t, pub.events, 1)

	data := processingStoppedTokens(t, pub.events[0])
	require.NotNil(t, data)
	assert.Equal(t, 5000, data["total_tokens"])
	assert.Equal(t, 4600, data["prompt_tokens"])
	assert.Equal(t, 400, data["completion_tokens"])
	_, hasCached := data["cached_prompt_tokens"]
	assert.False(t, hasCached, "cached_prompt_tokens must be omitted when 0 (the >0 gate)")
	_, hasContext := data["context_tokens"]
	assert.False(t, hasContext, "context_tokens must be omitted when not reported")
}

func errorsNew(s string) error { return fmt.Errorf("%s", s) }
