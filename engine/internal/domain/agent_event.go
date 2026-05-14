package domain

import (
	"time"
)

// AgentEventType represents the type of agent event.
//
// Event Contract (AC-EVT-03):
//
// All events carry:
//   - type: one of the AgentEventType constants below
//   - schema_version: "1.0" (AC-EVT-01) — for forward compatibility
//   - timestamp: RFC3339 time
//   - agent_id: identifies the agent that produced the event
//
// Clients MUST ignore unknown event types gracefully (AC-EVT-02).
//
// Event types:
//   - answer: final agent answer text
//   - answer_chunk: streaming text chunk
//   - reasoning: LLM reasoning/thinking content
//   - tool_call: tool invocation (content = tool name, metadata has arguments)
//   - tool_result: tool execution result
//   - plan_created/plan_progress/plan_completed: plan lifecycle
//   - error: agent error (includes error.code + error.message)
//   - agent_spawned/agent_completed/agent_failed: sub-agent lifecycle
//   - user_question: confirm_before prompt for client (awaiting user approval)
//   - structured_output: tables, action buttons
//   - state_changed: agent lifecycle state transition (metadata: agent_name, old_state, new_state, reason)
//   - flow.*: flow pipeline events (flow.step_started, flow.step_completed, flow.completed, flow.failed)
type AgentEventType string

const (
	EventTypeAnswer           AgentEventType = "answer"            // Standard agent answer
	EventTypeReasoning        AgentEventType = "reasoning"         // Reasoning content (for GLM 4.7, Claude thinking)
	EventTypeToolCall         AgentEventType = "tool_call"         // Tool invocation
	EventTypeToolResult       AgentEventType = "tool_result"       // Tool result
	EventTypeAnswerChunk      AgentEventType = "answer_chunk"      // Streaming answer chunk
	EventTypePlanCreated      AgentEventType = "plan_created"      // Plan created
	EventTypePlanProgress     AgentEventType = "plan_progress"     // Plan progress update
	EventTypePlanCompleted    AgentEventType = "plan_completed"    // Plan completed
	EventTypeError            AgentEventType = "error"             // Agent error (XML parsing, etc.)
	EventTypeAgentSpawned     AgentEventType = "agent_spawned"     // Code Agent spawned
	EventTypeAgentCompleted   AgentEventType = "agent_completed"   // Code Agent completed
	EventTypeAgentFailed      AgentEventType = "agent_failed"      // Code Agent failed
	EventTypeUserQuestion     AgentEventType = "user_question"     // confirm_before prompt awaiting user approval
	EventTypeStructuredOutput AgentEventType = "structured_output" // structured data display (tables, actions)
	EventTypeStateChanged     AgentEventType = "state_changed"     // Agent lifecycle state transition (AC-STATE-02)
	EventTypeTokenUsage       AgentEventType = "token_usage"       // Cumulative token usage for the turn (metadata: total_tokens, prompt_tokens, completion_tokens)
	// Signals the client to drop assistant text already emitted in this turn —
	// fires when a HITL tool_call lands alongside model-generated prose.
	EventTypeRetractAssistant AgentEventType = "assistant_retract"
)

// AgentError represents error information for EventTypeError
type AgentError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// EventSchemaVersion is the current version of the SSE event schema (AC-EVT-01).
const EventSchemaVersion = "1.0"

// AgentEvent represents an event from the agent execution.
// All events carry SchemaVersion for forward compatibility (AC-EVT-01).
type AgentEvent struct {
	Type          AgentEventType         `json:"type"`
	SchemaVersion string                 `json:"schema_version"`        // Always "1.0" (AC-EVT-01)
	EventID       string                 `json:"event_id,omitempty"`    // Unique event ID: "{sessionID}-{counter}", assigned at broadcast
	Timestamp     time.Time              `json:"timestamp"`
	Step          int                    `json:"step"`
	Content       string                 `json:"content"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
	IsComplete    bool                   `json:"is_complete,omitempty"` // For streaming: false during stream, true when complete
	Error         *AgentError            `json:"error,omitempty"`       // Error details for EventTypeError
	AgentID       string                 `json:"agent_id,omitempty"`    // "supervisor" | "code-agent-{uuid[:8]}"
}

// StateChangedData holds data for EventTypeStateChanged events.
type StateChangedData struct {
	AgentName string `json:"agent_name"`
	OldState  string `json:"old_state"`
	NewState  string `json:"new_state"`
	Reason    string `json:"reason,omitempty"`
}

// NewStateChangedEvent creates an agent.state_changed event (AC-STATE-02).
func NewStateChangedEvent(agentName string, oldState, newState LifecycleState, reason string) *AgentEvent {
	return &AgentEvent{
		Type:          EventTypeStateChanged,
		SchemaVersion: EventSchemaVersion,
		Timestamp:     time.Now(),
		AgentID:       agentName,
		Metadata: map[string]interface{}{
			"agent_name": agentName,
			"old_state":  string(oldState),
			"new_state":  string(newState),
			"reason":     reason,
		},
	}
}

// AgentEventStream defines interface for sending agent events
type AgentEventStream interface {
	Send(event *AgentEvent) error
}
