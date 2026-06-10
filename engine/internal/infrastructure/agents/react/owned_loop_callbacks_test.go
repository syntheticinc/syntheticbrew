package react

import (
	"context"
	"io"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agents/callbacks"
)

// TestOwnedGraph_EmitsCallbackEvents proves the ucb callback handler injects into
// the hand-built graph exactly as it does for eino's prebuilt agent: a single
// tool round emits tool_call → tool_result, and the model's prose is finalized as
// an answer. This is the wiring that lets the owned loop reuse the entire
// callbacks emission layer unchanged.
func TestOwnedGraph_EmitsCallbackEvents(t *testing.T) {
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
		maxStep:   50,
	})
	if err != nil {
		t.Fatalf("buildOwnedGraph: %v", err)
	}

	cap := &streamCapture{}
	cb := callbacks.NewBuilder(callbacks.BuilderConfig{
		EventCallback: cap.onEvent,
		ChunkCallback: cap.onChunk,
		SessionID:     "owned-cb",
		AgentID:       "supervisor",
	})

	sr, err := run.Stream(context.Background(),
		[]*schema.Message{{Role: schema.User, Content: "go"}},
		cb.BuildComposeCallbacksOption())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for {
		_, rerr := sr.Recv()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			t.Fatalf("Recv: %v", rerr)
		}
	}
	sr.Close()
	cb.WaitStreamDone()
	cb.FinalizeAccumulatedText(context.Background())

	t.Logf("typeSeq=%v", cap.typeSeq())
	if cap.countType(domain.EventTypeToolCall) != 1 {
		t.Errorf("expected one tool_call event from owned graph, got %d", cap.countType(domain.EventTypeToolCall))
	}
	if cap.countType(domain.EventTypeToolResult) != 1 {
		t.Errorf("expected one tool_result event from owned graph, got %d", cap.countType(domain.EventTypeToolResult))
	}
	if cap.countType(domain.EventTypeAnswer) != 1 {
		t.Errorf("expected one answer event from owned graph, got %d", cap.countType(domain.EventTypeAnswer))
	}
}
