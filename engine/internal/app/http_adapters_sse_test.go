package app

import (
	"encoding/json"
	"testing"

	pb "github.com/syntheticinc/syntheticbrew/api/proto/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertSessionEventToSSE_ToolResult_FullContent(t *testing.T) {
	fullResult := "This is a very long tool result that exceeds 500 characters. " +
		"It contains detailed JSON data with device IDs, names, and other fields. " +
		"Previously this was truncated to ~500 chars in the SSE event, but now " +
		"the full result should be passed through. " +
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" +
		"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB" +
		"CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC" +
		"DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD"

	event := &pb.SessionEvent{
		Type:              pb.SessionEventType_SESSION_EVENT_TOOL_EXECUTION_END,
		ToolName:          "device.list",
		CallId:            "server-device.list-1",
		Content:           fullResult,
		ToolResultSummary: "5 devices",
		ToolHasError:      false,
	}

	sse := convertSessionEventToSSE(event, "test-session")
	require.NotNil(t, sse)
	assert.Equal(t, "tool_result", sse.Type)

	var data map[string]interface{}
	err := json.Unmarshal([]byte(sse.Data), &data)
	require.NoError(t, err)

	assert.Equal(t, fullResult, data["content"], "content should be the full result")
	assert.Equal(t, "5 devices", data["summary"], "summary should be present")
	assert.Equal(t, "device.list", data["tool"])
	assert.Equal(t, "server-device.list-1", data["call_id"])
	assert.Equal(t, false, data["has_error"])
}

// TestConvertSessionEventToSSE_ProcessingStopped_ForwardsCachedTokens is the regression
// guard for the chat SSE path: the done event must forward cached_prompt_tokens (and the
// prompt/completion breakdown), not just total/context. Earlier the converter dropped
// them, so cached caching was invisible in the admin chat even when reported.
func TestConvertSessionEventToSSE_ProcessingStopped_ForwardsCachedTokens(t *testing.T) {
	event := &pb.SessionEvent{
		Type:    pb.SessionEventType_SESSION_EVENT_PROCESSING_STOPPED,
		Content: `{"total_tokens":5000,"context_tokens":4800,"prompt_tokens":4600,"completion_tokens":400,"cached_prompt_tokens":4622}`,
	}

	sse := convertSessionEventToSSE(event, "test-session")
	require.NotNil(t, sse)
	assert.Equal(t, "done", sse.Type)

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(sse.Data), &data))
	assert.Equal(t, float64(5000), data["total_tokens"])
	assert.Equal(t, float64(4800), data["context_tokens"])
	assert.Equal(t, float64(4600), data["prompt_tokens"])
	assert.Equal(t, float64(400), data["completion_tokens"])
	assert.Equal(t, float64(4622), data["cached_prompt_tokens"], "cached must reach the SSE done event")
}

// TestConvertSessionEventToSSE_ProcessingStopped_OmitsCachedWhenZero — the >0 gate holds
// at the SSE layer too: a turn with no cache hits carries no cached_prompt_tokens key.
func TestConvertSessionEventToSSE_ProcessingStopped_OmitsCachedWhenZero(t *testing.T) {
	event := &pb.SessionEvent{
		Type:    pb.SessionEventType_SESSION_EVENT_PROCESSING_STOPPED,
		Content: `{"total_tokens":5000,"prompt_tokens":4600,"completion_tokens":400}`,
	}

	sse := convertSessionEventToSSE(event, "test-session")
	require.NotNil(t, sse)
	var data map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(sse.Data), &data))
	_, hasCached := data["cached_prompt_tokens"]
	assert.False(t, hasCached, "cached_prompt_tokens must be omitted when absent/zero")
}

func TestConvertSessionEventToSSE_ToolResult_WithError(t *testing.T) {
	event := &pb.SessionEvent{
		Type:              pb.SessionEventType_SESSION_EVENT_TOOL_EXECUTION_END,
		ToolName:          "device.get",
		CallId:            "server-device.get-2",
		Content:           "Error: device not found with ID abc-123",
		ToolResultSummary: "Error: device not found with ID abc-123",
		ToolHasError:      true,
	}

	sse := convertSessionEventToSSE(event, "test-session")
	require.NotNil(t, sse)

	var data map[string]interface{}
	err := json.Unmarshal([]byte(sse.Data), &data)
	require.NoError(t, err)

	assert.Equal(t, "Error: device not found with ID abc-123", data["content"])
	assert.Equal(t, true, data["has_error"])
}
