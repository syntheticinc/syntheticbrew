package callbacks

import (
	"context"
	"fmt"
	"testing"

	"github.com/cloudwego/eino/callbacks"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// mockToolCallRecorder records tool calls and results for assertions.
type mockToolCallRecorder struct {
	calls   []recordedCall
	results []recordedResult
}

type recordedCall struct {
	sessionID string
	toolName  string
}

type recordedResult struct {
	sessionID string
	toolName  string
	result    string
}

func (m *mockToolCallRecorder) RecordToolCall(sessionID, toolName string) {
	m.calls = append(m.calls, recordedCall{sessionID: sessionID, toolName: toolName})
}

func (m *mockToolCallRecorder) RecordToolResult(sessionID, toolName, result string) {
	m.results = append(m.results, recordedResult{sessionID: sessionID, toolName: toolName, result: result})
}

func newTestToolEventHandler(collector *eventCollector, recorder *mockToolCallRecorder) (*ToolEventHandler, *StepCounter) {
	emitter := NewEventEmitter(collector.Callback, "test-agent")
	counter := NewStepCounter()
	sessionID := "test-session"
	if recorder == nil {
		recorder = &mockToolCallRecorder{}
	}
	handler := NewToolEventHandler(emitter, counter, nil, recorder, sessionID)
	return handler, counter
}

func TestOnToolError_EmitsEventWithError(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantCode    string
		wantMessage string
	}{
		{
			name:        "regular error",
			err:         fmt.Errorf("connection refused"),
			wantCode:    "tool_error",
			wantMessage: "connection refused",
		},
		{
			name:        "wrapped error",
			err:         fmt.Errorf("execute tool: %w", fmt.Errorf("timeout")),
			wantCode:    "tool_error",
			wantMessage: "execute tool: timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector := newEventCollector()
			handler, _ := newTestToolEventHandler(collector, nil)

			info := &callbacks.RunInfo{Name: "read_file"}
			handler.OnToolError(context.Background(), info, tt.err)

			events := collector.GetEventsByType(domain.EventTypeToolResult)
			require.Len(t, events, 1)

			event := events[0]
			assert.Equal(t, domain.EventTypeToolResult, event.Type)
			require.NotNil(t, event.Error)
			assert.Equal(t, tt.wantCode, event.Error.Code)
			assert.Equal(t, tt.wantMessage, event.Error.Message)
			assert.Equal(t, tt.wantMessage, event.Content)
		})
	}
}

// TestOnToolEnd_ErrorPrefixLiftsIntoEventError verifies the [ERROR]-
// convention path: when a tool returns a string starting with "[ERROR] "
// (the canonical engine-wide pattern for application-level tool errors,
// produced by MCP isError responses and any other native tool that
// reports a non-platform failure), OnToolEnd lifts it into event.Error
// with the canonical "tool_error" code instead of treating it as a
// successful tool result.
func TestOnToolEnd_ErrorPrefixLiftsIntoEventError(t *testing.T) {
	collector := newEventCollector()
	handler, _ := newTestToolEventHandler(collector, nil)

	info := &callbacks.RunInfo{Name: "rule_create"}
	errPayload := "[ERROR] Permission denied. The user does not have access."

	// Build the einotool.CallbackOutput with our error-prefixed string;
	// the handler must not abort the turn but must mark the event with
	// event.Error so SSE consumers render it as an error tool result.
	output := &einotool.CallbackOutput{Response: errPayload}
	handler.OnToolEnd(context.Background(), info, output)

	events := collector.GetEventsByType(domain.EventTypeToolResult)
	require.Len(t, events, 1)

	event := events[0]
	require.NotNil(t, event.Error, "[ERROR] prefix must lift into event.Error")
	assert.Equal(t, "tool_error", event.Error.Code)
	assert.Equal(t, errPayload, event.Error.Message)
}

func TestOnToolEnd_TripsBreakerAfterConsecutiveErrors(t *testing.T) {
	collector := newEventCollector()
	handler, _ := newTestToolEventHandler(collector, nil)

	info := &callbacks.RunInfo{Name: "device_list"}
	out := &einotool.CallbackOutput{Response: "[ERROR] ERROR: Invalid input."}

	// Below threshold: no abort.
	for i := 0; i < defaultMaxConsecutiveToolErrors-1; i++ {
		handler.OnToolEnd(context.Background(), info, out)
	}
	if _, _, ok := handler.Aborted(); ok {
		t.Fatalf("breaker tripped before threshold (%d errors)", defaultMaxConsecutiveToolErrors-1)
	}

	// The threshold-th consecutive error trips the breaker.
	handler.OnToolEnd(context.Background(), info, out)
	tool, lastErr, ok := handler.Aborted()
	require.True(t, ok, "breaker must trip at threshold")
	assert.Equal(t, "device_list", tool)
	assert.Equal(t, "[ERROR] ERROR: Invalid input.", lastErr)
}

func TestOnToolEnd_BreakerResetsOnSuccess(t *testing.T) {
	collector := newEventCollector()
	handler, _ := newTestToolEventHandler(collector, nil)

	info := &callbacks.RunInfo{Name: "device_list"}
	errOut := &einotool.CallbackOutput{Response: "[ERROR] boom"}
	okOut := &einotool.CallbackOutput{Response: `{"devices":[]}`}

	for i := 0; i < defaultMaxConsecutiveToolErrors-1; i++ {
		handler.OnToolEnd(context.Background(), info, errOut)
	}
	handler.OnToolEnd(context.Background(), info, okOut) // success resets the streak
	handler.OnToolEnd(context.Background(), info, errOut)

	if _, _, ok := handler.Aborted(); ok {
		t.Fatal("breaker must not trip when a success reset the error streak")
	}
}

func TestOnToolEnd_BreakerSuccessOnOtherToolDoesNotReset(t *testing.T) {
	collector := newEventCollector()
	handler, _ := newTestToolEventHandler(collector, nil)

	errOut := &einotool.CallbackOutput{Response: "[ERROR] boom"}
	okOut := &einotool.CallbackOutput{Response: `{"ok":true}`}
	// tool_a keeps failing; an unrelated tool_b success must not reset tool_a's streak.
	for i := 0; i < defaultMaxConsecutiveToolErrors-1; i++ {
		handler.OnToolEnd(context.Background(), &callbacks.RunInfo{Name: "tool_a"}, errOut)
	}
	handler.OnToolEnd(context.Background(), &callbacks.RunInfo{Name: "tool_b"}, okOut)
	handler.OnToolEnd(context.Background(), &callbacks.RunInfo{Name: "tool_a"}, errOut)

	if _, _, ok := handler.Aborted(); !ok {
		t.Fatal("tool_a reached the threshold; tool_b success must not have reset it")
	}
}

func TestFormatToolLoopAbortMessage(t *testing.T) {
	msg := formatToolLoopAbortMessage("device_list", "[ERROR] ERROR: Invalid input.")
	assert.Contains(t, msg, "device_list")
	assert.Contains(t, msg, "ERROR: Invalid input.")
	assert.NotContains(t, msg, "[ERROR]")
}

func TestOnToolError_IncrementsStep(t *testing.T) {
	collector := newEventCollector()
	handler, counter := newTestToolEventHandler(collector, nil)

	assert.Equal(t, 0, counter.GetStep())

	info := &callbacks.RunInfo{Name: "read_file"}
	handler.OnToolError(context.Background(), info, fmt.Errorf("file not found"))

	assert.Equal(t, 1, counter.GetStep())

	// Second error increments again
	handler.OnToolError(context.Background(), info, fmt.Errorf("permission denied"))

	assert.Equal(t, 2, counter.GetStep())
}

func TestOnToolError_RecordsToolResult(t *testing.T) {
	collector := newEventCollector()
	recorder := &mockToolCallRecorder{}
	handler, _ := newTestToolEventHandler(collector, recorder)

	info := &callbacks.RunInfo{Name: "execute_command"}
	handler.OnToolError(context.Background(), info, fmt.Errorf("exit code 1"))

	require.Len(t, recorder.results, 1)
	assert.Equal(t, "test-session", recorder.results[0].sessionID)
	assert.Equal(t, "execute_command", recorder.results[0].toolName)
	assert.Equal(t, "exit code 1", recorder.results[0].result)
}

func TestOnToolError_SetsMetadata(t *testing.T) {
	collector := newEventCollector()
	handler, _ := newTestToolEventHandler(collector, nil)

	info := &callbacks.RunInfo{Name: "search_code"}
	handler.OnToolError(context.Background(), info, fmt.Errorf("index corrupted"))

	events := collector.GetEventsByType(domain.EventTypeToolResult)
	require.Len(t, events, 1)

	event := events[0]
	require.NotNil(t, event.Metadata)
	assert.Equal(t, "search_code", event.Metadata["tool_name"])
	assert.Equal(t, len("index corrupted"), event.Metadata["result_length"])
	assert.Equal(t, "index corrupted", event.Metadata["full_result"])
}

func TestOnToolError_UsesCurrentStep(t *testing.T) {
	collector := newEventCollector()
	handler, counter := newTestToolEventHandler(collector, nil)

	// Simulate being at step 5
	for i := 0; i < 5; i++ {
		_ = counter.IncrementStep(context.Background())
	}

	info := &callbacks.RunInfo{Name: "read_file"}
	handler.OnToolError(context.Background(), info, fmt.Errorf("error"))

	events := collector.GetEventsByType(domain.EventTypeToolResult)
	require.Len(t, events, 1)
	assert.Equal(t, 5, events[0].Step)

	// After the error, step should be 6
	assert.Equal(t, 6, counter.GetStep())
}
