package llm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// TestRetryWrapper_HappyPath_GenerateNoRetryNoLatency verifies the
// happy-path SLA after wiring RetryWrapper into every production LLM
// constructor: a successful Generate call must return immediately with
// the inner model's result, without consuming retry attempts or
// accumulating backoff delay.
//
// If this regresses, every chat in production would suffer from
// unnecessary retries / latency / cost.
func TestRetryWrapper_HappyPath_GenerateNoRetryNoLatency(t *testing.T) {
	callCount := 0
	inner := &stubToolCallingChatModel{
		generateFunc: func(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			callCount++
			return &schema.Message{Role: schema.Assistant, Content: "happy"}, nil
		},
	}
	wrapped := WrapWithRetry(inner)

	start := time.Now()
	out, err := wrapped.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "hi"}})
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "happy", out.Content)
	assert.Equal(t, 1, callCount, "happy path must call inner exactly once")
	assert.Less(t, elapsed, 100*time.Millisecond,
		"happy path must not incur retry-backoff delay (elapsed=%v)", elapsed)
}

// TestRetryWrapper_HappyPath_StreamPassThroughNoLatency verifies the
// streaming chat path: RetryWrapper.Stream is intentionally pass-through
// (streaming is stateful, not safely retriable), so successful streaming
// must show no added latency vs. the inner model.
func TestRetryWrapper_HappyPath_StreamPassThroughNoLatency(t *testing.T) {
	callCount := 0
	expected := &schema.StreamReader[*schema.Message]{}
	inner := &stubToolCallingChatModel{
		streamFunc: func(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			callCount++
			return expected, nil
		},
	}
	wrapped := WrapWithRetry(inner)

	start := time.Now()
	got, err := wrapped.Stream(context.Background(), []*schema.Message{{Role: schema.User, Content: "hi"}})
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Same(t, expected, got, "stream reader must be returned by reference, not wrapped")
	assert.Equal(t, 1, callCount)
	assert.Less(t, elapsed, 50*time.Millisecond)
}

// TestRetryWrapper_RetryableErrorRecovers verifies the production
// improvement: a transient 503 followed by success must transparently
// recover within the retry budget. This is the behaviour we gain by
// wrapping every constructor; without it, every transient hiccup
// terminated the turn with a feedback retry round-trip.
func TestRetryWrapper_RetryableErrorRecovers(t *testing.T) {
	callCount := 0
	inner := &stubToolCallingChatModel{
		generateFunc: func(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			callCount++
			if callCount == 1 {
				return nil, errors.New("HTTP 503 service unavailable")
			}
			return &schema.Message{Role: schema.Assistant, Content: "recovered"}, nil
		},
	}
	wrapper := NewRetryWrapper(inner, 2, 1*time.Millisecond, 1*time.Second)

	out, err := wrapper.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "hi"}})
	require.NoError(t, err)
	assert.Equal(t, "recovered", out.Content)
	assert.Equal(t, 2, callCount, "transient 503 must be retried once before success")
}

// TestRetryWrapper_AuthErrorFailsFast verifies the safety guarantee:
// LLM provider auth failure (401/403/invalid api key) must NOT be
// retried. Hot-looping against a failed-auth provider would burn cost
// AND latency on every chat without ever succeeding.
func TestRetryWrapper_AuthErrorFailsFast(t *testing.T) {
	callCount := 0
	inner := &stubToolCallingChatModel{
		generateFunc: func(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			callCount++
			return nil, errors.New("HTTP 401 unauthorized")
		},
	}
	wrapper := NewRetryWrapper(inner, 5, 1*time.Millisecond, 1*time.Second)

	_, err := wrapper.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "hi"}})
	require.Error(t, err)
	assert.Equal(t, 1, callCount, "auth failure must NOT be retried")
	assert.True(t, pkgerrors.Is(err, pkgerrors.CodeLLMAuth),
		"auth error must be wrapped as CodeLLMAuth for the agent classifier")
}

// TestRetryWrapper_TimeoutBoundsCallDuration verifies that the per-
// attempt timeout in RetryWrapper.Generate bounds a hanging provider.
// Without this, a slow/unresponsive LLM endpoint could pin a goroutine
// for the lifetime of the outer context (potentially minutes).
func TestRetryWrapper_TimeoutBoundsCallDuration(t *testing.T) {
	inner := &stubToolCallingChatModel{
		generateFunc: func(ctx context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			// Mimic an unresponsive provider by blocking until ctx done.
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	// Tight per-attempt timeout so the test runs fast.
	wrapper := NewRetryWrapper(inner, 0, 1*time.Millisecond, 50*time.Millisecond)

	start := time.Now()
	_, err := wrapper.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "hi"}})
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Less(t, elapsed, 500*time.Millisecond,
		"per-attempt timeout must bound call duration (elapsed=%v)", elapsed)
}
