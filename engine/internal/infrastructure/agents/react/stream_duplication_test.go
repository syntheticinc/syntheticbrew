package react

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// streamingMockChatModel mimics the eino-ext openai client shape:
//
//   - declares IsCallbacksEnabled() = true (the openai client returns true);
//   - dispatches callbacks.OnEndWithStreamOutput from inside Stream(), the
//     same way libs/acl/openai.(*Client).Stream does at chat_model.go:955.
//
// This shape is what makes the duplication bug observable. If a wrapper
// sitting in front of this model does not also implement components.Checker
// (IsCallbacksEnabled), eino's compose layer auto-injects a second aspect
// and our handler is invoked twice per stream — each invocation drains its
// own tee'd copy and emits every chunk to chunkCallback again.
type streamingMockChatModel struct {
	chunks    []string
	streamHit atomic.Int32
}

func (m *streamingMockChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	full := ""
	for _, c := range m.chunks {
		full += c
	}
	return &schema.Message{Role: schema.Assistant, Content: full}, nil
}

func (m *streamingMockChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.streamHit.Add(1)

	// Build a stream of CallbackOutput frames (the shape the framework
	// expects for the manual aspect dispatch).
	cbOuts := make([]*model.CallbackOutput, 0, len(m.chunks))
	for _, c := range m.chunks {
		cbOuts = append(cbOuts, &model.CallbackOutput{
			Message: &schema.Message{Role: schema.Assistant, Content: c},
		})
	}
	cbStream := schema.StreamReaderFromArray(cbOuts)

	// Manually dispatch end-with-stream-output (mirrors the openai client).
	// The framework receives `nsr` and gives subscribers tee'd copies; the
	// caller of Stream() reads the converted output.
	_, nsr := callbacks.OnEndWithStreamOutput(ctx, cbStream)
	out := schema.StreamReaderWithConvert(nsr, func(o *model.CallbackOutput) (*schema.Message, error) {
		if o.Message == nil {
			return nil, schema.ErrNoValue
		}
		return o.Message, nil
	})
	return out, nil
}

func (m *streamingMockChatModel) BindTools(tools []*schema.ToolInfo) error { return nil }

func (m *streamingMockChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func (m *streamingMockChatModel) GetType() string          { return "streaming-mock" }
func (m *streamingMockChatModel) IsCallbacksEnabled() bool { return true }

func runStreamAndCollect(t *testing.T, m model.ToolCallingChatModel) []string {
	t.Helper()

	cfg := AgentConfig{
		ChatModel: m,
		MaxSteps:  10,
		SessionID: "test-stream-dup",
		AgentID:   "supervisor",
	}
	agent, err := NewAgent(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	var got []string
	chunkCb := func(chunk string) error {
		got = append(got, chunk)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := agent.Stream(ctx, "say hi", chunkCb, func(*domain.AgentEvent) error { return nil }); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	return got
}

// TestStream_NoChunkDuplication_PlainModel — baseline: a self-dispatching
// ChatModel without any wrapper produces one chunkCallback per stream frame.
func TestStream_NoChunkDuplication_PlainModel(t *testing.T) {
	chunks := []string{"Hello", "!", " ", "World"}
	got := runStreamAndCollect(t, &streamingMockChatModel{chunks: chunks})

	if len(got) != len(chunks) {
		t.Fatalf("plain: expected %d chunks, got %d (%v)", len(chunks), len(got), got)
	}
	for i, want := range chunks {
		if got[i] != want {
			t.Errorf("chunk[%d]: want %q, got %q", i, want, got[i])
		}
	}
}

// TestStream_NoChunkDuplication_WrappedModel — regression test for the SSE
// chunk-doubling bug.
//
// Wrapping the chat model with llm.WrapWithModelParams (any non-empty
// override — temperature/top_p/max_tokens/stop) used to double every streamed
// chunk on the wire. Cause: modelParamsWrapper did not implement
// components.Checker (IsCallbacksEnabled), so eino auto-injected a callback
// aspect on top of the inner model's own manual aspect dispatch — two
// goroutines drained tee'd copies of the same stream, each invoking
// chunkCallback per chunk.
//
// Fix: forward IsCallbacksEnabled through the wrapper (see
// llm/model_params_wrapper.go; the same forward is added on retry/debug
// wrappers).
func TestStream_NoChunkDuplication_WrappedModel(t *testing.T) {
	chunks := []string{"Hello", "!", " ", "World"}
	mock := &streamingMockChatModel{chunks: chunks}

	temp := 0.7
	wrapped := llm.WrapWithModelParams(mock, llm.ModelParams{Temperature: &temp})

	got := runStreamAndCollect(t, wrapped)

	if len(got) != len(chunks) {
		t.Fatalf("wrapped: expected %d chunks (one per frame), got %d — duplication regressed: %v",
			len(chunks), len(got), got)
	}
	for i, want := range chunks {
		if got[i] != want {
			t.Errorf("chunk[%d]: want %q, got %q", i, want, got[i])
		}
	}
}
