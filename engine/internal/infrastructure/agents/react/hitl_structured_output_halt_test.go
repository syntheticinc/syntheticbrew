package react

import (
	"context"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// Regression for the partner report: the built-in HITL widget
// `show_structured_output` must halt the owned ReAct loop the moment it fires
// (one widget per turn). Before the fix the owned loop only halted via the
// return-directly map (config / ToolInfo.Extra), and the built-in widget — which
// relied on eino's react.SetReturnDirectly writing the now-absent *react.state —
// fell through, so the loop kept running and the model re-emitted the same widget
// many times per turn (up to 14× observed) or degraded to plain text.
//
// The stub is named exactly `show_structured_output` and declares NO Extra: it
// proves the loop now halts by routing the built-in HITL tool name through the
// owned-loop return-directly branch, not via any self-declaration.

// hitlWidgetTool mimics the built-in show_structured_output: it returns the
// success string the real tool returns and counts its executions, with no
// return-directly self-declaration.
type hitlWidgetTool struct {
	runs *int
}

func (s *hitlWidgetTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "show_structured_output", Desc: "HITL widget stub"}, nil
}

func (s *hitlWidgetTool) InvokableRun(_ context.Context, _ string, _ ...einotool.Option) (string, error) {
	*s.runs++
	return "Structured output displayed to user.", nil
}

// reEmitWidgetModel scripts a model that keeps re-emitting the widget on every
// turn — the looping behaviour observed in the field. With the halt working the
// model is reached exactly once (the first widget) and never asked again.
func reEmitWidgetModel(modelCalls *int) *mockChatModel {
	return historyChatModel(func(input []*schema.Message) *schema.Message {
		*modelCalls++
		// Re-emit the same widget every turn; a working halt stops this after one.
		return charToolCall("w0", "show_structured_output", `{"output_type":"info","title":"Q"}`)
	})
}

// TestHITL_StructuredOutput_HaltsOwnedLoop_Streaming is the core RED→GREEN: the
// streaming chat path must halt on the first widget — model called once, tool run
// once. Without the fix the loop runs until the step budget, re-emitting each turn.
func TestHITL_StructuredOutput_HaltsOwnedLoop_Streaming(t *testing.T) {
	runs := 0
	modelCalls := 0
	tool := &hitlWidgetTool{runs: &runs}

	cap := runStreamOwned(t, reEmitWidgetModel(&modelCalls), []einotool.BaseTool{tool}, charAgentConfig(nil))
	t.Logf("typeSeq=%v modelCalls=%d toolRuns=%d", cap.typeSeq(), modelCalls, runs)

	if modelCalls != 1 {
		t.Errorf("HITL widget must halt the loop after the first emit (one model call), got %d", modelCalls)
	}
	if runs != 1 {
		t.Errorf("widget must run exactly once per turn (halt-on-first), got %d runs (re-emit loop)", runs)
	}
	// The widget is the surface; prose on a HITL turn is suppressed.
	if got := cap.countType(domain.EventTypeAnswer); got != 0 {
		t.Errorf("HITL turn must emit zero answer events, got %d (last=%q)", got, cap.lastAnswer())
	}
}

// TestHITL_StructuredOutput_HaltsOwnedLoop_NonStreaming proves the same on the
// RunWithCallbacks (runOwned) path whose final message would persist as an
// assistant_message row: halt-on-first, no follow-up model call, no answer event.
func TestHITL_StructuredOutput_HaltsOwnedLoop_NonStreaming(t *testing.T) {
	runs := 0
	modelCalls := 0
	tool := &hitlWidgetTool{runs: &runs}

	cap, answer := runOwnedChar(t, reEmitWidgetModel(&modelCalls), []einotool.BaseTool{tool}, charAgentConfig(nil))
	t.Logf("typeSeq=%v modelCalls=%d toolRuns=%d answer=%q", cap.typeSeq(), modelCalls, runs, answer)

	if modelCalls != 1 {
		t.Errorf("HITL widget must halt the loop after one model call, got %d", modelCalls)
	}
	if runs != 1 {
		t.Errorf("widget must run exactly once per turn, got %d runs", runs)
	}
	if got := cap.countType(domain.EventTypeAnswer); got != 0 {
		t.Errorf("HITL turn must emit zero answer events (no duplicate assistant_message), got %d", got)
	}
	if answer != "" {
		t.Errorf("HITL turn must not surface the tool success string as the answer, got %q", answer)
	}
}

// TestHITL_StructuredOutput_HaltsOwnedLoop_ParallelToolCalls locks the
// halt-on-first invariant when the model emits show_structured_output ALONGSIDE
// another tool in the SAME assistant step (parallel tool calls). The turn must
// still halt after one model call regardless of which position the widget takes;
// without the fix the widget is not in the return-directly set and the loop runs on.
func TestHITL_StructuredOutput_HaltsOwnedLoop_ParallelToolCalls(t *testing.T) {
	cases := []struct {
		name  string
		first bool // widget is the first of the two parallel calls
	}{
		{"widget_first", true},
		{"widget_second", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			widgetRuns := 0
			modelCalls := 0
			widget := &hitlWidgetTool{runs: &widgetRuns}
			other := &charTool{name: "lookup", run: func(string) string { return `{"facts":["A"]}` }}

			model := historyChatModel(func(input []*schema.Message) *schema.Message {
				modelCalls++
				widgetCall := []any{"w0", "show_structured_output", `{"output_type":"info","title":"Q"}`}
				otherCall := []any{"t0", "lookup", `{"q":"x"}`}
				a, b := widgetCall, otherCall
				if !tc.first {
					a, b = otherCall, widgetCall
				}
				return charToolCall2(
					a[0].(string), a[1].(string), a[2].(string),
					b[0].(string), b[1].(string), b[2].(string))
			})

			cap := runStreamOwned(t, model, []einotool.BaseTool{widget, other}, charAgentConfig(nil))
			t.Logf("typeSeq=%v modelCalls=%d widgetRuns=%d", cap.typeSeq(), modelCalls, widgetRuns)

			if modelCalls != 1 {
				t.Errorf("a parallel step containing the widget must halt after one model call, got %d", modelCalls)
			}
			if widgetRuns != 1 {
				t.Errorf("widget must run exactly once, got %d", widgetRuns)
			}
			if got := cap.countType(domain.EventTypeAnswer); got != 0 {
				t.Errorf("HITL turn must emit zero answer events, got %d", got)
			}
		})
	}
}

// TestOwnedReturnDirectlyMap_IncludesBuiltinHITL locks the invariant directly: the
// built-in HITL tools are always return-directly, independent of agent config or
// any ToolInfo.Extra self-declaration. This is the seam that ties the HITL
// classification to the loop-halt routing.
func TestOwnedReturnDirectlyMap_IncludesBuiltinHITL(t *testing.T) {
	m := ownedReturnDirectlyMap(nil, nil)
	for _, name := range domain.HITLToolNames() {
		if _, ok := m[name]; !ok {
			t.Errorf("ownedReturnDirectlyMap must include built-in HITL tool %q (loop-halt seam), got map=%v", name, m)
		}
	}
}
