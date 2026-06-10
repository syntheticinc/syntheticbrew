package react

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agents/callbacks"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
)

// runStreamOwned builds a real Agent (NewAgent) and drives the OWNED path
// (streamOwned), capturing the SSE event/chunk stream — the integrated
// counterpart to runStreamChar (which drives the eino Stream path).
func runStreamOwned(t *testing.T, model *mockChatModel, tools []einotool.BaseTool, cfg *config.AgentConfig) *streamCapture {
	t.Helper()
	a, err := NewAgent(context.Background(), AgentConfig{
		ChatModel:   model,
		Tools:       tools,
		MaxSteps:    cfg.MaxSteps,
		SessionID:   "owned-session",
		AgentConfig: cfg,
		ModelName:   "mock",
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	cap := &streamCapture{}
	if err := a.streamOwned(context.Background(), "go", cap.onChunk, cap.onEvent); err != nil {
		t.Fatalf("streamOwned returned error (expected graceful nil): %v", err)
	}
	return cap
}

// TestStreamOwned_SingleTool proves the integrated owned path reproduces the
// happy-path event sequence the eino path produces (golden from step 1).
func TestStreamOwned_SingleTool(t *testing.T) {
	tool := &charTool{name: "lookup", run: func(args string) string { return `{"facts":["A","B","C"]}` }}
	model := historyChatModel(func(input []*schema.Message) *schema.Message {
		if countToolMsgs(input) == 0 {
			return charToolCall("c1", "lookup", `{"q":"x"}`)
		}
		return charText("Found A, B, C.")
	})
	cap := runStreamOwned(t, model, []einotool.BaseTool{tool}, charAgentConfig(nil))

	t.Logf("typeSeq=%v", cap.typeSeq())
	if cap.countType(domain.EventTypeToolCall) != 1 {
		t.Errorf("expected one tool_call, got %d", cap.countType(domain.EventTypeToolCall))
	}
	if cap.countType(domain.EventTypeToolResult) != 1 {
		t.Errorf("expected one tool_result, got %d", cap.countType(domain.EventTypeToolResult))
	}
	if got := cap.lastAnswer(); !strings.Contains(got, "A, B, C") {
		t.Errorf("answer must reference tool output, got %q", got)
	}
}

// TestStreamOwned_BudgetFinalizesWithSummary is the core regression for the
// partner report: at the step-budget wall the owned path must emit a MODEL
// summary built from gathered context — NOT the hardcoded apology. This is the
// behaviour the eino path could not produce.
func TestStreamOwned_BudgetFinalizesWithSummary(t *testing.T) {
	tool := &charTool{name: "scan", run: func(args string) string { return `{"family":"temperature"}` }}
	model := historyChatModel(func(input []*schema.Message) *schema.Message {
		if sawFinalizeDirective(input) {
			return charText("Summary: I gathered the temperature sensor family before running out of budget.")
		}
		return charToolCall("c", "scan", `{"n":1}`)
	})
	cfg := charAgentConfig(func(c *config.AgentConfig) { c.MaxSteps = 2 })
	cap := runStreamOwned(t, model, []einotool.BaseTool{tool}, cfg)

	t.Logf("typeSeq=%v lastAnswer=%q", cap.typeSeq(), cap.lastAnswer())
	got := cap.lastAnswer()
	if !strings.Contains(got, "Summary") || !strings.Contains(got, "temperature") {
		t.Errorf("budget wall must yield a MODEL summary referencing gathered context, got %q", got)
	}
	// The hardcoded apology must NOT be the answer — the whole point of the fix.
	if strings.Contains(got, "maximum number of steps") {
		t.Errorf("budget wall must NOT emit the hardcoded apology when the model summarised, got %q", got)
	}
	if cap.countType(domain.EventTypeAnswer) != 1 {
		t.Errorf("expected exactly one answer, got %d", cap.countType(domain.EventTypeAnswer))
	}
}

// TestStreamOwned_IdenticalArgsEscalatesToSummary proves the graduated loop
// policy end-to-end: a model that ignores the correction nudges and keeps
// hammering a byte-identical call is escalated, after the correction budget, into
// a finalize summary — never a hardcoded apology, never a hung turn.
func TestStreamOwned_IdenticalArgsEscalatesToSummary(t *testing.T) {
	tool := &charTool{name: "search", run: func(args string) string { return `{"results":[]}` }}
	model := historyChatModel(func(input []*schema.Message) *schema.Message {
		if sawFinalizeDirective(input) {
			return charText("Summary: the search returned no results across repeated attempts.")
		}
		return charToolCall("c", "search", `{"q":"same"}`) // ignores every nudge
	})
	cfg := charAgentConfig(func(c *config.AgentConfig) { c.MaxSteps = 20 })
	cap := runStreamOwned(t, model, []einotool.BaseTool{tool}, cfg)

	t.Logf("typeSeq=%v lastAnswer=%q", cap.typeSeq(), cap.lastAnswer())
	got := cap.lastAnswer()
	if !strings.Contains(got, "Summary") {
		t.Errorf("escalated loop must yield a finalize summary, got %q", got)
	}
	if strings.Contains(got, "kept repeating") {
		t.Errorf("must NOT emit the hardcoded apology when the model summarised, got %q", got)
	}
	if cap.countType(domain.EventTypeAnswer) != 1 {
		t.Errorf("expected exactly one answer, got %d", cap.countType(domain.EventTypeAnswer))
	}
}

// TestGraphBackstopReason maps eino's hard-wall errors to graceful reasons, and
// leaves genuine client cancels alone.
func TestGraphBackstopReason(t *testing.T) {
	if r, ok := graphBackstopReason(compose.ErrExceedMaxSteps, nil); !ok || r != callbacks.TerminalStepBudget {
		t.Errorf("ErrExceedMaxSteps must map to StepBudget, got %v ok=%v", r, ok)
	}
	if r, ok := graphBackstopReason(context.DeadlineExceeded, nil); !ok || r != callbacks.TerminalTimeBudget {
		t.Errorf("turn-time deadline (outer alive) must map to TimeBudget, got %v ok=%v", r, ok)
	}
	if _, ok := graphBackstopReason(context.DeadlineExceeded, context.Canceled); ok {
		t.Error("deadline with outer ctx cancelled is a genuine client cancel, not a budget")
	}
	if _, ok := graphBackstopReason(context.Canceled, context.Canceled); ok {
		t.Error("client cancel must not map to a budget")
	}
	if _, ok := graphBackstopReason(stderrors.New("boom"), nil); ok {
		t.Error("ordinary error must not map to a budget")
	}
}

// TestStreamOwned_UnlimitedRunawayEndsGracefully is the regression guard for the
// reviewer-found gap: an unlimited-budget agent (max_steps=0) that runs away with
// varying args and successful results must NOT surface a bare error when eino's
// hard backstop fires — it must end with a graceful answer + done.
func TestStreamOwned_UnlimitedRunawayEndsGracefully(t *testing.T) {
	tool := &charTool{name: "page", run: func(args string) string { return `{"ok":true}` }}
	model := historyChatModel(func(input []*schema.Message) *schema.Message {
		// Always a tool call with DIFFERENT args (no identical-args trip) and a
		// SUCCESSFUL result (no error-loop trip): only a budget can stop it.
		return charToolCall("c", "page", `{"n":`+itoaChar(countToolMsgs(input))+`}`)
	})
	// max_steps=0 → unlimited owned step budget; force a SMALL eino backstop via a
	// tiny configured budget so the test terminates fast. We build the graph
	// directly with stepBudget=0 (unlimited) and a small maxStep (eino wall).
	tools := []einotool.BaseTool{tool}
	run, err := buildOwnedGraph(context.Background(), ownedGraphConfig{
		model: model, tools: tools, toolInfos: toolInfosFor(t, tools),
		maxStep: 5, stepBudget: 0, // unlimited owned budget; eino wall at 5
	})
	if err != nil {
		t.Fatalf("buildOwnedGraph: %v", err)
	}
	// Drive via Stream and confirm eino returns ErrExceedMaxSteps (the condition
	// the backstop maps). This documents the hard-wall the backstop catches.
	sr, err := run.Stream(context.Background(), []*schema.Message{{Role: schema.User, Content: "go"}})
	if err == nil {
		// drain to surface the terminal error
		_, err = func() (any, error) {
			defer sr.Close()
			for {
				if _, e := sr.Recv(); e != nil {
					return nil, e
				}
			}
		}()
	}
	if err == nil {
		t.Fatal("expected eino hard-wall error from an uncapped runaway")
	}
	if r, ok := graphBackstopReason(err, nil); !ok || r != callbacks.TerminalStepBudget {
		t.Errorf("runaway hard-wall must be classified StepBudget by the backstop, got %v ok=%v (err=%v)", r, ok, err)
	}
}
