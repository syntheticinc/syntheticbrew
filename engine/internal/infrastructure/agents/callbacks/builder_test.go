package callbacks

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

func TestNewBuilder_DefaultAgentID(t *testing.T) {
	collector := newEventCollector()
	b := NewBuilder(BuilderConfig{
		EventCallback: collector.Callback,
	})

	// Emit an event and verify the default agentID is "supervisor"
	b.modelHandler.emitter.Emit(context.Background(), &domain.AgentEvent{
		Type:    domain.EventTypeToolCall,
		Content: "test",
	})

	events := collector.GetEvents()
	require.Len(t, events, 1)
	assert.Equal(t, "supervisor", events[0].AgentID)
}

func TestNewBuilder_CustomAgentID(t *testing.T) {
	collector := newEventCollector()
	b := NewBuilder(BuilderConfig{
		EventCallback: collector.Callback,
		AgentID:       "code-agent-abc",
	})

	b.modelHandler.emitter.Emit(context.Background(), &domain.AgentEvent{
		Type:    domain.EventTypeToolCall,
		Content: "test",
	})

	events := collector.GetEvents()
	require.Len(t, events, 1)
	assert.Equal(t, "code-agent-abc", events[0].AgentID)
}

func TestNewBuilder_GetStep(t *testing.T) {
	b := NewBuilder(BuilderConfig{})
	assert.Equal(t, 0, b.GetStep())
}

func TestNewBuilder_BuildCallbackOption(t *testing.T) {
	b := NewBuilder(BuilderConfig{})

	// Should not panic and should return a valid option
	opt := b.BuildCallbackOption()
	_ = opt
}

func TestNewBuilder_NilCallbacks(t *testing.T) {
	// Handler should work even with nil callbacks
	b := NewBuilder(BuilderConfig{})

	// These should not panic
	assert.Equal(t, 0, b.GetStep())
	_ = b.BuildCallbackOption()
}

func TestNewBuilder_FinalizeAccumulatedText(t *testing.T) {
	collector := newEventCollector()
	b := NewBuilder(BuilderConfig{
		EventCallback: collector.Callback,
	})

	// No accumulated text - should not emit
	b.FinalizeAccumulatedText(context.Background())
	assert.Empty(t, collector.GetEvents())
}

func TestNewBuilder_EventCallback_ReceivesEvents(t *testing.T) {
	collector := newEventCollector()
	b := NewBuilder(BuilderConfig{
		EventCallback: collector.Callback,
	})

	// Emit through builder's emitter
	b.emitter.Emit(context.Background(), &domain.AgentEvent{
		Type:       domain.EventTypeAnswer,
		Timestamp:  time.Now(),
		Content:    "Test answer",
		IsComplete: true,
	})

	events := collector.GetEvents()
	require.Len(t, events, 1)
	assert.Equal(t, domain.EventTypeAnswer, events[0].Type)
	assert.Equal(t, "Test answer", events[0].Content)
}
