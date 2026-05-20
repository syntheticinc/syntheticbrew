package tools

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// CancellableToolWrapper wraps a tool.InvokableTool and checks context before execution.
// If context is cancelled, returns immediately without running the tool.
type CancellableToolWrapper struct {
	inner tool.InvokableTool
}

// NewCancellableToolWrapper wraps a tool with context cancellation check.
func NewCancellableToolWrapper(inner tool.InvokableTool) tool.InvokableTool {
	return &CancellableToolWrapper{inner: inner}
}

func (w *CancellableToolWrapper) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return w.inner.Info(ctx)
}

func (w *CancellableToolWrapper) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	if ctx.Err() != nil {
		return "[CANCELLED] operation cancelled", ctx.Err()
	}
	return w.inner.InvokableRun(ctx, argumentsInJSON, opts...)
}
