package react

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agents"
)

// toolInfosFor gathers ToolInfo from a set of tools for graph construction.
func toolInfosFor(t *testing.T, tools []einotool.BaseTool) []*schema.ToolInfo {
	t.Helper()
	var infos []*schema.ToolInfo
	for _, tl := range tools {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatalf("tool Info: %v", err)
		}
		infos = append(infos, info)
	}
	return infos
}

// drainOwnedStream collects the final assistant content from the owned graph's
// stream output (the last non-empty content wins, as the model emits one message).
func drainOwnedStream(t *testing.T, sr *schema.StreamReader[*schema.Message]) string {
	t.Helper()
	defer sr.Close()
	var content string
	for {
		msg, err := sr.Recv()
		if err == io.EOF {
			return content
		}
		if err != nil {
			t.Fatalf("stream recv: %v", err)
		}
		if msg != nil && msg.Content != "" {
			content = msg.Content
		}
	}
}

// TestOwnedGraph_TextOnly proves the skeleton graph compiles and runs: a model
// that answers immediately produces its text with no tool calls.
func TestOwnedGraph_TextOnly(t *testing.T) {
	model := historyChatModel(func(input []*schema.Message) *schema.Message {
		return charText("The answer is 42.")
	})
	run, err := buildOwnedGraph(context.Background(), ownedGraphConfig{
		model:   model,
		maxStep: 6,
	})
	if err != nil {
		t.Fatalf("buildOwnedGraph: %v", err)
	}
	out, err := run.Invoke(context.Background(), []*schema.Message{{Role: schema.User, Content: "go"}})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !strings.Contains(out.Content, "42") {
		t.Errorf("expected model answer, got %q", out.Content)
	}
}

// TestOwnedGraph_SingleToolThenAnswer proves the chat⇄tools cycle works: the
// model calls a tool, the tools node executes it, and the next model call
// answers from the result.
func TestOwnedGraph_SingleToolThenAnswer(t *testing.T) {
	tool := &charTool{name: "lookup", run: func(args string) string { return `{"facts":["A","B","C"]}` }}
	tools := []einotool.BaseTool{tool}
	model := historyChatModel(func(input []*schema.Message) *schema.Message {
		if countToolMsgs(input) == 0 {
			return charToolCall("c1", "lookup", `{"q":"x"}`)
		}
		return charText("Found A, B, C.")
	})
	run, err := buildOwnedGraph(context.Background(), ownedGraphConfig{
		model:     model,
		tools:     tools,
		toolInfos: toolInfosFor(t, tools),
		maxStep:   6,
	})
	if err != nil {
		t.Fatalf("buildOwnedGraph: %v", err)
	}
	out, err := run.Invoke(context.Background(), []*schema.Message{{Role: schema.User, Content: "go"}})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !strings.Contains(out.Content, "A, B, C") {
		t.Errorf("expected answer referencing tool output, got %q", out.Content)
	}
}

// TestOwnedGraph_StreamSingleTool proves the streaming entry point drains a full
// chat⇄tools cycle end-to-end.
func TestOwnedGraph_StreamSingleTool(t *testing.T) {
	tool := &charTool{name: "lookup", run: func(args string) string { return `{"facts":["X"]}` }}
	tools := []einotool.BaseTool{tool}
	model := historyChatModel(func(input []*schema.Message) *schema.Message {
		if countToolMsgs(input) == 0 {
			return charToolCall("c1", "lookup", `{"q":"x"}`)
		}
		return charText("Done with X.")
	})
	run, err := buildOwnedGraph(context.Background(), ownedGraphConfig{
		model:     model,
		tools:     tools,
		toolInfos: toolInfosFor(t, tools),
		maxStep:   6,
	})
	if err != nil {
		t.Fatalf("buildOwnedGraph: %v", err)
	}
	sr, err := run.Stream(context.Background(), []*schema.Message{{Role: schema.User, Content: "go"}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	got := drainOwnedStream(t, sr)
	if !strings.Contains(got, "X") {
		t.Errorf("expected streamed answer about X, got %q", got)
	}
}

// sawFinalizeDirective reports whether the finalize directive was injected into
// the model input — i.e. the model is on the tool-less finalize call.
func sawFinalizeDirective(input []*schema.Message) bool {
	for _, m := range input {
		if m.Role == schema.System && strings.Contains(m.Content, "FINAL ANSWER REQUIRED NOW") {
			return true
		}
	}
	return false
}

// TestOwnedGraph_StepBudgetFinalizes proves the step-budget wall routes to the
// finalize node: a model that never stops calling a tool is diverted, after the
// budget, into a tool-less call that summarises.
func TestOwnedGraph_StepBudgetFinalizes(t *testing.T) {
	var toolRuns int
	tool := &charTool{name: "poll", run: func(args string) string { toolRuns++; return `{"status":"pending"}` }}
	tools := []einotool.BaseTool{tool}
	model := historyChatModel(func(input []*schema.Message) *schema.Message {
		if sawFinalizeDirective(input) {
			return charText("Summary: status still pending after checks.")
		}
		return charToolCall("c", "poll", `{"id":1}`)
	})
	run, err := buildOwnedGraph(context.Background(), ownedGraphConfig{
		model:      model,
		tools:      tools,
		toolInfos:  toolInfosFor(t, tools),
		maxStep:    50,
		stepBudget: 2,
	})
	if err != nil {
		t.Fatalf("buildOwnedGraph: %v", err)
	}
	out, err := run.Invoke(context.Background(), []*schema.Message{{Role: schema.User, Content: "go"}})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !strings.Contains(out.Content, "Summary") {
		t.Errorf("expected finalize summary, got %q", out.Content)
	}
	if toolRuns != 2 {
		t.Errorf("expected exactly stepBudget=2 tool runs before finalize, got %d", toolRuns)
	}
}

// TestOwnedGraph_UnknownToolHandledGracefully proves the owned graph wires
// UnknownToolsHandler: a hallucinated tool call returns a graceful [ERROR] result
// (not a hard failure), preserving conversation structure so the model recovers.
func TestOwnedGraph_UnknownToolHandledGracefully(t *testing.T) {
	realTool := &charTool{name: "lookup", run: func(args string) string { return `{"ok":true}` }}
	tools := []einotool.BaseTool{realTool}
	model := historyChatModel(func(input []*schema.Message) *schema.Message {
		if countToolMsgs(input) == 0 {
			return charToolCall("c1", "ghost_tool", `{}`) // not in the tool set
		}
		return charText("Recovered after the unknown tool.")
	})
	run, err := buildOwnedGraph(context.Background(), ownedGraphConfig{
		model:     model,
		tools:     tools,
		toolInfos: toolInfosFor(t, tools),
		maxStep:   6,
		unknownToolsHandler: func(ctx context.Context, name, input string) (string, error) {
			return "[ERROR] Tool '" + name + "' does not exist.", nil
		},
	})
	if err != nil {
		t.Fatalf("buildOwnedGraph: %v", err)
	}
	out, err := run.Invoke(context.Background(), []*schema.Message{{Role: schema.User, Content: "go"}})
	if err != nil {
		t.Fatalf("Invoke must not hard-fail on a hallucinated tool: %v", err)
	}
	if !strings.Contains(out.Content, "Recovered") {
		t.Errorf("model must recover after graceful unknown-tool result, got %q", out.Content)
	}
}

// TestOwnedGraph_PreservesAssistantContentWithToolCalls verifies empirically
// whether the owned graph keeps an assistant message's Content alongside its
// ToolCalls in the transcript fed to the next model call. If it does, the
// StepContentStore content-recovery (a workaround for eino's react dropping
// content) is unnecessary on this path.
func TestOwnedGraph_PreservesAssistantContentWithToolCalls(t *testing.T) {
	tool := &charTool{name: "lookup", run: func(args string) string { return `{"ok":true}` }}
	tools := []einotool.BaseTool{tool}
	var sawAssistantToolCall bool
	var preservedContent string
	model := historyChatModel(func(input []*schema.Message) *schema.Message {
		if countToolMsgs(input) == 0 {
			m := charToolCall("c1", "lookup", `{"q":"x"}`)
			m.Content = "Let me look that up." // content + tool call in one message
			return m
		}
		for _, msg := range input {
			if msg.Role == schema.Assistant && len(msg.ToolCalls) > 0 {
				sawAssistantToolCall = true
				preservedContent = msg.Content
			}
		}
		return charText("Done.")
	})
	run, err := buildOwnedGraph(context.Background(), ownedGraphConfig{
		model: model, tools: tools, toolInfos: toolInfosFor(t, tools), maxStep: 6,
	})
	if err != nil {
		t.Fatalf("buildOwnedGraph: %v", err)
	}
	if _, err := run.Invoke(context.Background(), []*schema.Message{{Role: schema.User, Content: "go"}}); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !sawAssistantToolCall {
		t.Fatal("second model call never saw the prior assistant tool-call message")
	}
	t.Logf("owned graph preserved assistant content alongside tool_calls: %q", preservedContent)
	if preservedContent != "Let me look that up." {
		t.Errorf("CONTENT NOT PRESERVED — StepContentStore recovery still needed; got %q", preservedContent)
	}
}

// streamingContentThenToolModel streams a message as separate frames: a content
// chunk first, then a tool-call frame — the realistic shape that made eino's
// react agent drop content. Used to verify whether the OWNED graph preserves
// content+tool_calls through stream concatenation into state.
func streamingContentThenToolModel(preamble, toolName, toolArgs, finalText string) *mockChatModel {
	return &mockChatModel{
		generateFunc: func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
			if countToolMsgs(input) == 0 {
				m := charToolCall("c1", toolName, toolArgs)
				m.Content = preamble
				return m, nil
			}
			return charText(finalText), nil
		},
		streamFunc: func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](4)
			go func() {
				defer sw.Close()
				if countToolMsgs(input) == 0 {
					// Frame 1: content only. Frame 2: the tool call only.
					sw.Send(&schema.Message{Role: schema.Assistant, Content: preamble}, nil)
					sw.Send(&schema.Message{
						Role: schema.Assistant,
						ToolCalls: []schema.ToolCall{{
							ID: "c1", Type: "function",
							Function: schema.FunctionCall{Name: toolName, Arguments: toolArgs},
						}},
						ResponseMeta: &schema.ResponseMeta{FinishReason: "tool_calls"},
					}, nil)
					return
				}
				sw.Send(charText(finalText), nil)
			}()
			return sr, nil
		},
	}
}

func TestOwnedGraph_StreamingContentWithToolCallsPreserved(t *testing.T) {
	tool := &charTool{name: "lookup", run: func(args string) string { return `{"ok":true}` }}
	tools := []einotool.BaseTool{tool}
	var preserved string
	var saw bool
	base := streamingContentThenToolModel("Let me look that up.", "lookup", `{"q":"x"}`, "Done.")
	// Wrap the second-call inspection via generateFunc is not hit in Stream mode;
	// inspect by overriding decide through a closure on streamFunc input instead.
	model := &mockChatModel{
		streamFunc: func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			if countToolMsgs(input) > 0 {
				for _, msg := range input {
					if msg.Role == schema.Assistant && len(msg.ToolCalls) > 0 {
						saw = true
						preserved = msg.Content
					}
				}
			}
			return base.streamFunc(ctx, input, opts...)
		},
		generateFunc: base.generateFunc,
	}
	run, err := buildOwnedGraph(context.Background(), ownedGraphConfig{
		model: model, tools: tools, toolInfos: toolInfosFor(t, tools), maxStep: 6,
		streamToolChecker: agents.NewEnhancedStreamToolCallChecker(),
	})
	if err != nil {
		t.Fatalf("buildOwnedGraph: %v", err)
	}
	sr, err := run.Stream(context.Background(), []*schema.Message{{Role: schema.User, Content: "go"}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	drainOwnedStream(t, sr)
	if !saw {
		t.Fatal("second streaming model call never saw the prior assistant tool-call message")
	}
	t.Logf("MULTI-FRAME streaming: preserved assistant content = %q", preserved)
	if preserved != "Let me look that up." {
		t.Errorf("STREAMING CONTENT DROPPED — StepContentStore recovery is NECESSARY (not a workaround to delete); got %q", preserved)
	}
}
