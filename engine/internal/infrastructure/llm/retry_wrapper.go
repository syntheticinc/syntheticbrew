package llm

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// RetryWrapper wraps a ToolCallingChatModel with retry logic for transient errors.
type RetryWrapper struct {
	inner      model.ToolCallingChatModel
	maxRetries int
	baseDelay  time.Duration
	timeout    time.Duration
}

// NewRetryWrapper creates a retry wrapper around a chat model.
// maxRetries is the number of retry attempts (0 means no retries).
// baseDelay is the initial delay between retries (exponential backoff).
// timeout is the per-call timeout.
func NewRetryWrapper(inner model.ToolCallingChatModel, maxRetries int, baseDelay, timeout time.Duration) *RetryWrapper {
	return &RetryWrapper{
		inner:      inner,
		maxRetries: maxRetries,
		baseDelay:  baseDelay,
		timeout:    timeout,
	}
}

// Generate calls the inner model with retry logic for transient errors.
// Errors crossing this boundary are normalised via classifyLLMError so
// upstream consumers can use errors.Is against typed pkgerrors codes
// instead of substring matching on opaque provider messages.
func (w *RetryWrapper) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	var lastErr error
	for attempt := 0; attempt <= w.maxRetries; attempt++ {
		if attempt > 0 {
			delay := w.baseDelay * time.Duration(1<<uint(attempt-1))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		callCtx, cancel := context.WithTimeout(ctx, w.timeout)
		result, err := w.inner.Generate(callCtx, input, opts...)
		cancel()

		if err == nil {
			return result, nil
		}
		lastErr = err
		if !isRetriable(err) {
			return nil, classifyLLMError(err)
		}
	}
	return nil, classifyLLMError(fmt.Errorf("all %d retries failed: %w", w.maxRetries+1, lastErr))
}

// Stream delegates directly to the inner model (streaming is stateful,
// not retriable). Returned errors are normalised via classifyLLMError so
// agent-level recovery sees typed codes consistently with Generate.
func (w *RetryWrapper) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	reader, err := w.inner.Stream(ctx, input, opts...)
	if err != nil {
		return nil, classifyLLMError(err)
	}
	return reader, nil
}

// WithTools returns a new RetryWrapper with the specified tools bound to the inner model.
func (w *RetryWrapper) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	newInner, err := w.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &RetryWrapper{
		inner:      newInner,
		maxRetries: w.maxRetries,
		baseDelay:  w.baseDelay,
		timeout:    w.timeout,
	}, nil
}

// IsCallbacksEnabled forwards the inner model's callback aspect status so eino's
// components.Checker type-assertion succeeds on the wrapper and the framework
// does not auto-inject a duplicate aspect on top of the inner model's manual
// callbacks dispatch (which would emit every streamed chunk twice).
func (w *RetryWrapper) IsCallbacksEnabled() bool {
	return components.IsCallbacksEnabled(w.inner)
}

// isRetriable determines whether an error is transient and worth retrying.
// Delegates classification to classifyLLMError — the single chokepoint for
// LLM-error categorisation — then maps the typed code to a retry decision.
// Unknown shapes default to retriable (preserves prior behaviour).
func isRetriable(err error) bool {
	if err == nil {
		return false
	}

	typed := classifyLLMError(err)

	// Explicit non-retriable: auth failures, caller-side bad requests.
	if pkgerrors.Is(typed, pkgerrors.CodeLLMAuth) ||
		pkgerrors.Is(typed, pkgerrors.CodeInvalidInput) {
		return false
	}

	// Explicit retriable: rate-limit, transient transport/server errors.
	if pkgerrors.Is(typed, pkgerrors.CodeRateLimited) ||
		pkgerrors.Is(typed, pkgerrors.CodeTransient) {
		return true
	}

	// Unknown shape — retry to preserve existing behaviour where transient
	// hiccups producing non-standard error text get a second chance.
	return true
}
