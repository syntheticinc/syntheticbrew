package callbacks

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/callbacks"
	einotool "github.com/cloudwego/eino/components/tool"

	"github.com/syntheticinc/bytebrew/engine/internal/domain"
	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/agents"
)

// HITL chain integration test: covers the wire-through from
// ToolEventHandler.OnToolStart → ModelEventHandler.MarkHITLSeen →
// FinalizeAccumulatedText → EventTypeAnswer suppression. Guards against
// regressions in any of the four defense layers.

type capturedEvents struct {
	all      []*domain.AgentEvent
	byType   map[domain.AgentEventType]int
	contents map[domain.AgentEventType][]string
}

func newCapturedEvents() *capturedEvents {
	return &capturedEvents{
		byType:   make(map[domain.AgentEventType]int),
		contents: make(map[domain.AgentEventType][]string),
	}
}

func (c *capturedEvents) emit(event *domain.AgentEvent) error {
	c.all = append(c.all, event)
	c.byType[event.Type]++
	c.contents[event.Type] = append(c.contents[event.Type], event.Content)
	return nil
}

func newTestBuilder(captured *capturedEvents) *AgentCallbackBuilder {
	return NewBuilder(BuilderConfig{
		EventCallback: captured.emit,
		ChunkCallback: func(chunk string) error { return nil },
		Store:         agents.NewStepContentStore(),
		SessionID:     "test-session",
		AgentID:       "test-agent",
	})
}

func TestHITL_DropsAccumulatedTextOnHITLTool(t *testing.T) {
	captured := newCapturedEvents()
	b := newTestBuilder(captured)

	// Simulate the model emitting prose chunks that accumulate.
	b.modelHandler.accumulatedMu.Lock()
	b.modelHandler.accumulatedChunks = "I'll help you reprogram the device..."
	b.modelHandler.chunksStreamed = true
	b.modelHandler.accumulatedMu.Unlock()

	// HITL tool_call arrives — handler must mark + finalize must drop.
	ctx := context.Background()
	toolHandler := b.toolHandler
	toolHandler.OnToolStart(ctx, &callbacks.RunInfo{Name: "show_structured_output"}, &einotool.CallbackInput{ArgumentsInJSON: "{}"})

	if !b.HITLSeen() {
		t.Fatal("HITLSeen must report true after OnToolStart for show_structured_output")
	}

	// FinalizeAccumulatedText must NOT emit EventTypeAnswer — the prose
	// was fabricated alongside the HITL widget.
	if captured.byType[domain.EventTypeAnswer] > 0 {
		t.Errorf("EventTypeAnswer must not be emitted on HITL turn; got %d", captured.byType[domain.EventTypeAnswer])
	}

	// Retract event must be emitted so SSE consumers can scrub already-
	// delivered chunks.
	if captured.byType[domain.EventTypeRetractAssistant] != 1 {
		t.Errorf("EventTypeRetractAssistant must be emitted exactly once, got %d",
			captured.byType[domain.EventTypeRetractAssistant])
	}

	// Tool_call event for the HITL tool must still be emitted — the user
	// must see the widget.
	if captured.byType[domain.EventTypeToolCall] != 1 {
		t.Errorf("EventTypeToolCall must be emitted exactly once for HITL tool, got %d",
			captured.byType[domain.EventTypeToolCall])
	}
}

func TestHITL_PreservesAccumulatedTextOnNonHITLTool(t *testing.T) {
	captured := newCapturedEvents()
	b := newTestBuilder(captured)

	// Accumulate prose.
	b.modelHandler.accumulatedMu.Lock()
	b.modelHandler.accumulatedChunks = "Let me search the knowledge base..."
	b.modelHandler.chunksStreamed = true
	b.modelHandler.accumulatedMu.Unlock()

	// Non-HITL tool fires — accumulated prose is a legitimate "thinking
	// before acting" preamble and must be preserved as EventTypeAnswer
	// in history.
	ctx := context.Background()
	b.toolHandler.OnToolStart(ctx, &callbacks.RunInfo{Name: "knowledge_search"}, &einotool.CallbackInput{ArgumentsInJSON: "{}"})

	if b.HITLSeen() {
		t.Fatal("HITLSeen must remain false for non-HITL tools")
	}
	if captured.byType[domain.EventTypeAnswer] != 1 {
		t.Errorf("EventTypeAnswer must be emitted for non-HITL turn (preserved preamble), got %d",
			captured.byType[domain.EventTypeAnswer])
	}
	if captured.byType[domain.EventTypeRetractAssistant] != 0 {
		t.Errorf("EventTypeRetractAssistant must NOT fire for non-HITL tools, got %d",
			captured.byType[domain.EventTypeRetractAssistant])
	}
}

func TestHITL_RetractEventNotEmittedWithoutHITLTool(t *testing.T) {
	captured := newCapturedEvents()
	b := newTestBuilder(captured)

	ctx := context.Background()
	// Several non-HITL tools — none should trigger retract.
	for _, name := range []string{"manage_tasks", "memory_recall", "knowledge_search"} {
		b.toolHandler.OnToolStart(ctx, &callbacks.RunInfo{Name: name}, &einotool.CallbackInput{ArgumentsInJSON: "{}"})
	}
	if captured.byType[domain.EventTypeRetractAssistant] != 0 {
		t.Errorf("Retract must not be emitted for non-HITL tools, got %d",
			captured.byType[domain.EventTypeRetractAssistant])
	}
	if b.HITLSeen() {
		t.Fatal("HITLSeen must be false when only non-HITL tools fired")
	}
}

func TestHITL_FlagPersistsAfterMultipleNonHITLTools(t *testing.T) {
	captured := newCapturedEvents()
	b := newTestBuilder(captured)

	ctx := context.Background()
	// Sequence: text → non-HITL tool → HITL tool → another non-HITL tool.
	// Once HITL fires, the flag must stay set for the rest of the turn so
	// any prose that arrives later is also suppressed.
	b.toolHandler.OnToolStart(ctx, &callbacks.RunInfo{Name: "knowledge_search"}, &einotool.CallbackInput{ArgumentsInJSON: "{}"})
	if b.HITLSeen() {
		t.Fatal("HITLSeen must be false after only non-HITL tool")
	}
	b.toolHandler.OnToolStart(ctx, &callbacks.RunInfo{Name: "show_structured_output"}, &einotool.CallbackInput{ArgumentsInJSON: "{}"})
	if !b.HITLSeen() {
		t.Fatal("HITLSeen must be true after HITL tool")
	}
	// Another non-HITL tool fires after HITL — flag must stay set.
	b.toolHandler.OnToolStart(ctx, &callbacks.RunInfo{Name: "manage_tasks"}, &einotool.CallbackInput{ArgumentsInJSON: "{}"})
	if !b.HITLSeen() {
		t.Fatal("HITLSeen must remain true after HITL once set, even if non-HITL tools fire later")
	}
}
