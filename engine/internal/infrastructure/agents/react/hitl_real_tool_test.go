package react

import (
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools"
)

// captureEmitter records the events the real StructuredOutputTool emits, so the
// test can count interrupt_request envelopes — the partner report's acceptance
// metric ("1 widget per turn, loop halts").
type captureEmitter struct{ events []*domain.AgentEvent }

func (e *captureEmitter) Send(ev *domain.AgentEvent) error {
	e.events = append(e.events, ev)
	return nil
}

// TestHITL_RealStructuredOutputTool_OneInterruptPerTurn drives the REAL built-in
// show_structured_output tool (not a stub) through the owned loop with a model
// that would re-emit the widget every turn if the loop kept running. It asserts
// the report's literal acceptance: exactly one interrupt_request event per turn,
// because the loop halts on the first widget. Before the fix the loop ran to the
// step budget and the tool emitted one interrupt_request per re-emit.
func TestHITL_RealStructuredOutputTool_OneInterruptPerTurn(t *testing.T) {
	emitter := &captureEmitter{}
	realTool := tools.NewStructuredOutputTool(emitter, "hitl-sess")

	modelCalls := 0
	model := historyChatModel(func(input []*schema.Message) *schema.Message {
		modelCalls++
		return charToolCall("w0", "show_structured_output",
			`{"output_type":"info","title":"Pick a region","description":"Which region?"}`)
	})

	runStreamOwned(t, model, []einotool.BaseTool{realTool}, charAgentConfig(nil))

	interrupts := 0
	for _, ev := range emitter.events {
		if ev.Type == domain.EventTypeInterruptRequest {
			interrupts++
		}
	}
	t.Logf("modelCalls=%d interrupt_request=%d", modelCalls, interrupts)

	if interrupts != 1 {
		t.Errorf("real show_structured_output must emit exactly one interrupt_request per turn (loop halts on first widget), got %d", interrupts)
	}
	if modelCalls != 1 {
		t.Errorf("loop must halt after the first widget (one model call), got %d", modelCalls)
	}
}
