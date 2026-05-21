package callbacks

import (
	"context"
	"fmt"
	"testing"

	"github.com/cloudwego/eino/callbacks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/mcp"
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

func TestOnToolError_MCPToolError(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantMessage string
	}{
		{
			name:        "direct MCPToolError",
			err:         &mcp.MCPToolError{Content: "service unavailable"},
			wantMessage: "service unavailable",
		},
		{
			name:        "wrapped MCPToolError",
			err:         fmt.Errorf("mcp call failed: %w", &mcp.MCPToolError{Content: "rate limited"}),
			wantMessage: "rate limited",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector := newEventCollector()
			handler, _ := newTestToolEventHandler(collector, nil)

			info := &callbacks.RunInfo{Name: "mcp_tool"}
			handler.OnToolError(context.Background(), info, tt.err)

			events := collector.GetEventsByType(domain.EventTypeToolResult)
			require.Len(t, events, 1)

			event := events[0]
			assert.Equal(t, tt.wantMessage, event.Error.Message)
			assert.Equal(t, tt.wantMessage, event.Content)
			assert.Equal(t, "tool_error", event.Error.Code)
		})
	}
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

func TestOnToolError_MCPToolError_RecordsContent(t *testing.T) {
	collector := newEventCollector()
	recorder := &mockToolCallRecorder{}
	handler, _ := newTestToolEventHandler(collector, recorder)

	info := &callbacks.RunInfo{Name: "mcp_tool"}
	handler.OnToolError(context.Background(), info, &mcp.MCPToolError{Content: "not found"})

	require.Len(t, recorder.results, 1)
	assert.Equal(t, "not found", recorder.results[0].result)
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
