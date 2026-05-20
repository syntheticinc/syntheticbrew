package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockToolForWrapper struct {
	name   string
	result string
	err    error
}

func (m *mockToolForWrapper) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: m.name, Desc: "test"}, nil
}

func (m *mockToolForWrapper) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	return m.result, m.err
}

func TestCancellableToolWrapper_PassThroughWhenNotCancelled(t *testing.T) {
	inner := &mockToolForWrapper{name: "any_tool", result: "ok"}
	wrapped := NewCancellableToolWrapper(inner)

	out, err := wrapped.InvokableRun(context.Background(), `{}`)
	require.NoError(t, err)
	assert.Equal(t, "ok", out)
}

func TestCancellableToolWrapper_ReturnsCancelledOnContextCancel(t *testing.T) {
	inner := &mockToolForWrapper{name: "any_tool", result: "ok"}
	wrapped := NewCancellableToolWrapper(inner)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out, err := wrapped.InvokableRun(ctx, `{}`)
	assert.True(t, errors.Is(err, context.Canceled))
	assert.Equal(t, "[CANCELLED] operation cancelled", out)
}

func TestCancellableToolWrapper_InfoDelegates(t *testing.T) {
	inner := &mockToolForWrapper{name: "memory_recall"}
	wrapped := NewCancellableToolWrapper(inner)

	info, err := wrapped.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "memory_recall", info.Name)
}

func TestGetContentRiskLevel_AllTools(t *testing.T) {
	tests := []struct {
		toolName string
		want     ContentRiskLevel
	}{
		// Internal coordination tools — no wrapping needed.
		{"manage_tasks", RiskNone},
		{"spawn_agent", RiskNone},
		{"show_structured_output", RiskNone},
		// Capability / MCP tools — default to high so their content is wrapped.
		{"memory_recall", RiskHigh},
		{"knowledge_search", RiskHigh},
		{"some_future_tool", RiskHigh},
	}
	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			got := GetContentRiskLevel(tt.toolName)
			assert.Equal(t, tt.want, got)
		})
	}
}
