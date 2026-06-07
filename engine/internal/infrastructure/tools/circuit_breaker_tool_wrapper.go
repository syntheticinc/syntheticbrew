package tools

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// CircuitBreakerChecker checks circuit breaker state for a resource (consumer-side interface).
type CircuitBreakerChecker interface {
	AllowRequest() error
	RecordSuccess()
	RecordFailure()
}

// CircuitBreakerToolWrapper wraps a tool with circuit breaker checks.
// Used for MCP tools to short-circuit calls when the server is unavailable.
type CircuitBreakerToolWrapper struct {
	inner   tool.InvokableTool
	breaker CircuitBreakerChecker
}

// NewCircuitBreakerToolWrapper wraps a tool with circuit breaker protection.
func NewCircuitBreakerToolWrapper(inner tool.InvokableTool, breaker CircuitBreakerChecker) tool.InvokableTool {
	return &CircuitBreakerToolWrapper{
		inner:   inner,
		breaker: breaker,
	}
}

func (w *CircuitBreakerToolWrapper) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return w.inner.Info(ctx)
}

func (w *CircuitBreakerToolWrapper) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	// Check circuit breaker before calling.
	//
	// Return a Go error so Eino's tool framework fires OnToolError (not
	// OnToolEnd). This lets the session_event_log record `tool_has_error=true`
	// for CB-open rejections, which is what the Tool Call Log UI filters on
	// (status=failed). Returning nil would misleadingly mark the call as
	// completed in the observability view.
	if err := w.breaker.AllowRequest(); err != nil {
		// AllowRequest already returns a typed DomainError whose Error() carries
		// the "[UNAVAILABLE] …" prefix; return it as-is (no double prefix).
		return "", err
	}

	// Execute tool
	output, err := w.inner.InvokableRun(ctx, argumentsInJSON, opts...)
	if err != nil {
		w.breaker.RecordFailure()
		return output, err
	}

	w.breaker.RecordSuccess()
	return output, nil
}
