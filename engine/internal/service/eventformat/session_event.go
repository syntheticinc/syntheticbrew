package eventformat

import (
	"context"
	"log/slog"

	pb "github.com/syntheticinc/bytebrew/engine/api/proto/gen"
)

// SerializeSessionEvent converts a proto SessionEvent into the flat JSON shape
// used for SSE persistence (session_event_log) and admin/widget event payloads.
func SerializeSessionEvent(event *pb.SessionEvent) map[string]interface{} {
	switch event.GetType() {
	case pb.SessionEventType_SESSION_EVENT_ANSWER:
		return map[string]interface{}{
			"type":     "MessageCompleted",
			"content":  event.GetContent(),
			"role":     "assistant",
			"agent_id": event.GetAgentId(),
		}

	case pb.SessionEventType_SESSION_EVENT_ANSWER_CHUNK:
		return map[string]interface{}{
			"type":     "StreamingProgress",
			"content":  event.GetContent(),
			"agent_id": event.GetAgentId(),
		}

	case pb.SessionEventType_SESSION_EVENT_TOOL_EXECUTION_START:
		args := make(map[string]interface{}, len(event.GetToolArguments()))
		for k, v := range event.GetToolArguments() {
			args[k] = v
		}
		return map[string]interface{}{
			"type":      "ToolExecutionStarted",
			"call_id":   event.GetCallId(),
			"tool_name": event.GetToolName(),
			"arguments": args,
			"agent_id":  event.GetAgentId(),
		}

	case pb.SessionEventType_SESSION_EVENT_TOOL_EXECUTION_END:
		return map[string]interface{}{
			"type":           "ToolExecutionCompleted",
			"call_id":        event.GetCallId(),
			"tool_name":      event.GetToolName(),
			"result":         event.GetContent(),
			"result_summary": event.GetToolResultSummary(),
			"has_error":      event.GetToolHasError(),
			"agent_id":       event.GetAgentId(),
		}

	case pb.SessionEventType_SESSION_EVENT_REASONING:
		return map[string]interface{}{
			"type":     "ReasoningChunk",
			"content":  event.GetContent(),
			"agent_id": event.GetAgentId(),
		}

	case pb.SessionEventType_SESSION_EVENT_ASK_USER:
		return map[string]interface{}{
			"type":     "AskUserRequested",
			"question": event.GetQuestion(),
			"options":  event.GetOptions(),
			"agent_id": event.GetAgentId(),
		}

	case pb.SessionEventType_SESSION_EVENT_PROCESSING_STARTED:
		return map[string]interface{}{
			"type":  "ProcessingStarted",
			"state": "processing",
		}

	case pb.SessionEventType_SESSION_EVENT_PROCESSING_STOPPED:
		return map[string]interface{}{
			"type":  "ProcessingStopped",
			"state": "idle",
		}

	case pb.SessionEventType_SESSION_EVENT_ERROR:
		msg := event.GetContent()
		if detail := event.GetErrorDetail(); detail != nil {
			msg = detail.GetMessage()
		}
		return map[string]interface{}{
			"type":    "Error",
			"message": msg,
			"code":    "error",
		}

	case pb.SessionEventType_SESSION_EVENT_USER_MESSAGE:
		return map[string]interface{}{
			"type":    "UserMessage",
			"content": event.GetContent(),
			"role":    "user",
		}

	case pb.SessionEventType_SESSION_EVENT_PLAN_UPDATE:
		steps := make([]map[string]interface{}, 0, len(event.GetPlanSteps()))
		for _, s := range event.GetPlanSteps() {
			steps = append(steps, map[string]interface{}{
				"title":  s.GetTitle(),
				"status": s.GetStatus(),
			})
		}
		return map[string]interface{}{
			"type":      "PlanUpdated",
			"plan_name": event.GetPlanName(),
			"steps":     steps,
			"agent_id":  event.GetAgentId(),
		}

	case pb.SessionEventType_SESSION_EVENT_INTERRUPT_REQUEST:
		return map[string]interface{}{
			"type":         "InterruptRequest",
			"interrupt_id": event.GetCallId(),
			"content":      event.GetContent(),
			"agent_id":     event.GetAgentId(),
		}

	case pb.SessionEventType_SESSION_EVENT_INTERRUPT_RESUME:
		return map[string]interface{}{
			"type":         "InterruptResume",
			"interrupt_id": event.GetCallId(),
			"content":      event.GetContent(),
			"agent_id":     event.GetAgentId(),
		}

	default:
		slog.WarnContext(context.Background(), "unknown session event type for serialization", "type", event.GetType().String())
		return nil
	}
}

// EventTypeString returns a human-readable event type string from a proto SessionEventType.
func EventTypeString(t pb.SessionEventType) string {
	switch t {
	case pb.SessionEventType_SESSION_EVENT_USER_MESSAGE:
		return "user_message"
	case pb.SessionEventType_SESSION_EVENT_PROCESSING_STARTED:
		return "processing_started"
	case pb.SessionEventType_SESSION_EVENT_PROCESSING_STOPPED:
		return "processing_stopped"
	case pb.SessionEventType_SESSION_EVENT_ANSWER:
		return "answer"
	case pb.SessionEventType_SESSION_EVENT_ANSWER_CHUNK:
		return "answer_chunk"
	case pb.SessionEventType_SESSION_EVENT_TOOL_EXECUTION_START:
		return "tool_call_start"
	case pb.SessionEventType_SESSION_EVENT_TOOL_EXECUTION_END:
		return "tool_call_end"
	case pb.SessionEventType_SESSION_EVENT_REASONING:
		return "reasoning"
	case pb.SessionEventType_SESSION_EVENT_ASK_USER:
		return "ask_user"
	case pb.SessionEventType_SESSION_EVENT_ERROR:
		return "error"
	case pb.SessionEventType_SESSION_EVENT_PLAN_UPDATE:
		return "plan_update"
	case pb.SessionEventType_SESSION_EVENT_INTERRUPT_REQUEST:
		return "interrupt_request"
	case pb.SessionEventType_SESSION_EVENT_INTERRUPT_RESUME:
		return "interrupt_resume"
	default:
		return "unknown"
	}
}
