package tools

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// extraTool is a stub whose Info carries a return-directly Extra flag, mirroring
// what the MCP adapter emits for a tool declaring it via `_meta`.
type extraTool struct{ name string }

func (e *extraTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name:  e.name,
		Extra: map[string]any{domain.ToolExtraReturnDirectly: true},
	}, nil
}

func (e *extraTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return "ok", nil
}

// TestWrappers_PreserveReturnDirectlyExtra is the regression guard for the
// name-based return-directly seam: MCP tools are wrapped by the timeout and
// circuit-breaker wrappers before reaching the ReAct loop, which reads
// ToolInfo.Extra. The wrappers delegate Info(), so the flag must survive both —
// in the same nesting order resolveMCPTools applies (timeout innermost, CB outer).
func TestWrappers_PreserveReturnDirectlyExtra(t *testing.T) {
	var inner tool.InvokableTool = &extraTool{name: "recommend_products"}
	inner = NewTimeoutToolWrapper(inner, 1000)
	wrapped := NewCircuitBreakerToolWrapper(inner, &stubBreaker{})

	info, err := wrapped.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if v, _ := info.Extra[domain.ToolExtraReturnDirectly].(bool); !v {
		t.Errorf("return-directly Extra must survive timeout+circuit-breaker wrapping, got %#v", info.Extra)
	}
}
