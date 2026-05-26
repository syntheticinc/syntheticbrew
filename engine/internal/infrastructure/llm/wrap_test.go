package llm

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// TestWrapWithRetry_ReturnsRetryWrapper ensures the chokepoint returns a
// *RetryWrapper, not the raw inner model. This is the contract every
// production constructor depends on — if WrapWithRetry ever stops
// wrapping, the agent layer would see raw provider errors again.
func TestWrapWithRetry_ReturnsRetryWrapper(t *testing.T) {
	inner := &stubToolCallingChatModel{}
	wrapped := WrapWithRetry(inner)

	if wrapped == nil {
		t.Fatal("WrapWithRetry returned nil")
	}

	if _, ok := wrapped.(*RetryWrapper); !ok {
		t.Fatalf("expected *RetryWrapper, got %T", wrapped)
	}
}

// TestWrapWithRetry_ForwardsGenerate verifies the wrapper transparently
// forwards Generate calls to the inner model on the happy path.
func TestWrapWithRetry_ForwardsGenerate(t *testing.T) {
	called := false
	inner := &stubToolCallingChatModel{
		generateFunc: func(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			called = true
			return &schema.Message{Role: schema.Assistant, Content: "ok"}, nil
		},
	}
	wrapped := WrapWithRetry(inner)

	out, err := wrapped.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "ping"}})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if !called {
		t.Fatal("inner.Generate was not invoked")
	}
	if out == nil || out.Content != "ok" {
		t.Fatalf("unexpected output: %+v", out)
	}
}

// TestWrapWithRetry_ForwardsStream verifies the wrapper transparently
// forwards Stream calls. Stream is intentionally not retried by
// RetryWrapper — verify that pass-through behaviour is preserved.
func TestWrapWithRetry_ForwardsStream(t *testing.T) {
	called := false
	inner := &stubToolCallingChatModel{
		streamFunc: func(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			called = true
			return nil, nil
		},
	}
	wrapped := WrapWithRetry(inner)

	_, err := wrapped.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if !called {
		t.Fatal("inner.Stream was not invoked")
	}
}

// stubToolCallingChatModel is a minimal mock for testing the wrapper
// behaviour. The real mock_chat_model.go is heavier and not needed here.
type stubToolCallingChatModel struct {
	generateFunc func(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.Message, error)
	streamFunc   func(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error)
}

func (s *stubToolCallingChatModel) Generate(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if s.generateFunc != nil {
		return s.generateFunc(ctx, msgs, opts...)
	}
	return nil, nil
}

func (s *stubToolCallingChatModel) Stream(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if s.streamFunc != nil {
		return s.streamFunc(ctx, msgs, opts...)
	}
	return nil, nil
}

func (s *stubToolCallingChatModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return s, nil
}
