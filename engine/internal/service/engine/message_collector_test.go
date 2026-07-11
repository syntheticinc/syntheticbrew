package engine

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// captureCtxHistoryRepo records the ctx passed to every Create call. Used to
// verify that MessageCollector propagates the tenant_id from its constructor
// ctx into DB writes — without it, multi-tenant users lose their assistant/tool/
// reasoning rows on reload (the 2026-04-27 "last AI message disappears" bug).
type captureCtxHistoryRepo struct {
	mu       sync.Mutex
	ctxs     []context.Context
	messages []*domain.Message
}

func (r *captureCtxHistoryRepo) Create(ctx context.Context, message *domain.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ctxs = append(r.ctxs, ctx)
	r.messages = append(r.messages, message)
	return nil
}

// TestMessageCollector_PropagatesTenantToHandleEvent reproduces the bug where
// streamed assistant messages were written under CETenantID instead of the
// caller's tenant. handleEvent must inherit the ctx given at construction so
// that MessageRepositoryImpl.Create can stamp the right tenant_id.
func TestMessageCollector_PropagatesTenantToHandleEvent(t *testing.T) {
	const tenant = "9238e024-adbd-ef67-933d-51465a5a5280"
	repo := &captureCtxHistoryRepo{}

	parentCtx := domain.WithTenantID(context.Background(), tenant)
	mc := NewMessageCollector(parentCtx, "session-1", "supervisor", repo)

	cb := mc.WrapEventCallback(nil)

	// Streamed final assistant message — the path that vanished on reload.
	require.NoError(t, cb(&domain.AgentEvent{
		Type:    domain.EventTypeAnswer,
		Content: "Echo: hi",
	}))

	// Tool call + result so we cover the other handleEvent branches too.
	require.NoError(t, cb(&domain.AgentEvent{
		Type: domain.EventTypeToolCall,
		Metadata: map[string]interface{}{
			"id":                 "call-1",
			"tool_name":          "echo_message",
			"function_arguments": `{"text":"hi"}`,
		},
	}))
	require.NoError(t, cb(&domain.AgentEvent{
		Type: domain.EventTypeToolResult,
		Metadata: map[string]interface{}{
			"tool_name":   "echo_message",
			"full_result": "ok",
		},
		Content: "ok",
	}))

	// Reasoning gets persisted only when IsComplete=true.
	require.NoError(t, cb(&domain.AgentEvent{
		Type:       domain.EventTypeReasoning,
		Content:    "I'll echo it back.",
		IsComplete: true,
	}))

	require.NotEmpty(t, repo.ctxs, "MessageCollector must persist at least one event")

	for i, ctx := range repo.ctxs {
		got := domain.TenantIDFromContext(ctx)
		assert.Equalf(t, tenant, got,
			"event #%d (%s) lost tenant_id — got %q, want %q",
			i, repo.messages[i].Type, got, tenant)
	}
}

// AgentEvent with Error → persisted payload.IsError=true.
func TestMessageCollector_ToolResultErrorPersistsIsErrorFlag(t *testing.T) {
	repo := &captureCtxHistoryRepo{}
	mc := NewMessageCollector(context.Background(), "session-err", "supervisor", repo)
	cb := mc.WrapEventCallback(nil)

	require.NoError(t, cb(&domain.AgentEvent{
		Type: domain.EventTypeToolCall,
		Metadata: map[string]interface{}{
			"id":                 "call-1",
			"tool_name":          "rule.list",
			"function_arguments": `{}`,
		},
	}))
	require.NoError(t, cb(&domain.AgentEvent{
		Type: domain.EventTypeToolResult,
		Metadata: map[string]interface{}{
			"tool_name":   "rule.list",
			"full_result": "[UNAVAILABLE] circuit breaker open for chirp-platform: too many failures",
		},
		Content: "[UNAVAILABLE] circuit breaker open for chirp-platform: too many failures",
		Error:   &domain.AgentError{Code: "tool_error", Message: "circuit breaker open"},
	}))

	var toolResult *domain.Message
	for _, m := range repo.messages {
		if m.Type == domain.MessageTypeToolResult {
			toolResult = m
			break
		}
	}
	require.NotNil(t, toolResult, "tool_result message was not persisted")

	p, ok := toolResult.GetToolResultPayload()
	require.True(t, ok)
	assert.True(t, p.IsError, "tool_result with AgentEvent.Error must persist IsError=true")
	assert.Contains(t, p.Content, "circuit breaker open")
}

// Happy-path tool_result JSON omits is_error (back-compat).
func TestMessageCollector_ToolResultHappyPathOmitsIsError(t *testing.T) {
	repo := &captureCtxHistoryRepo{}
	mc := NewMessageCollector(context.Background(), "session-ok", "supervisor", repo)
	cb := mc.WrapEventCallback(nil)

	require.NoError(t, cb(&domain.AgentEvent{
		Type: domain.EventTypeToolCall,
		Metadata: map[string]interface{}{
			"id":                 "call-1",
			"tool_name":          "echo_message",
			"function_arguments": `{"text":"hi"}`,
		},
	}))
	require.NoError(t, cb(&domain.AgentEvent{
		Type: domain.EventTypeToolResult,
		Metadata: map[string]interface{}{
			"tool_name":   "echo_message",
			"full_result": "ok",
		},
		Content: "ok",
	}))

	var toolResult *domain.Message
	for _, m := range repo.messages {
		if m.Type == domain.MessageTypeToolResult {
			toolResult = m
			break
		}
	}
	require.NotNil(t, toolResult)

	p, _ := toolResult.GetToolResultPayload()
	assert.False(t, p.IsError)
	assert.NotContains(t, string(toolResult.Payload), "is_error",
		"happy-path payload JSON must omit is_error field for back-compat")
}

// TestMessageCollector_NilCtxFallsBackToBackground guards the safety net in
// handleEvent: if a caller forgets to pass ctx, writes still succeed (with the
// CETenantID default) instead of panicking.
func TestMessageCollector_NilCtxFallsBackToBackground(t *testing.T) {
	repo := &captureCtxHistoryRepo{}
	mc := &MessageCollector{
		sessionID:   "session-2",
		agentID:     "supervisor",
		historyRepo: repo,
	}

	cb := mc.WrapEventCallback(nil)
	require.NoError(t, cb(&domain.AgentEvent{
		Type:    domain.EventTypeAnswer,
		Content: "fallback",
	}))

	require.Len(t, repo.ctxs, 1)
	assert.NotNil(t, repo.ctxs[0])
}
