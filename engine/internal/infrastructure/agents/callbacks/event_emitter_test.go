package callbacks

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eventCollector collects events for testing
type eventCollector struct {
	mu     sync.Mutex
	events []*domain.AgentEvent
}

func newEventCollector() *eventCollector {
	return &eventCollector{
		events: make([]*domain.AgentEvent, 0),
	}
}

func (c *eventCollector) Callback(event *domain.AgentEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
	return nil
}

func (c *eventCollector) GetEvents() []*domain.AgentEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]*domain.AgentEvent, len(c.events))
	copy(result, c.events)
	return result
}

func (c *eventCollector) GetEventsByType(eventType domain.AgentEventType) []*domain.AgentEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []*domain.AgentEvent
	for _, e := range c.events {
		if e.Type == eventType {
			result = append(result, e)
		}
	}
	return result
}

// chunkCollector collects chunks for testing
type chunkCollector struct {
	mu     sync.Mutex
	chunks []string
}

func newChunkCollector() *chunkCollector {
	return &chunkCollector{
		chunks: make([]string, 0),
	}
}

func (c *chunkCollector) Callback(chunk string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.chunks = append(c.chunks, chunk)
	return nil
}

func (c *chunkCollector) GetChunks() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]string, len(c.chunks))
	copy(result, c.chunks)
	return result
}

func TestEventEmitter_Emit_SetsAgentID(t *testing.T) {
	collector := newEventCollector()
	emitter := NewEventEmitter(collector.Callback, "test-agent")

	event := &domain.AgentEvent{
		Type:      domain.EventTypeToolCall,
		Timestamp: time.Now(),
		Step:      0,
		Content:   "read_file",
		Metadata:  make(map[string]interface{}),
	}
	emitter.Emit(context.Background(), event)

	events := collector.GetEvents()
	require.Len(t, events, 1)
	assert.Equal(t, "test-agent", events[0].AgentID)
}

func TestEventEmitter_Emit_PreservesExistingAgentID(t *testing.T) {
	collector := newEventCollector()
	emitter := NewEventEmitter(collector.Callback, "supervisor")

	event := &domain.AgentEvent{
		Type:    domain.EventTypeToolCall,
		AgentID: "code-agent-abc",
		Content: "read_file",
	}
	emitter.Emit(context.Background(), event)

	events := collector.GetEvents()
	require.Len(t, events, 1)
	assert.Equal(t, "code-agent-abc", events[0].AgentID)
}

func TestEventEmitter_Emit_NilCallback(t *testing.T) {
	emitter := NewEventEmitter(nil, "supervisor")

	// Should not panic
	event := &domain.AgentEvent{
		Type:    domain.EventTypeToolCall,
		Content: "read_file",
	}
	emitter.Emit(context.Background(), event)
}
