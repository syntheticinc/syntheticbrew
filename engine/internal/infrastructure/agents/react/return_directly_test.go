package react

import (
	"context"
	"strings"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
)

// returnDirectlyTool is a stub tool that self-declares return-directly via
// ToolInfo.Extra — exactly the shape the MCP adapter produces for a tool carrying
// the `syntheticbrew.ai/return-directly` _meta flag.
type returnDirectlyTool struct {
	name string
	run  func(args string) string
}

func (s *returnDirectlyTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name:  s.name,
		Desc:  "return-directly stub tool",
		Extra: map[string]any{domain.ToolExtraReturnDirectly: true},
	}, nil
}

func (s *returnDirectlyTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...einotool.Option) (string, error) {
	return s.run(argumentsInJSON), nil
}

// runOwnedChar builds a real Agent and drives the non-streaming RunWithCallbacks
// (runOwned) path — the path whose final assistant message persists as an
// assistant_message row — capturing its event stream plus the returned answer.
func runOwnedChar(t *testing.T, model *mockChatModel, tools []einotool.BaseTool, cfg *config.AgentConfig) (*streamCapture, string) {
	t.Helper()
	a, err := NewAgent(context.Background(), AgentConfig{
		ChatModel:   model,
		Tools:       tools,
		MaxSteps:    cfg.MaxSteps,
		SessionID:   "rd-session",
		AgentConfig: cfg,
		ModelName:   "mock",
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	cap := &streamCapture{}
	answer, err := a.RunWithCallbacks(context.Background(), "go", cap.onEvent)
	if err != nil {
		t.Fatalf("RunWithCallbacks returned error: %v", err)
	}
	return cap, answer
}

// metaNarration is the spurious trailing message a reasoning model emits when a
// non-terminal "final answer" tool forces one more model turn. The fix must make
// the loop end before this is ever produced.
const metaNarration = "The recommendation has been generated. I will await the user's response."

// TestReturnDirectly_MCPDeclared_NoTrailingAssistantMessage is the core
// regression for the partner report: a tool that self-declares return-directly
// (via ToolInfo.Extra, the MCP `_meta` path) must end the turn right after it
// runs — no follow-up model call, no trailing assistant_message. The answer is
// delivered by the tool_result event, so the answer-event count is zero.
func TestReturnDirectly_MCPDeclared_NoTrailingAssistantMessage(t *testing.T) {
	tool := &returnDirectlyTool{name: "recommend_products", run: func(string) string {
		return `{"summary":"Вот рекомендации","cards":[1,2,3]}`
	}}
	modelCalls := 0
	model := historyChatModel(func(input []*schema.Message) *schema.Message {
		modelCalls++
		if countToolMsgs(input) == 0 {
			return charToolCall("c1", "recommend_products", `{"query":"x"}`)
		}
		// Reached only if the loop wrongly calls the model again after the tool.
		return charText(metaNarration)
	})

	cap, answer := runOwnedChar(t, model, []einotool.BaseTool{tool}, charAgentConfig(nil))
	t.Logf("typeSeq=%v modelCalls=%d answer=%q", cap.typeSeq(), modelCalls, answer)

	if got := cap.countType(domain.EventTypeToolCall); got != 1 {
		t.Errorf("expected one tool_call, got %d", got)
	}
	if got := cap.countType(domain.EventTypeToolResult); got != 1 {
		t.Errorf("expected one tool_result, got %d", got)
	}
	if got := cap.countType(domain.EventTypeAnswer); got != 0 {
		t.Errorf("return-directly turn must emit zero answer events (assistant_message=0), got %d (last=%q)", got, cap.lastAnswer())
	}
	if modelCalls != 1 {
		t.Errorf("model must be called once (the tool call), not again for meta-narration; got %d calls", modelCalls)
	}
	if strings.Contains(cap.lastAnswer(), "await the user") {
		t.Errorf("meta-narration leaked into the answer: %q", cap.lastAnswer())
	}
	if !strings.Contains(answer, "рекомендации") {
		t.Errorf("run answer should carry the tool output, got %q", answer)
	}
}

// TestReturnDirectly_Streaming proves the streaming path also stops after the
// return-directly tool: the model is called once and the meta-narration turn
// never runs.
func TestReturnDirectly_Streaming(t *testing.T) {
	tool := &returnDirectlyTool{name: "recommend_products", run: func(string) string {
		return `{"summary":"ok"}`
	}}
	modelCalls := 0
	model := historyChatModel(func(input []*schema.Message) *schema.Message {
		modelCalls++
		if countToolMsgs(input) == 0 {
			return charToolCall("c1", "recommend_products", `{"query":"x"}`)
		}
		return charText(metaNarration)
	})

	cap := runStreamOwned(t, model, []einotool.BaseTool{tool}, charAgentConfig(nil))
	t.Logf("typeSeq=%v modelCalls=%d lastAnswer=%q", cap.typeSeq(), modelCalls, cap.lastAnswer())

	if got := cap.countType(domain.EventTypeToolResult); got != 1 {
		t.Errorf("expected one tool_result, got %d", got)
	}
	if modelCalls != 1 {
		t.Errorf("model must be called once; got %d calls", modelCalls)
	}
	if strings.Contains(cap.lastAnswer(), "await the user") {
		t.Errorf("meta-narration leaked into the stream: %q", cap.lastAnswer())
	}
}

// TestReturnDirectly_OffByDefault guards that an ordinary tool (no return-directly
// declaration) is unaffected: the loop continues to a normal model answer turn.
func TestReturnDirectly_OffByDefault(t *testing.T) {
	tool := &charTool{name: "lookup", run: func(string) string { return `{"facts":["A"]}` }}
	modelCalls := 0
	model := historyChatModel(func(input []*schema.Message) *schema.Message {
		modelCalls++
		if countToolMsgs(input) == 0 {
			return charToolCall("c1", "lookup", `{"q":"x"}`)
		}
		return charText("Found A.")
	})

	cap, answer := runOwnedChar(t, model, []einotool.BaseTool{tool}, charAgentConfig(nil))
	t.Logf("typeSeq=%v modelCalls=%d answer=%q", cap.typeSeq(), modelCalls, answer)

	if modelCalls != 2 {
		t.Errorf("ordinary tool must let the loop run a normal answer turn (2 model calls), got %d", modelCalls)
	}
	if got := cap.countType(domain.EventTypeAnswer); got != 1 {
		t.Errorf("ordinary tool turn must emit one answer event, got %d", got)
	}
	if !strings.Contains(answer, "Found A") {
		t.Errorf("ordinary tool turn must end with the model answer, got %q", answer)
	}
}

// TestReturnDirectly_ConfigName proves the config-driven path still works and is
// unioned with self-declared tools: a tool named in AgentConfig.ToolReturnDirectly
// terminates the turn even though it does not self-declare via Extra.
func TestReturnDirectly_ConfigName(t *testing.T) {
	tool := &charTool{name: "final_answer", run: func(string) string { return `{"done":true}` }}
	modelCalls := 0
	model := historyChatModel(func(input []*schema.Message) *schema.Message {
		modelCalls++
		if countToolMsgs(input) == 0 {
			return charToolCall("c1", "final_answer", `{}`)
		}
		return charText(metaNarration)
	})
	cfg := charAgentConfig(func(c *config.AgentConfig) {
		c.ToolReturnDirectly = map[string]struct{}{"final_answer": {}}
	})

	cap, _ := runOwnedChar(t, model, []einotool.BaseTool{tool}, cfg)
	t.Logf("typeSeq=%v modelCalls=%d", cap.typeSeq(), modelCalls)

	if modelCalls != 1 {
		t.Errorf("config-named return-directly tool must end the turn after one model call, got %d", modelCalls)
	}
	if got := cap.countType(domain.EventTypeAnswer); got != 0 {
		t.Errorf("config-named return-directly turn must emit zero answer events, got %d", got)
	}
}
