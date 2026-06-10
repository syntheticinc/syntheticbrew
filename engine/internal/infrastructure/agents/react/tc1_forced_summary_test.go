package react

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
)

// toolAwareMock is the partner's TC-1 model: it emits a `lookup` tool call when
// tools ARE bound, and plain text when NONE are bound. Because tools are removed
// structurally at the finalize node (not via a prose directive), this proves the
// engine stripped the tools for the final call — the model literally cannot call
// a tool there, so it must summarise.
type toolAwareMock struct {
	hasTools bool
	facts    string // text to emit (referencing gathered facts) when tool-less
}

func (m *toolAwareMock) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	cp := *m
	cp.hasTools = len(tools) > 0
	return &cp, nil
}

func (m *toolAwareMock) BindTools(tools []*schema.ToolInfo) error { return nil }
func (m *toolAwareMock) GetType() string                          { return "tool-aware-mock" }
func (m *toolAwareMock) IsCallbacksEnabled() bool                 { return false }

func (m *toolAwareMock) reply() *schema.Message {
	if m.hasTools {
		return charToolCall("c", "lookup", `{"q":"x"}`)
	}
	return charText("Based on what I gathered: facts " + m.facts + ".")
}

func (m *toolAwareMock) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return m.reply(), nil
}

func (m *toolAwareMock) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg := m.reply()
	sr, sw := schema.Pipe[*schema.Message](1)
	go func() { defer sw.Close(); sw.Send(msg, nil) }()
	return sr, nil
}

// TestTC1_BudgetWallForcesToolLessSummary is the partner's TC-1 (deterministic):
// the model keeps calling a tool until the step budget, then the engine forces a
// tool-less finalize call. PASS criteria (all required):
//   - exactly one answer event, no error;
//   - the answer references the gathered facts (A/B/C) → the tool-less call ran;
//   - the answer is NOT the hardcoded budget apology.
func TestTC1_BudgetWallForcesToolLessSummary(t *testing.T) {
	lookup := &charTool{name: "lookup", run: func(args string) string { return `{"facts":["A","B","C"]}` }}
	mock := &toolAwareMock{facts: "A, B, C"}

	cfg := &config.AgentConfig{
		MaxSteps:                      6,
		MaxTurnDuration:               0,
		MaxStepDuration:               0,
		Prompts:                       &config.PromptsConfig{SystemPrompt: "Gather facts and summarize."},
		EnableEnhancedToolCallChecker: true,
	}
	a, err := NewAgent(context.Background(), AgentConfig{
		ChatModel:   mock,
		Tools:       []einotool.BaseTool{lookup},
		MaxSteps:    cfg.MaxSteps,
		SessionID:   "tc1",
		AgentConfig: cfg,
		ModelName:   "tool-aware-mock",
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	cap := &streamCapture{}
	if err := a.streamOwned(context.Background(), "Gather facts and summarize.", cap.onChunk, cap.onEvent); err != nil {
		t.Fatalf("streamOwned: %v", err)
	}

	t.Logf("typeSeq=%v lastAnswer=%q", cap.typeSeq(), cap.lastAnswer())

	if cap.countType(domain.EventTypeError) != 0 {
		t.Errorf("TC-1: no error event allowed, got %d", cap.countType(domain.EventTypeError))
	}
	if cap.countType(domain.EventTypeAnswer) != 1 {
		t.Errorf("TC-1: expected exactly one answer, got %d", cap.countType(domain.EventTypeAnswer))
	}
	ans := cap.lastAnswer()
	// #3: references the gathered facts (proves the forced tool-less call ran).
	for _, f := range []string{"A", "B", "C"} {
		if !strings.Contains(ans, f) {
			t.Errorf("TC-1: final answer must reference gathered fact %q (forced tool-less summary), got %q", f, ans)
		}
	}
	// #4: NOT the hardcoded apology.
	if strings.Contains(ans, "maximum number of steps") || strings.Contains(ans, "spent the time available") {
		t.Errorf("TC-1: final answer must be a model summary, not the hardcoded apology, got %q", ans)
	}
}
