package eventformat

import (
	"testing"

	pb "github.com/syntheticinc/syntheticbrew/api/proto/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSerializeSessionEvent_ToolResult_IncludesFullResult(t *testing.T) {
	fullResult := "device1: iPhone 14 Pro\ndevice2: Pixel 8\ndevice3: Samsung Galaxy S24\ndevice4: OnePlus 12\ndevice5: Xiaomi 14"
	summary := "5 devices found"

	event := &pb.SessionEvent{
		Type:              pb.SessionEventType_SESSION_EVENT_TOOL_EXECUTION_END,
		CallId:            "server-device.list-1",
		ToolName:          "device.list",
		Content:           fullResult,
		ToolResultSummary: summary,
		ToolHasError:      false,
		AgentId:           "supervisor",
	}

	result := SerializeSessionEvent(event)

	require.NotNil(t, result)
	assert.Equal(t, "ToolExecutionCompleted", result["type"])
	assert.Equal(t, fullResult, result["result"], "result field should contain the full content")
	assert.Equal(t, summary, result["result_summary"], "result_summary should contain the summary")
	assert.Equal(t, "device.list", result["tool_name"])
	assert.Equal(t, false, result["has_error"])
}
