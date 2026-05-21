package eventformat

import (
	"testing"

	pb "github.com/syntheticinc/syntheticbrew/api/proto/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSerializeSessionEvent(t *testing.T) {
	tests := []struct {
		name     string
		event    *pb.SessionEvent
		expected map[string]interface{}
	}{
		{
			name: "answer",
			event: &pb.SessionEvent{
				Type:    pb.SessionEventType_SESSION_EVENT_ANSWER,
				Content: "Hello, world!",
				AgentId: "supervisor",
			},
			expected: map[string]interface{}{
				"type":     "MessageCompleted",
				"content":  "Hello, world!",
				"role":     "assistant",
				"agent_id": "supervisor",
			},
		},
		{
			name: "answer chunk",
			event: &pb.SessionEvent{
				Type:    pb.SessionEventType_SESSION_EVENT_ANSWER_CHUNK,
				Content: "partial ",
				AgentId: "code-agent-1",
			},
			expected: map[string]interface{}{
				"type":     "StreamingProgress",
				"content":  "partial ",
				"agent_id": "code-agent-1",
			},
		},
		{
			name: "tool execution start with arguments",
			event: &pb.SessionEvent{
				Type:     pb.SessionEventType_SESSION_EVENT_TOOL_EXECUTION_START,
				CallId:   "call-123",
				ToolName: "read_file",
				ToolArguments: map[string]string{
					"path": "/src/main.go",
					"line": "10",
				},
				AgentId: "code-agent-1",
			},
			expected: map[string]interface{}{
				"type":      "ToolExecutionStarted",
				"call_id":   "call-123",
				"tool_name": "read_file",
				"arguments": map[string]interface{}{
					"path": "/src/main.go",
					"line": "10",
				},
				"agent_id": "code-agent-1",
			},
		},
		{
			name: "tool execution start with empty arguments",
			event: &pb.SessionEvent{
				Type:     pb.SessionEventType_SESSION_EVENT_TOOL_EXECUTION_START,
				CallId:   "call-456",
				ToolName: "get_status",
				AgentId:  "supervisor",
			},
			expected: map[string]interface{}{
				"type":      "ToolExecutionStarted",
				"call_id":   "call-456",
				"tool_name": "get_status",
				"arguments": map[string]interface{}{},
				"agent_id":  "supervisor",
			},
		},
		{
			name: "reasoning",
			event: &pb.SessionEvent{
				Type:    pb.SessionEventType_SESSION_EVENT_REASONING,
				Content: "I need to analyze the code structure first.",
				AgentId: "supervisor",
			},
			expected: map[string]interface{}{
				"type":     "ReasoningChunk",
				"content":  "I need to analyze the code structure first.",
				"agent_id": "supervisor",
			},
		},
		{
			name: "ask user with options",
			event: &pb.SessionEvent{
				Type:     pb.SessionEventType_SESSION_EVENT_ASK_USER,
				Question: "Which approach do you prefer?",
				Options:  []string{"option A", "option B"},
				AgentId:  "supervisor",
			},
			expected: map[string]interface{}{
				"type":     "AskUserRequested",
				"question": "Which approach do you prefer?",
				"options":  []string{"option A", "option B"},
				"agent_id": "supervisor",
			},
		},
		{
			name: "ask user without options",
			event: &pb.SessionEvent{
				Type:     pb.SessionEventType_SESSION_EVENT_ASK_USER,
				Question: "Should I proceed?",
				AgentId:  "supervisor",
			},
			expected: map[string]interface{}{
				"type":     "AskUserRequested",
				"question": "Should I proceed?",
				"options":  []string(nil),
				"agent_id": "supervisor",
			},
		},
		{
			name: "processing started",
			event: &pb.SessionEvent{
				Type: pb.SessionEventType_SESSION_EVENT_PROCESSING_STARTED,
			},
			expected: map[string]interface{}{
				"type":  "ProcessingStarted",
				"state": "processing",
			},
		},
		{
			name: "processing stopped",
			event: &pb.SessionEvent{
				Type: pb.SessionEventType_SESSION_EVENT_PROCESSING_STOPPED,
			},
			expected: map[string]interface{}{
				"type":  "ProcessingStopped",
				"state": "idle",
			},
		},
		{
			name: "error from content",
			event: &pb.SessionEvent{
				Type:    pb.SessionEventType_SESSION_EVENT_ERROR,
				Content: "something went wrong",
			},
			expected: map[string]interface{}{
				"type":    "Error",
				"message": "something went wrong",
				"code":    "error",
			},
		},
		{
			name: "error with ErrorDetail overrides content",
			event: &pb.SessionEvent{
				Type:    pb.SessionEventType_SESSION_EVENT_ERROR,
				Content: "generic fallback",
				ErrorDetail: &pb.Error{
					Code:    "rate_limit",
					Message: "too many requests",
				},
			},
			expected: map[string]interface{}{
				"type":    "Error",
				"message": "too many requests",
				"code":    "error",
			},
		},
		{
			name: "user message",
			event: &pb.SessionEvent{
				Type:    pb.SessionEventType_SESSION_EVENT_USER_MESSAGE,
				Content: "Hello agent",
			},
			expected: map[string]interface{}{
				"type":    "UserMessage",
				"content": "Hello agent",
				"role":    "user",
			},
		},
		{
			name: "plan update with steps",
			event: &pb.SessionEvent{
				Type:     pb.SessionEventType_SESSION_EVENT_PLAN_UPDATE,
				PlanName: "Implementation Plan",
				PlanSteps: []*pb.PlanStep{
					{Title: "Analyze requirements", Status: "completed"},
					{Title: "Write code", Status: "in_progress"},
					{Title: "Write tests", Status: "pending"},
				},
				AgentId: "code-agent-1",
			},
			expected: map[string]interface{}{
				"type":      "PlanUpdated",
				"plan_name": "Implementation Plan",
				"steps": []map[string]interface{}{
					{"title": "Analyze requirements", "status": "completed"},
					{"title": "Write code", "status": "in_progress"},
					{"title": "Write tests", "status": "pending"},
				},
				"agent_id": "code-agent-1",
			},
		},
		{
			name: "plan update with no steps",
			event: &pb.SessionEvent{
				Type:     pb.SessionEventType_SESSION_EVENT_PLAN_UPDATE,
				PlanName: "Empty Plan",
				AgentId:  "supervisor",
			},
			expected: map[string]interface{}{
				"type":      "PlanUpdated",
				"plan_name": "Empty Plan",
				"steps":     []map[string]interface{}{},
				"agent_id":  "supervisor",
			},
		},
		{
			name: "unknown event type returns nil",
			event: &pb.SessionEvent{
				Type:    pb.SessionEventType(9999),
				Content: "should be ignored",
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SerializeSessionEvent(tt.event)

			if tt.expected == nil {
				assert.Nil(t, result)
				return
			}

			require.NotNil(t, result)
			assert.Equal(t, len(tt.expected), len(result), "map should have expected number of keys")

			for key, want := range tt.expected {
				got, exists := result[key]
				require.True(t, exists, "key %q should exist in result", key)
				assert.Equal(t, want, got, "key %q", key)
			}
		})
	}
}

func TestSerializeSessionEvent_EmptyContent(t *testing.T) {
	event := &pb.SessionEvent{
		Type:    pb.SessionEventType_SESSION_EVENT_ANSWER,
		Content: "",
		AgentId: "",
	}

	result := SerializeSessionEvent(event)
	require.NotNil(t, result)
	assert.Equal(t, "MessageCompleted", result["type"])
	assert.Equal(t, "", result["content"])
	assert.Equal(t, "", result["agent_id"])
	assert.Equal(t, "assistant", result["role"])
}

func TestEventTypeString(t *testing.T) {
	tests := []struct {
		eventType pb.SessionEventType
		want      string
	}{
		{pb.SessionEventType_SESSION_EVENT_USER_MESSAGE, "user_message"},
		{pb.SessionEventType_SESSION_EVENT_PROCESSING_STARTED, "processing_started"},
		{pb.SessionEventType_SESSION_EVENT_PROCESSING_STOPPED, "processing_stopped"},
		{pb.SessionEventType_SESSION_EVENT_ANSWER, "answer"},
		{pb.SessionEventType_SESSION_EVENT_ANSWER_CHUNK, "answer_chunk"},
		{pb.SessionEventType_SESSION_EVENT_TOOL_EXECUTION_START, "tool_call_start"},
		{pb.SessionEventType_SESSION_EVENT_TOOL_EXECUTION_END, "tool_call_end"},
		{pb.SessionEventType_SESSION_EVENT_REASONING, "reasoning"},
		{pb.SessionEventType_SESSION_EVENT_ASK_USER, "ask_user"},
		{pb.SessionEventType_SESSION_EVENT_ERROR, "error"},
		{pb.SessionEventType_SESSION_EVENT_PLAN_UPDATE, "plan_update"},
		{pb.SessionEventType_SESSION_EVENT_INTERRUPT_REQUEST, "interrupt_request"},
		{pb.SessionEventType_SESSION_EVENT_INTERRUPT_RESUME, "interrupt_resume"},
		{pb.SessionEventType(9999), "unknown"},
		{pb.SessionEventType(0), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := EventTypeString(tt.eventType)
			assert.Equal(t, tt.want, got)
		})
	}
}
