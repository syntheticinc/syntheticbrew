package sessionprocessor

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	pb "github.com/syntheticinc/syntheticbrew/api/proto/gen"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/service/eventformat"
	apperrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// EventPublisher publishes session events to subscribers (consumer-side interface).
type EventPublisher interface {
	PublishEvent(sessionID string, event *pb.SessionEvent)
}

// EventStore persists session events for reliable replay (consumer-side interface).
type EventStore interface {
	Append(sessionID, eventType string, proto *pb.SessionEvent, jsonData map[string]interface{}) (string, error)
}

// InterruptCreator persists the interrupts row pointing at a request event.
// Nil in no-DB / test mode.
type InterruptCreator interface {
	Create(ctx context.Context, interrupt *domain.Interrupt) error
}

// EventStream converts domain.AgentEvent to pb.SessionEvent, persists via EventStore,
// and publishes via EventPublisher.
// Implements domain.AgentEventStream.
type EventStream struct {
	sessionID     string
	publisher     EventPublisher
	store         EventStore
	interrupts    InterruptCreator // optional, nil in no-DB mode; required for HITL
	totalTokens   int              // accumulated from EventTypeTokenUsage, included in ProcessingStopped
	contextTokens int              // actual context window size at last model call (from ContextRewriter)
}

// NewEventStream creates a new event stream that persists and publishes events.
// interrupts may be nil for no-DB / test contexts.
func NewEventStream(sessionID string, publisher EventPublisher, store EventStore, interrupts InterruptCreator) *EventStream {
	return &EventStream{
		sessionID:  sessionID,
		publisher:  publisher,
		store:      store,
		interrupts: interrupts,
	}
}

// Send converts a domain AgentEvent to a proto SessionEvent, persists it, and publishes.
// SchemaVersion is injected here so all events carry it (AC-EVT-01).
func (s *EventStream) Send(event *domain.AgentEvent) error {
	// Capture token usage for inclusion in ProcessingStopped (not broadcast as separate event)
	if event.Type == domain.EventTypeTokenUsage {
		if tokens, ok := event.Metadata["total_tokens"].(int); ok {
			s.totalTokens = tokens
		}
		if ctx, ok := event.Metadata["context_tokens"].(int); ok {
			s.contextTokens = ctx
		}
		return nil
	}

	// AC-EVT-01: ensure schema_version is set on every event
	if event.SchemaVersion == "" {
		event.SchemaVersion = domain.EventSchemaVersion
	}

	// Special case: EventTypeAnswer with already_streamed=true.
	// The client already received every chunk via ANSWER_CHUNK, so we must
	// NOT re-broadcast the full text (duplicate). We must, however, persist
	// the final aggregated assistant message in the event store so it shows
	// up on reload. convertEvent returns nil for this case (skip publish);
	// we build the pb event manually here and call persistOnly.
	if event.Type == domain.EventTypeAnswer {
		if streamed, _ := event.Metadata["already_streamed"].(bool); streamed {
			agentID := event.AgentID
			if agentID == "" {
				agentID = "supervisor"
			}
			pbEvent := &pb.SessionEvent{
				SessionId: s.sessionID,
				Type:      pb.SessionEventType_SESSION_EVENT_ANSWER,
				Content:   SanitizeUTF8(event.Content),
				AgentId:   agentID,
			}
			s.persistOnly(pbEvent)
			return nil
		}
	}

	pbEvent := s.convertEvent(event)
	if pbEvent == nil {
		return nil
	}

	pbEvent.SessionId = s.sessionID
	if isStreamingChunk(event) {
		// In-flight chunks are broadcast to SSE subscribers in real time but
		// not persisted — the DB keeps only the final aggregated message
		// (EventTypeAnswer / EventTypeReasoning with IsComplete=true). This
		// avoids hundreds of partial reasoning rows per turn.
		s.publisher.PublishEvent(s.sessionID, pbEvent)
		return nil
	}
	s.persistAndPublish(pbEvent)
	return nil
}

// isStreamingChunk reports whether the event is an in-flight chunk whose final
// aggregated form will be emitted separately. Chunks are published live to
// clients but skipped by the event store.
func isStreamingChunk(event *domain.AgentEvent) bool {
	switch event.Type {
	case domain.EventTypeAnswerChunk:
		return true
	case domain.EventTypeReasoning:
		return !event.IsComplete
	}
	return false
}

// PublishProcessingStarted sends a PROCESSING_STARTED event.
func (s *EventStream) PublishProcessingStarted() {
	s.persistAndPublish(&pb.SessionEvent{
		SessionId: s.sessionID,
		Type:      pb.SessionEventType_SESSION_EVENT_PROCESSING_STARTED,
	})
}

// PublishProcessingStopped sends a PROCESSING_STOPPED event.
// If token usage was reported during the turn, it is included in the Content field as JSON.
func (s *EventStream) PublishProcessingStopped() {
	evt := &pb.SessionEvent{
		SessionId: s.sessionID,
		Type:      pb.SessionEventType_SESSION_EVENT_PROCESSING_STOPPED,
	}
	if s.totalTokens > 0 {
		data := map[string]int{"total_tokens": s.totalTokens}
		if s.contextTokens > 0 {
			data["context_tokens"] = s.contextTokens
		}
		if encoded, err := json.Marshal(data); err == nil {
			evt.Content = string(encoded)
		}
	}
	s.persistAndPublish(evt)
}

// PublishError sends an ERROR event carrying the typed DeepestCode (so clients
// switch on a stable code instead of matching text) and the curated UserMessage.
func (s *EventStream) PublishError(err error) {
	msg := SanitizeUTF8(apperrors.UserMessage(err))
	s.persistAndPublish(&pb.SessionEvent{
		SessionId: s.sessionID,
		Type:      pb.SessionEventType_SESSION_EVENT_ERROR,
		Content:   msg,
		ErrorDetail: &pb.Error{
			Code:    apperrors.DeepestCode(err),
			Message: msg,
		},
	})
}

// PublishUserMessage sends a USER_MESSAGE event so user messages appear in backfill history.
func (s *EventStream) PublishUserMessage(content string) {
	s.persistAndPublish(&pb.SessionEvent{
		SessionId: s.sessionID,
		Type:      pb.SessionEventType_SESSION_EVENT_USER_MESSAGE,
		Content:   SanitizeUTF8(content),
	})
}

// PublishAnswerChunk sends an ANSWER_CHUNK event.
func (s *EventStream) PublishAnswerChunk(chunk string) {
	s.persistAndPublish(&pb.SessionEvent{
		SessionId: s.sessionID,
		Type:      pb.SessionEventType_SESSION_EVENT_ANSWER_CHUNK,
		Content:   chunk,
	})
}

// persistAndPublish stores the event, stamps the assigned id onto the proto,
// then publishes. For INTERRUPT_REQUEST it also creates the interrupts row
// with FK to the just-stored event so the resume path can JOIN back to it.
func (s *EventStream) persistAndPublish(event *pb.SessionEvent) {
	if s.store != nil {
		jsonData := eventformat.SerializeSessionEvent(event)
		eventType := eventformat.EventTypeString(event.GetType())

		id, err := s.store.Append(s.sessionID, eventType, event, jsonData)
		if err != nil {
			slog.ErrorContext(context.Background(), "failed to persist event", "session_id", s.sessionID, "event_type", eventType, "error", err)
		}
		if id != "" {
			event.EventId = id
		}

		if event.GetType() == pb.SessionEventType_SESSION_EVENT_INTERRUPT_REQUEST && s.interrupts != nil && id != "" {
			interruptID := event.GetCallId()
			if interruptID != "" {
				createErr := s.interrupts.Create(context.Background(), &domain.Interrupt{
					ID:             interruptID,
					RequestEventID: id,
				})
				if createErr != nil {
					slog.ErrorContext(context.Background(), "failed to create interrupt row", "session_id", s.sessionID, "interrupt_id", interruptID, "error", createErr)
				}
			}
		}
	}

	s.publisher.PublishEvent(s.sessionID, event)
}

// persistOnly stores the event in the event store but does NOT publish it.
// Used for EventTypeAnswer events with already_streamed=true: the client has
// already received the content via ANSWER_CHUNK chunks, so re-publishing the
// full aggregated text would duplicate it on the wire. The store row is still
// required so that GET /sessions/{id}/messages returns the final assistant
// message on reload.
func (s *EventStream) persistOnly(event *pb.SessionEvent) {
	if s.store == nil {
		return
	}
	jsonData := eventformat.SerializeSessionEvent(event)
	eventType := eventformat.EventTypeString(event.GetType())

	id, err := s.store.Append(s.sessionID, eventType, event, jsonData)
	if err != nil {
		slog.ErrorContext(context.Background(), "failed to persist event", "session_id", s.sessionID, "event_type", eventType, "error", err)
		return
	}
	if id != "" {
		event.EventId = id
	}
}

func (s *EventStream) convertEvent(event *domain.AgentEvent) *pb.SessionEvent {
	agentID := event.AgentID
	if agentID == "" {
		agentID = "supervisor"
	}

	switch event.Type {
	case domain.EventTypeAnswerChunk:
		return &pb.SessionEvent{
			Type:    pb.SessionEventType_SESSION_EVENT_ANSWER_CHUNK,
			Content: SanitizeUTF8(event.Content),
			AgentId: agentID,
			Step:    int32(event.Step),
		}

	case domain.EventTypeAnswer:
		// Skip SSE delivery if text was already streamed via message_delta chunks.
		// The event is still processed by MessageCollector for history persistence.
		if streamed, _ := event.Metadata["already_streamed"].(bool); streamed {
			return nil
		}
		return &pb.SessionEvent{
			Type:    pb.SessionEventType_SESSION_EVENT_ANSWER,
			Content: SanitizeUTF8(event.Content),
			AgentId: agentID,
		}

	case domain.EventTypeToolCall:
		args := ParseToolArguments(event)
		callID := fmt.Sprintf("server-%s-%d", event.Content, event.Step)
		return &pb.SessionEvent{
			Type:          pb.SessionEventType_SESSION_EVENT_TOOL_EXECUTION_START,
			ToolName:      event.Content,
			CallId:        callID,
			ToolArguments: args,
			AgentId:       agentID,
			Step:          int32(event.Step),
		}

	case domain.EventTypeToolResult:
		toolName := ""
		if name, ok := event.Metadata["tool_name"].(string); ok {
			toolName = name
		}
		callID := fmt.Sprintf("server-%s-%d", toolName, event.Step)

		summary := SanitizeUTF8(event.Content)
		if s, ok := event.Metadata["summary"].(string); ok {
			summary = SanitizeUTF8(s)
		}

		// Use full result for Content, not the truncated preview
		fullContent := SanitizeUTF8(event.Content)
		if fr, ok := event.Metadata["full_result"].(string); ok && fr != "" {
			fullContent = SanitizeUTF8(fr)
		}

		return &pb.SessionEvent{
			Type:              pb.SessionEventType_SESSION_EVENT_TOOL_EXECUTION_END,
			ToolName:          toolName,
			CallId:            callID,
			Content:           fullContent,
			ToolResultSummary: summary,
			ToolHasError:      event.Error != nil,
			AgentId:           agentID,
			Step:              int32(event.Step),
		}

	case domain.EventTypeReasoning:
		return &pb.SessionEvent{
			Type:    pb.SessionEventType_SESSION_EVENT_REASONING,
			Content: SanitizeUTF8(event.Content),
			AgentId: agentID,
			Step:    int32(event.Step),
		}

	case domain.EventTypePlanCreated, domain.EventTypePlanProgress, domain.EventTypePlanCompleted:
		return s.convertPlanEvent(event, agentID)

	case domain.EventTypeUserQuestion:
		question := SanitizeUTF8(event.Content)
		callID := ""
		if id, ok := event.Metadata["call_id"].(string); ok {
			callID = id
		}
		toolName := ""
		if name, ok := event.Metadata["tool_name"].(string); ok {
			toolName = name
		}
		return &pb.SessionEvent{
			Type:     pb.SessionEventType_SESSION_EVENT_ASK_USER,
			Content:  question,
			Question: question,
			CallId:   callID,
			ToolName: toolName,
			AgentId:  agentID,
		}

	case domain.EventTypeError:
		content := SanitizeUTF8(event.Content)
		var errDetail *pb.Error
		if event.Error != nil {
			content = SanitizeUTF8(event.Error.Message)
			errDetail = &pb.Error{
				Code:    event.Error.Code,
				Message: SanitizeUTF8(event.Error.Message),
			}
		}
		return &pb.SessionEvent{
			Type:        pb.SessionEventType_SESSION_EVENT_ERROR,
			Content:     content,
			ErrorDetail: errDetail,
		}

	case domain.EventTypeAgentSpawned, domain.EventTypeAgentCompleted, domain.EventTypeAgentFailed:
		eventTypeStr := string(event.Type)
		content := fmt.Sprintf("[%s] %s: %s", eventTypeStr, agentID, SanitizeUTF8(event.Content))
		return &pb.SessionEvent{
			Type:    pb.SessionEventType_SESSION_EVENT_ANSWER_CHUNK,
			Content: content,
			AgentId: agentID,
		}

	case domain.EventTypeStateChanged:
		// AC-STATE-02: agent.state_changed event
		content := SanitizeUTF8(event.Content)
		if content == "" {
			if m := event.Metadata; m != nil {
				newState, _ := m["new_state"].(string)
				oldState, _ := m["old_state"].(string)
				content = fmt.Sprintf("state: %s -> %s", oldState, newState)
			}
		}
		return &pb.SessionEvent{
			Type:    pb.SessionEventType_SESSION_EVENT_ANSWER_CHUNK,
			Content: content,
			AgentId: agentID,
		}

	case domain.EventTypeInterruptRequest:
		// interrupt_id surfaces as CallId so resume can correlate.
		callID, _ := event.Metadata["interrupt_id"].(string)
		return &pb.SessionEvent{
			Type:    pb.SessionEventType_SESSION_EVENT_INTERRUPT_REQUEST,
			Content: SanitizeUTF8(event.Content),
			CallId:  callID,
			AgentId: agentID,
			Step:    int32(event.Step),
		}

	case domain.EventTypeInterruptResume:
		// User submitted resume — Content carries InterruptResumePayload JSON.
		callID, _ := event.Metadata["interrupt_id"].(string)
		return &pb.SessionEvent{
			Type:    pb.SessionEventType_SESSION_EVENT_INTERRUPT_RESUME,
			Content: SanitizeUTF8(event.Content),
			CallId:  callID,
			AgentId: agentID,
			Step:    int32(event.Step),
		}

	default:
		return nil
	}
}

// convertPlanEvent converts plan-related domain events to SessionEvent.
func (s *EventStream) convertPlanEvent(event *domain.AgentEvent, agentID string) *pb.SessionEvent {
	pbEvent := &pb.SessionEvent{
		Type:    pb.SessionEventType_SESSION_EVENT_PLAN_UPDATE,
		AgentId: agentID,
	}

	if name, ok := event.Metadata["plan_name"].(string); ok {
		pbEvent.PlanName = name
	}

	if stepsRaw, ok := event.Metadata["plan_steps"]; ok {
		pbEvent.PlanSteps = ExtractPlanSteps(stepsRaw)
	}

	pbEvent.Content = SanitizeUTF8(event.Content)
	return pbEvent
}

// ParseToolArguments extracts tool arguments from event metadata.
func ParseToolArguments(event *domain.AgentEvent) map[string]string {
	args := make(map[string]string)
	argsJSON, ok := event.Metadata["function_arguments"].(string)
	if !ok || argsJSON == "" {
		return args
	}

	var parsedArgs map[string]interface{}
	if err := json.Unmarshal([]byte(argsJSON), &parsedArgs); err != nil {
		args["_json"] = SanitizeUTF8(argsJSON)
		return args
	}

	for k, v := range parsedArgs {
		switch val := v.(type) {
		case string:
			args[k] = SanitizeUTF8(val)
		case float64:
			args[k] = fmt.Sprintf("%.0f", val)
		case bool:
			args[k] = fmt.Sprintf("%v", val)
		case []interface{}:
			var parts []string
			for _, item := range val {
				parts = append(parts, SanitizeUTF8(fmt.Sprintf("%v", item)))
			}
			args[k] = strings.Join(parts, "\n")
		default:
			if jsonVal, err := json.Marshal(val); err == nil {
				args[k] = SanitizeUTF8(string(jsonVal))
			}
		}
	}
	return args
}

// ExtractPlanSteps converts raw metadata into PlanStep proto messages.
func ExtractPlanSteps(stepsRaw interface{}) []*pb.PlanStep {
	stepsSlice, ok := stepsRaw.([]interface{})
	if !ok {
		return nil
	}

	steps := make([]*pb.PlanStep, 0, len(stepsSlice))
	for _, raw := range stepsSlice {
		stepMap, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		step := &pb.PlanStep{}
		if title, ok := stepMap["title"].(string); ok {
			step.Title = title
		}
		if status, ok := stepMap["status"].(string); ok {
			step.Status = status
		}
		steps = append(steps, step)
	}
	return steps
}

// SanitizeUTF8 removes invalid UTF-8 characters from a string.
func SanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "\uFFFD")
}
