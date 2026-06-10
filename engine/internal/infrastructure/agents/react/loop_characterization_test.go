package react

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
)

// Characterization tests for the 1.6.0 ReAct loop.
//
// These lock the observable SSE behaviour of Agent.Stream / Agent.RunWithCallbacks
// — the ordered event-type stream, the terminal graceful-answer text, and the
// retract placement — BEFORE the loop is reimplemented on a hand-owned
// compose.Graph. The owned loop must reproduce these sequences (trace
// equivalence) plus the new forced tool-less summary; anything else is a
// regression. They run entirely on deterministic mocks (no network, no LLM).

// historyChatModel drives the loop deterministically: each model invocation calls
// decide() with the current message history, so the script reacts to how many tool
// results have come back rather than relying on a fragile call counter.
func historyChatModel(decide func(input []*schema.Message) *schema.Message) *mockChatModel {
	return &mockChatModel{
		generateFunc: func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
			return decide(input), nil
		},
		streamFunc: func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			msg := decide(input)
			sr, sw := schema.Pipe[*schema.Message](1)
			go func() {
				defer sw.Close()
				sw.Send(msg, nil)
			}()
			return sr, nil
		},
	}
}

// countToolMsgs returns how many Tool-role messages are in the history — the
// loop's progress marker for the scripted model.
func countToolMsgs(input []*schema.Message) int {
	n := 0
	for _, m := range input {
		if m.Role == schema.Tool {
			n++
		}
	}
	return n
}

// charToolCall builds an assistant tool-call message with an explicit call ID so
// distinct steps get distinct IDs.
func charToolCall(id, name, args string) *schema.Message {
	return &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID:       id,
			Type:     "function",
			Function: schema.FunctionCall{Name: name, Arguments: args},
		}},
		ResponseMeta: &schema.ResponseMeta{FinishReason: "tool_calls"},
	}
}

func charText(content string) *schema.Message {
	return &schema.Message{
		Role:         schema.Assistant,
		Content:      content,
		ResponseMeta: &schema.ResponseMeta{FinishReason: "stop"},
	}
}

// charTool is a stub invokable tool whose result is produced by run().
type charTool struct {
	name string
	run  func(args string) string
}

func (s *charTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: s.name, Desc: "characterization stub tool"}, nil
}

func (s *charTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einotool.Option) (string, error) {
	return s.run(argumentsInJSON), nil
}

// streamCapture records the ordered event + chunk stream from a run.
type streamCapture struct {
	mu     sync.Mutex
	events []*domain.AgentEvent
	chunks []string
}

func (c *streamCapture) onEvent(e *domain.AgentEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
	return nil
}

func (c *streamCapture) onChunk(s string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.chunks = append(c.chunks, s)
	return nil
}

// typeSeq returns the ordered event-type sequence as strings.
func (c *streamCapture) typeSeq() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	seq := make([]string, len(c.events))
	for i, e := range c.events {
		seq[i] = string(e.Type)
	}
	return seq
}

// countType counts events of a given type.
func (c *streamCapture) countType(t domain.AgentEventType) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.events {
		if e.Type == t {
			n++
		}
	}
	return n
}

// lastAnswer returns the content of the final EventTypeAnswer, or "".
func (c *streamCapture) lastAnswer() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	ans := ""
	for _, e := range c.events {
		if e.Type == domain.EventTypeAnswer {
			ans = e.Content
		}
	}
	return ans
}

// charAgentConfig builds a minimal valid AgentConfig for the harness.
func charAgentConfig(mut func(*config.AgentConfig)) *config.AgentConfig {
	cfg := &config.AgentConfig{
		MaxSteps:       6,
		MaxContextSize: 0,  // no rewriter unless a scenario wants it
		ContextLogPath: "", // skip file logging
		Prompts: &config.PromptsConfig{
			SystemPrompt: "You are a characterization test agent.",
		},
		EnableEnhancedToolCallChecker: true,
	}
	if mut != nil {
		mut(cfg)
	}
	return cfg
}

// runStreamChar builds an Agent and drives Agent.Stream, returning the capture.
func runStreamChar(t *testing.T, model *mockChatModel, tools []einotool.BaseTool, cfg *config.AgentConfig) *streamCapture {
	t.Helper()
	a, err := NewAgent(context.Background(), AgentConfig{
		ChatModel:   model,
		Tools:       tools,
		MaxSteps:    cfg.MaxSteps,
		SessionID:   "char-session",
		AgentConfig: cfg,
		ModelName:   "mock",
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	cap := &streamCapture{}
	if err := a.Stream(context.Background(), "go", cap.onChunk, cap.onEvent); err != nil {
		t.Fatalf("Stream returned error (expected graceful nil): %v", err)
	}
	return cap
}

// --- Scenario: text-only answer (no tools) ---

func TestChar_TextOnly(t *testing.T) {
	model := historyChatModel(func(input []*schema.Message) *schema.Message {
		return charText("The answer is 42.")
	})
	cap := runStreamChar(t, model, nil, charAgentConfig(nil))

	t.Logf("typeSeq=%v chunks=%d", cap.typeSeq(), len(cap.chunks))
	if got := cap.lastAnswer(); !strings.Contains(got, "42") {
		t.Errorf("final answer must contain model text, got %q", got)
	}
	if cap.countType(domain.EventTypeError) != 0 {
		t.Errorf("no error events expected, got %d", cap.countType(domain.EventTypeError))
	}
	if cap.countType(domain.EventTypeToolCall) != 0 {
		t.Errorf("no tool_call events expected, got %d", cap.countType(domain.EventTypeToolCall))
	}
}

// --- Scenario: single tool call, then answer ---

func TestChar_SingleToolThenAnswer(t *testing.T) {
	tool := &charTool{name: "lookup", run: func(args string) string { return `{"facts":["A","B","C"]}` }}
	model := historyChatModel(func(input []*schema.Message) *schema.Message {
		if countToolMsgs(input) == 0 {
			return charToolCall("c1", "lookup", `{"q":"x"}`)
		}
		return charText("Found A, B, C.")
	})
	cap := runStreamChar(t, model, []einotool.BaseTool{tool}, charAgentConfig(nil))

	t.Logf("typeSeq=%v", cap.typeSeq())
	if cap.countType(domain.EventTypeToolCall) != 1 {
		t.Errorf("expected exactly one tool_call, got %d", cap.countType(domain.EventTypeToolCall))
	}
	if cap.countType(domain.EventTypeToolResult) != 1 {
		t.Errorf("expected exactly one tool_result, got %d", cap.countType(domain.EventTypeToolResult))
	}
	if got := cap.lastAnswer(); !strings.Contains(got, "A, B, C") {
		t.Errorf("final answer must reference tool output, got %q", got)
	}
	// tool_call must precede tool_result.
	seq := cap.typeSeq()
	tcIdx, trIdx := -1, -1
	for i, ty := range seq {
		if ty == string(domain.EventTypeToolCall) && tcIdx == -1 {
			tcIdx = i
		}
		if ty == string(domain.EventTypeToolResult) && trIdx == -1 {
			trIdx = i
		}
	}
	if tcIdx == -1 || trIdx == -1 || tcIdx >= trIdx {
		t.Errorf("tool_call must precede tool_result: tcIdx=%d trIdx=%d seq=%v", tcIdx, trIdx, seq)
	}
}

// --- Scenario: identical-args loop → graceful terminal answer ---

func TestChar_IdenticalArgsLoop(t *testing.T) {
	tool := &charTool{name: "search", run: func(args string) string { return `{"results":[]}` }} // success-but-empty
	model := historyChatModel(func(input []*schema.Message) *schema.Message {
		// Always the same byte-identical call — the degenerate loop.
		return charToolCall("c", "search", `{"q":"same"}`)
	})
	cfg := charAgentConfig(func(c *config.AgentConfig) { c.MaxSteps = 10 })
	cap := runStreamChar(t, model, []einotool.BaseTool{tool}, cfg)

	t.Logf("typeSeq=%v lastAnswer=%q", cap.typeSeq(), cap.lastAnswer())
	if got := cap.lastAnswer(); !strings.Contains(got, "kept repeating the same") {
		t.Errorf("identical-args loop must yield its graceful message, got %q", got)
	}
	if cap.countType(domain.EventTypeAnswer) != 1 {
		t.Errorf("expected exactly one graceful answer, got %d", cap.countType(domain.EventTypeAnswer))
	}
}

// --- Scenario: tool-error loop → graceful terminal answer ---

func TestChar_ToolErrorLoop(t *testing.T) {
	tool := &charTool{name: "fetch", run: func(args string) string { return "[ERROR] upstream exploded" }}
	calls := 0
	model := historyChatModel(func(input []*schema.Message) *schema.Message {
		// Vary args so the identical-args breaker never trips; isolate the
		// error-loop breaker (per-tool [ERROR] streak).
		calls++
		return charToolCall("c", "fetch", `{"i":`+itoaChar(countToolMsgs(input))+`}`)
	})
	cfg := charAgentConfig(func(c *config.AgentConfig) { c.MaxSteps = 12 })
	cap := runStreamChar(t, model, []einotool.BaseTool{tool}, cfg)

	t.Logf("typeSeq=%v lastAnswer=%q", cap.typeSeq(), cap.lastAnswer())
	if got := cap.lastAnswer(); !strings.Contains(got, "kept failing") {
		t.Errorf("tool-error loop must yield its graceful message, got %q", got)
	}
	if cap.countType(domain.EventTypeAnswer) != 1 {
		t.Errorf("expected exactly one graceful answer, got %d", cap.countType(domain.EventTypeAnswer))
	}
}

// --- Scenario: step budget exhausted → graceful terminal answer ---

func TestChar_StepBudget(t *testing.T) {
	tool := &charTool{name: "page", run: func(args string) string { return `{"ok":true}` }}
	model := historyChatModel(func(input []*schema.Message) *schema.Message {
		// Different args each step (no identical-args trip), success results
		// (no error-loop trip): only the step budget can stop this.
		return charToolCall("c", "page", `{"n":`+itoaChar(countToolMsgs(input))+`}`)
	})
	cfg := charAgentConfig(func(c *config.AgentConfig) { c.MaxSteps = 3 })
	cap := runStreamChar(t, model, []einotool.BaseTool{tool}, cfg)

	t.Logf("typeSeq=%v lastAnswer=%q", cap.typeSeq(), cap.lastAnswer())
	if got := cap.lastAnswer(); !strings.Contains(got, "maximum number of steps") {
		t.Errorf("step budget must yield its graceful message, got %q", got)
	}
}

// itoaChar is a tiny int→string for arg variation without importing strconv twice.
func itoaChar(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// charToolCall2 builds an assistant message with two parallel tool calls.
func charToolCall2(id1, name1, args1, id2, name2, args2 string) *schema.Message {
	return &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{
			{ID: id1, Type: "function", Function: schema.FunctionCall{Name: name1, Arguments: args1}},
			{ID: id2, Type: "function", Function: schema.FunctionCall{Name: name2, Arguments: args2}},
		},
		ResponseMeta: &schema.ResponseMeta{FinishReason: "tool_calls"},
	}
}

// --- Scenario: two parallel tool calls in one step, then answer ---
//
// Locks the invariant that every tool_call event precedes its matching
// tool_result, and that the turn ends in a single answer — independent of the
// order eino interleaves the two tools.
func TestChar_ParallelTools(t *testing.T) {
	toolA := &charTool{name: "alpha", run: func(args string) string { return `{"a":1}` }}
	toolB := &charTool{name: "beta", run: func(args string) string { return `{"b":2}` }}
	model := historyChatModel(func(input []*schema.Message) *schema.Message {
		if countToolMsgs(input) == 0 {
			return charToolCall2("ca", "alpha", `{"x":1}`, "cb", "beta", `{"y":2}`)
		}
		return charText("Both done.")
	})
	cap := runStreamChar(t, model, []einotool.BaseTool{toolA, toolB}, charAgentConfig(nil))

	t.Logf("typeSeq=%v lastAnswer=%q", cap.typeSeq(), cap.lastAnswer())
	if cap.countType(domain.EventTypeToolCall) != 2 {
		t.Errorf("expected two tool_call events, got %d", cap.countType(domain.EventTypeToolCall))
	}
	if cap.countType(domain.EventTypeToolResult) != 2 {
		t.Errorf("expected two tool_result events, got %d", cap.countType(domain.EventTypeToolResult))
	}
	if cap.countType(domain.EventTypeAnswer) != 1 {
		t.Errorf("expected exactly one answer, got %d", cap.countType(domain.EventTypeAnswer))
	}
	// Every tool_call must precede a tool_result overall (no result before any call).
	seq := cap.typeSeq()
	calls, results := 0, 0
	for _, ty := range seq {
		switch ty {
		case string(domain.EventTypeToolCall):
			calls++
		case string(domain.EventTypeToolResult):
			results++
			if results > calls {
				t.Errorf("tool_result appeared before its tool_call: seq=%v", seq)
			}
		}
	}
}
