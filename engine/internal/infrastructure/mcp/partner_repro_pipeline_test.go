package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/callbacks"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	enginecallbacks "github.com/syntheticinc/syntheticbrew/internal/infrastructure/agents/callbacks"
)

// TestPartnerRepro_EndToEnd_PermissionDeniedDoesNotAbortTurn is the
// in-process equivalent of the partner-bug repro that would run as a
// full HTTP integration test. It walks the *exact* code path that
// fired in production for the partner: MCP server returns isError:true
// with RBAC text → MCP client → tool adapter → Eino tool callback →
// emitted AgentEvent.
//
// What this test guards (each step is a place the original bug could
// have hidden):
//
//  1. MCP adapter must NOT bubble a Go error for isError:true (the
//     direct cause of "INTERNAL_ERROR: agent stream failed" in prod).
//  2. The adapter output must be parseable by OnToolEnd's [ERROR]-
//     prefix detection so the event is marked as a tool error.
//  3. The emitted AgentEvent must carry event.Error.Code == "tool_error"
//     and the partner-supplied content in event.Error.Message, so SSE
//     consumers render an error tool result instead of aborting.
//  4. The step counter must increment so the agent loop progresses.
//
// If any step regresses, this test fails — and the partner bug is back.
func TestPartnerRepro_EndToEnd_PermissionDeniedDoesNotAbortTurn(t *testing.T) {
	// === STEP 1: mock MCP server that mirrors the partner trace ===
	// The trace string is byte-identical to the production failure
	// (excerpted from the partner bug report).
	partnerRBACMessage := "ERROR: Permission denied. The user does not have access to this resource."

	transport := newMockTransport()
	resultJSON, _ := json.Marshal(ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: partnerRBACMessage}},
		IsError: true,
	})
	transport.responses["tools/call"] = &Response{JSONRPC: "2.0", ID: 1, Result: resultJSON}

	client := NewClient("partner-mcp", transport)
	tool := MCPTool{Name: "rule_create", Description: "Create a rule"}
	adapted := AdaptMCPTool(client, tool)

	// === STEP 2: invoke through the adapter ===
	// Pre-fix: this returned ("", &MCPToolError{Content: ...}).
	// Post-fix: returns ("[ERROR] <message>", nil).
	output, err := adapted.InvokableRun(context.Background(), `{"name":"x","bpmn_xml":"<?xml ...?>"}`)
	require.NoError(t, err,
		"REGRESSION: MCP isError must not bubble as Go error; agent layer would abort the turn")
	require.NotEmpty(t, output)
	require.True(t, strings.HasPrefix(output, "[ERROR] "),
		"adapter output must start with [ERROR] so OnToolEnd lifts it into event.Error")
	require.Contains(t, output, partnerRBACMessage,
		"partner-supplied content must reach the event so the LLM can react")

	// === STEP 3: simulate Eino calling OnToolEnd with this output ===
	// In production, Eino's tool node feeds InvokableRun's return value
	// into the tool callback chain. We replay that step in-process and
	// capture what the engine emits to SSE clients.
	collector := newEventCollector()
	emitter := enginecallbacks.NewEventEmitter(collector.Callback, "test-agent")
	counter := enginecallbacks.NewStepCounter()
	stepBefore := counter.GetStep()

	handler := enginecallbacks.NewToolEventHandler(emitter, counter, nil, &nullRecorder{}, "partner-session", nil)
	handler.OnToolEnd(
		context.Background(),
		&callbacks.RunInfo{Name: "rule_create"},
		&einotool.CallbackOutput{Response: output},
	)

	// === STEP 4: verify the emitted event matches the SSE contract ===
	events := collector.events
	require.Len(t, events, 1, "OnToolEnd must emit exactly one ToolResult event")

	evt := events[0]
	assert.Equal(t, domain.EventTypeToolResult, evt.Type,
		"event type must be ToolResult so SSE clients route it correctly")
	require.NotNil(t, evt.Error,
		"REGRESSION: [ERROR] prefix must lift into event.Error; without it, SSE clients see a normal result and never render the error")
	assert.Equal(t, "tool_error", evt.Error.Code,
		"event.Error.Code must match the SSE contract used by partner client UIs")
	assert.Contains(t, evt.Error.Message, partnerRBACMessage,
		"event.Error.Message must contain the partner-supplied content so SSE clients render it verbatim")

	// === STEP 5: verify the agent loop can progress ===
	// Step counter must advance — otherwise the next turn would loop
	// on a stale counter, hiding state corruption.
	assert.Greater(t, counter.GetStep(), stepBefore,
		"step counter must advance after OnToolEnd so the agent loop progresses")
}

// nullRecorder is the no-op implementation of ToolCallRecorder used to
// keep the pipeline test focused on event flow rather than recorder
// side effects.
type nullRecorder struct{}

func (n *nullRecorder) RecordToolCall(_, _ string)      {}
func (n *nullRecorder) RecordToolResult(_, _, _ string) {}

// eventCollector mirrors the test harness in tool_event_handler_test.go
// but lives here so the pipeline test can stay self-contained in the
// mcp package. Callback signature matches engine's EventEmitter contract.
type eventCollector struct {
	events []*domain.AgentEvent
}

func newEventCollector() *eventCollector {
	return &eventCollector{}
}

func (c *eventCollector) Callback(evt *domain.AgentEvent) error {
	c.events = append(c.events, evt)
	return nil
}
