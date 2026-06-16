package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	pb "github.com/syntheticinc/syntheticbrew/api/proto/gen"
	deliveryhttp "github.com/syntheticinc/syntheticbrew/internal/delivery/http"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agentregistry"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/flowregistry"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"github.com/syntheticinc/syntheticbrew/internal/service/sessionprocessor"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// schemaChatRepo narrows the schema repository to the operations the chat
// dispatcher needs: load for chat_enabled + entry_agent_id, and stamp
// chat_last_fired_at on the first message of a session. Defined consumer-side
// so the adapter can be unit-tested against a fake.
type schemaChatRepo interface {
	GetModelByID(ctx context.Context, id string) (*models.SchemaModel, error)
	MarkChatFired(ctx context.Context, id string) error
}

// chatSessionPersister persists chat sessions to the DB.
type chatSessionPersister interface {
	Create(ctx context.Context, session *models.SessionModel) error
	// CreateIfNotExists is the idempotent variant — silently skips when
	// a row with the same primary key already exists. Used by the chat
	// adapter's first-seen path so engine restart / registry eviction
	// does not produce a "duplicate key" WARN on every existing session.
	CreateIfNotExists(ctx context.Context, session *models.SessionModel) error
	Update(ctx context.Context, id string, updates map[string]interface{}) error
}

type interruptResumeRepo interface {
	LoadWithRequestEvent(ctx context.Context, id string) (*domain.Interrupt, *models.SessionEventLogModel, error)
	MarkResolved(ctx context.Context, id, resolveEventID string) (bool, error)
}

type interruptResumeHistory interface {
	Create(ctx context.Context, message *domain.Message) error
}

type resumeEventStore interface {
	Append(sessionID, eventType string, event *pb.SessionEvent, jsonData map[string]interface{}) (string, error)
}

// chatServiceHTTPAdapter bridges SessionRegistry + SessionProcessor to the
// deliveryhttp.ChatService interface for the REST chat endpoint.
type chatServiceHTTPAdapter struct {
	registry    *flowregistry.SessionRegistry
	processor   *sessionprocessor.Processor
	agents      *agentregistry.AgentRegistry // non-nil in single-tenant (CE) mode
	registryMgr *agentregistry.Manager       // non-nil in multi-tenant (Cloud/EE) mode
	schemas     schemaChatRepo               // optional — nil in tests / no-DB mode
	sessions    chatSessionPersister         // optional — nil when no DB
	chatEnabled bool                         // false when no LLM model configured
	interrupts  interruptResumeRepo
	eventStore  resumeEventStore
	history     interruptResumeHistory // mirrors interrupt_resume into messages table for reload restore
}

// resolveRegistry returns the AgentRegistry for the current request context.
// In single-tenant mode it returns a.agents directly; in multi-tenant mode it
// delegates to registryMgr.GetForContext to get the per-tenant registry.
func (a *chatServiceHTTPAdapter) resolveRegistry(ctx context.Context) (*agentregistry.AgentRegistry, error) {
	if a.agents != nil {
		return a.agents, nil
	}
	if a.registryMgr != nil {
		return a.registryMgr.GetForContext(ctx)
	}
	return nil, fmt.Errorf("no agent registry configured")
}

// Chat creates (or resumes) a session for the given schema, enqueues the
// user message, subscribes to events, and returns an SSEEvent channel that
// closes when processing stops.
//
// Resolution: schemaID → SchemaModel.entry_agent_id → agentregistry.GetByID →
// agent name used by SessionRegistry and Processor. Chat is allowed only when
// schemas.chat_enabled = true; if disabled, NotFound is returned so the route
// doesn't leak existence of chat-disabled schemas.
func (a *chatServiceHTTPAdapter) Chat(ctx context.Context, schemaID, message, userSub, sessionID string) (<-chan deliveryhttp.SSEEvent, error) {
	if userSub == "" {
		return nil, pkgerrors.InvalidInput("user_sub is required")
	}
	if a.schemas == nil {
		return nil, fmt.Errorf("schema repo not wired")
	}

	// Tenant-scoped schema lookup must happen before any other check so that
	// cross-tenant requests get NotFound (404) rather than leaking a 500.
	schema, err := a.schemas.GetModelByID(ctx, schemaID)
	if err != nil {
		return nil, fmt.Errorf("load schema: %w", err)
	}
	if schema == nil {
		return nil, pkgerrors.NotFound(fmt.Sprintf("schema not found: %s", schemaID))
	}
	if !schema.ChatEnabled {
		return nil, pkgerrors.NotFound(fmt.Sprintf("schema not found: %s", schemaID))
	}
	if schema.EntryAgentID == nil || *schema.EntryAgentID == "" {
		return nil, pkgerrors.InvalidInput("schema has no entry agent")
	}

	registry, err := a.resolveRegistry(ctx)
	if err != nil {
		return nil, fmt.Errorf("no agents configured: %w", err)
	}

	entryAgent, err := registry.GetByID(ctx, *schema.EntryAgentID)
	if err != nil {
		return nil, pkgerrors.NotFound(fmt.Sprintf("entry agent not found for schema %s", schemaID))
	}
	agentName := entryAgent.Record.Name

	if sessionID == "" {
		sessionID = uuid.New().String()
	} else if _, err := uuid.Parse(sessionID); err != nil {
		return nil, pkgerrors.InvalidInput("session_id must be a valid UUID")
	}

	// "First seen" = not in the in-memory registry. This is what triggers
	// session-row persistence; relying on a client-side `sessionID == ""`
	// signal misses the case where the caller supplies a fresh UUID for
	// tracking purposes (e.g., e2e tests), which would FK-fail when the
	// first message/tool-event is written.
	firstSeen := !a.registry.HasSession(sessionID)
	if firstSeen {
		a.registry.CreateSession(sessionID, "", userSub, "", "", agentName)

		if markErr := a.schemas.MarkChatFired(ctx, schemaID); markErr != nil {
			slog.WarnContext(ctx, "mark schema chat fired failed", "schema_id", schemaID, "error", markErr)
		}
		if a.sessions != nil {
			m := &models.SessionModel{
				ID:       sessionID,
				SchemaID: schemaID,
				UserSub:  userSub,
				Status:   "active",
			}
			// Idempotent: the in-memory "first seen" check above is
			// process-scoped and is wrong after engine restart or
			// registry eviction. Using CreateIfNotExists tolerates the
			// row already existing in the DB without log spam, while
			// still creating it on genuinely-new sessions.
			if createErr := a.sessions.CreateIfNotExists(ctx, m); createErr != nil {
				slog.WarnContext(ctx, "persist chat session failed", "session_id", sessionID, "error", createErr)
			}
		}
	}

	// Subscribe BEFORE enqueueing so we don't miss events.
	eventCh, cleanup := a.registry.Subscribe(sessionID)

	if err := a.registry.EnqueueMessage(sessionID, message); err != nil {
		cleanup()
		return nil, fmt.Errorf("enqueue message: %w", err)
	}

	a.processor.StartProcessing(ctx, sessionID)

	// Fan-out: read proto events, convert to SSE, close when processing stops.
	// Buffered channel avoids deadlock when the HTTP handler is slow to read.
	sseCh := make(chan deliveryhttp.SSEEvent, 64)
	go func() {
		defer close(sseCh)
		defer a.registry.RemoveSession(sessionID)
		defer a.processor.StopProcessing(sessionID)
		defer cleanup()

		for protoEvent := range eventCh {
			sseEvent := convertSessionEventToSSE(protoEvent, sessionID)
			if sseEvent == nil {
				continue
			}
			sseCh <- *sseEvent

			if sseEvent.Type == "done" {
				if a.sessions != nil {
					if updateErr := a.sessions.Update(context.Background(), sessionID, map[string]interface{}{"status": "completed"}); updateErr != nil {
						slog.WarnContext(context.Background(), "update chat session status failed", "session_id", sessionID, "error", updateErr)
					}
				}
				return
			}
		}
	}()

	return sseCh, nil
}

// ResumeInterrupt validates the interrupt + tenant + session match, persists
// interrupt_resume, marks the interrupt row resolved (atomic via WHERE
// status='pending' — second caller gets 409), and resumes the React loop by
// enqueueing a reconstructed Q+A user message.
//
// Errors → writeDomainError on the handler: InvalidInput→400, NotFound→404
// (also cross-tenant), Forbidden→403 (session mismatch), Conflict→409
// (status!=pending or concurrent resume).
func (a *chatServiceHTTPAdapter) ResumeInterrupt(
	ctx context.Context,
	schemaID, userSub, sessionID, interruptID string,
	payload json.RawMessage,
) (<-chan deliveryhttp.SSEEvent, error) {
	if userSub == "" {
		return nil, pkgerrors.InvalidInput("user_sub is required")
	}
	if sessionID == "" {
		return nil, pkgerrors.InvalidInput("session_id is required for resume_interrupt")
	}
	if interruptID == "" {
		return nil, pkgerrors.InvalidInput("interrupt_id is required")
	}
	if a.interrupts == nil || a.eventStore == nil {
		return nil, fmt.Errorf("interrupt repo not wired (no-DB mode does not support HITL resume)")
	}
	if a.schemas == nil {
		return nil, fmt.Errorf("schema repo not wired")
	}

	// Schema gating mirrors Chat — cross-tenant / chat-disabled → 404.
	schema, err := a.schemas.GetModelByID(ctx, schemaID)
	if err != nil {
		return nil, fmt.Errorf("load schema: %w", err)
	}
	if schema == nil || !schema.ChatEnabled {
		return nil, pkgerrors.NotFound(fmt.Sprintf("schema not found: %s", schemaID))
	}

	// Tenant-scoped lookup of interrupt + its request event row. Returns nil
	// (mapped → NotFound) for cross-tenant ids or when the id is unknown.
	interrupt, requestEvent, err := a.interrupts.LoadWithRequestEvent(ctx, interruptID)
	if err != nil {
		return nil, fmt.Errorf("load interrupt: %w", err)
	}
	if interrupt == nil {
		return nil, pkgerrors.NotFound(fmt.Sprintf("interrupt not found: %s", interruptID))
	}
	if requestEvent.SessionID != sessionID {
		return nil, pkgerrors.Forbidden("interrupt does not belong to this session")
	}
	if interrupt.Status != domain.InterruptStatusPending {
		return nil, pkgerrors.AlreadyExists(fmt.Sprintf("interrupt is %s, cannot resume", interrupt.Status))
	}

	requestProto := &pb.SessionEvent{}
	if err := proto.Unmarshal(requestEvent.ProtoData, requestProto); err != nil {
		return nil, fmt.Errorf("decode request event: %w", err)
	}
	var requestPayload domain.InterruptRequestPayload
	if err := json.Unmarshal([]byte(requestProto.GetContent()), &requestPayload); err != nil {
		return nil, fmt.Errorf("decode interrupt request payload: %w", err)
	}

	resumePayload := domain.InterruptResumePayload{
		InterruptID: interruptID,
		Kind:        requestPayload.Kind,
		Payload:     payload,
	}
	resumeJSON, err := json.Marshal(resumePayload)
	if err != nil {
		return nil, fmt.Errorf("encode resume payload: %w", err)
	}

	resumeProto := &pb.SessionEvent{
		SessionId: sessionID,
		Type:      pb.SessionEventType_SESSION_EVENT_INTERRUPT_RESUME,
		Content:   string(resumeJSON),
		CallId:    interruptID,
	}
	resolveEventID, err := a.eventStore.Append(sessionID, "interrupt_resume", resumeProto, nil)
	if err != nil {
		return nil, fmt.Errorf("persist interrupt_resume event: %w", err)
	}
	resumeProto.EventId = resolveEventID

	resolved, err := a.interrupts.MarkResolved(ctx, interruptID, resolveEventID)
	if err != nil {
		return nil, fmt.Errorf("mark interrupt resolved: %w", err)
	}
	if !resolved {
		return nil, pkgerrors.AlreadyExists("interrupt was resumed by a concurrent request")
	}

	// Best-effort mirror to messages table for reload replay; state of record
	// is interrupts + session_event_log.
	if a.history != nil {
		if histMsg, herr := domain.NewInterruptResumeMessage(sessionID, interruptID, string(resumeJSON)); herr == nil {
			if werr := a.history.Create(ctx, histMsg); werr != nil {
				slog.WarnContext(ctx, "persist interrupt_resume to messages failed",
					"session_id", sessionID, "interrupt_id", interruptID, "error", werr)
			}
		}
	}

	qaText := buildResumeLLMText(requestPayload.Kind, requestPayload.Schema, payload)

	if schema.EntryAgentID == nil || *schema.EntryAgentID == "" {
		return nil, pkgerrors.InvalidInput("schema has no entry agent")
	}
	registry, err := a.resolveRegistry(ctx)
	if err != nil {
		return nil, fmt.Errorf("no agents configured: %w", err)
	}
	entryAgent, err := registry.GetByID(ctx, *schema.EntryAgentID)
	if err != nil {
		return nil, pkgerrors.NotFound(fmt.Sprintf("entry agent not found for schema %s", schemaID))
	}
	agentName := entryAgent.Record.Name
	if !a.registry.HasSession(sessionID) {
		a.registry.CreateSession(sessionID, "", userSub, "", "", agentName)
	}

	eventCh, cleanup := a.registry.Subscribe(sessionID)
	// Sentinel tells processor not to publish this as a user_message (the
	// widget's answered state already represents the answer).
	if err := a.registry.EnqueueMessage(sessionID, sessionprocessor.ResumeMessagePrefix+qaText); err != nil {
		cleanup()
		return nil, fmt.Errorf("enqueue resume message: %w", err)
	}
	a.processor.StartProcessing(ctx, sessionID)

	sseCh := make(chan deliveryhttp.SSEEvent, 64)
	go func() {
		defer close(sseCh)
		defer a.registry.RemoveSession(sessionID)
		defer a.processor.StopProcessing(sessionID)
		defer cleanup()

		// Surface the resume event first so the client marks the widget
		// answered before any subsequent assistant chunks arrive.
		if first := convertSessionEventToSSE(resumeProto, sessionID); first != nil {
			sseCh <- *first
		}

		for protoEvent := range eventCh {
			sseEvent := convertSessionEventToSSE(protoEvent, sessionID)
			if sseEvent == nil {
				continue
			}
			sseCh <- *sseEvent
			if sseEvent.Type == "done" {
				if a.sessions != nil {
					if updateErr := a.sessions.Update(context.Background(), sessionID, map[string]interface{}{"status": "completed"}); updateErr != nil {
						slog.WarnContext(context.Background(), "update chat session status failed", "session_id", sessionID, "error", updateErr)
					}
				}
				return
			}
		}
	}()
	return sseCh, nil
}

// buildResumeLLMText renders the user's interrupt submission as a
// natural-language user turn for the React loop.
func buildResumeLLMText(kind domain.InterruptKind, schemaRaw json.RawMessage, payloadRaw json.RawMessage) string {
	if kind == domain.InterruptKindStructuredOutput {
		return buildStructuredOutputResumeText(schemaRaw, payloadRaw)
	}
	return fmt.Sprintf("User submitted form response: %s", string(payloadRaw))
}

func buildStructuredOutputResumeText(schemaRaw, payloadRaw json.RawMessage) string {
	type schemaQuestion struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}
	type schemaShape struct {
		Title     string           `json:"title"`
		Questions []schemaQuestion `json:"questions"`
	}
	type answerShape struct {
		QuestionID string `json:"question_id"`
		Value      string `json:"value"`
		Label      string `json:"label"`
	}
	type payloadShape struct {
		Answers []answerShape `json:"answers"`
	}

	var sch schemaShape
	_ = json.Unmarshal(schemaRaw, &sch)
	var pl payloadShape
	if err := json.Unmarshal(payloadRaw, &pl); err != nil {
		return fmt.Sprintf("User submitted form response: %s", string(payloadRaw))
	}

	questionLabel := make(map[string]string, len(sch.Questions))
	for _, q := range sch.Questions {
		questionLabel[q.ID] = q.Label
	}

	if len(pl.Answers) == 0 {
		return "User submitted the form (no answers provided)."
	}

	var b []byte
	b = append(b, "User submitted the form:\n"...)
	for _, ans := range pl.Answers {
		qLabel := questionLabel[ans.QuestionID]
		if qLabel == "" {
			qLabel = ans.QuestionID
		}
		aLabel := ans.Label
		if aLabel == "" {
			aLabel = ans.Value
		}
		// Don't double-up '?' when the label already ends with one.
		separator := "? A: "
		if len(qLabel) > 0 && qLabel[len(qLabel)-1] == '?' {
			separator = " A: "
		}
		b = append(b, "Q: "...)
		b = append(b, qLabel...)
		b = append(b, separator...)
		b = append(b, aLabel...)
		b = append(b, '\n')
	}
	return string(b)
}

// convertSessionEventToSSE maps a pb.SessionEvent to an SSEEvent.
// Returns nil for event types that should not be forwarded over SSE.
func convertSessionEventToSSE(event *pb.SessionEvent, sessionID string) *deliveryhttp.SSEEvent {
	switch event.GetType() {
	case pb.SessionEventType_SESSION_EVENT_REASONING:
		return sseEventJSON("thinking", map[string]interface{}{
			"content": event.GetContent(),
		})

	case pb.SessionEventType_SESSION_EVENT_ANSWER_CHUNK:
		return sseEventJSON("message_delta", map[string]interface{}{
			"content": event.GetContent(),
		})

	case pb.SessionEventType_SESSION_EVENT_ANSWER:
		return sseEventJSON("message", map[string]interface{}{
			"content": event.GetContent(),
		})

	case pb.SessionEventType_SESSION_EVENT_TOOL_EXECUTION_START:
		// HITL tool surfaces via interrupt_request — suppress raw tool_call.
		if event.GetToolName() == "show_structured_output" {
			return nil
		}
		data := map[string]interface{}{
			"tool":    event.GetToolName(),
			"call_id": event.GetCallId(),
		}
		if args := event.GetToolArguments(); len(args) > 0 {
			data["arguments"] = args
		}
		return sseEventJSON("tool_call", data)

	case pb.SessionEventType_SESSION_EVENT_TOOL_EXECUTION_END:
		// HITL tool surfaces via interrupt_resume — suppress raw tool_result.
		if event.GetToolName() == "show_structured_output" {
			return nil
		}
		return sseEventJSON("tool_result", map[string]interface{}{
			"tool":      event.GetToolName(),
			"call_id":   event.GetCallId(),
			"content":   event.GetContent(),
			"summary":   event.GetToolResultSummary(),
			"has_error": event.GetToolHasError(),
		})

	case pb.SessionEventType_SESSION_EVENT_ASK_USER:
		data := map[string]interface{}{
			"content": event.GetContent(),
			"call_id": event.GetCallId(),
		}
		if tn := event.GetToolName(); tn != "" {
			data["tool"] = tn
		}
		return sseEventJSON("confirmation", data)

	case pb.SessionEventType_SESSION_EVENT_INTERRUPT_REQUEST:
		// HITL halt — client renders widget from schema, POSTs resume_interrupt.
		// Content is the InterruptRequestPayload JSON; clients parse it client-side.
		return sseEventJSON("interrupt_request", map[string]interface{}{
			"interrupt_id": event.GetCallId(),
			"content":      event.GetContent(),
		})

	case pb.SessionEventType_SESSION_EVENT_INTERRUPT_RESUME:
		// Echo of user's resume submission — client uses interrupt_id to locate
		// the rendered widget and mark it answered (no duplicate user_message bubble).
		return sseEventJSON("interrupt_resume", map[string]interface{}{
			"interrupt_id": event.GetCallId(),
			"content":      event.GetContent(),
		})

	case pb.SessionEventType_SESSION_EVENT_PROCESSING_STOPPED:
		data := map[string]interface{}{
			"session_id": sessionID,
		}
		if content := event.GetContent(); content != "" {
			var tokenData map[string]int
			if err := json.Unmarshal([]byte(content), &tokenData); err == nil {
				if t, ok := tokenData["total_tokens"]; ok && t > 0 {
					data["total_tokens"] = t
				}
				if c, ok := tokenData["context_tokens"]; ok && c > 0 {
					data["context_tokens"] = c
				}
				if p, ok := tokenData["prompt_tokens"]; ok && p > 0 {
					data["prompt_tokens"] = p
				}
				if comp, ok := tokenData["completion_tokens"]; ok && comp > 0 {
					data["completion_tokens"] = comp
				}
				if cached, ok := tokenData["cached_prompt_tokens"]; ok && cached > 0 {
					data["cached_prompt_tokens"] = cached
				}
			} else {
				if tokens, err := strconv.Atoi(content); err == nil && tokens > 0 {
					data["total_tokens"] = tokens
				}
			}
		}
		return sseEventJSON("done", data)

	case pb.SessionEventType_SESSION_EVENT_ERROR:
		data := map[string]interface{}{
			"content": event.GetContent(),
		}
		if detail := event.GetErrorDetail(); detail != nil {
			data["code"] = detail.GetCode()
			data["message"] = detail.GetMessage()
		}
		return sseEventJSON("error", data)

	default:
		return nil
	}
}

// sseEventJSON creates an SSEEvent with JSON-encoded data.
func sseEventJSON(eventType string, data map[string]interface{}) *deliveryhttp.SSEEvent {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		slog.ErrorContext(context.Background(), "failed to marshal SSE event data", "type", eventType, "error", err)
		return nil
	}
	return &deliveryhttp.SSEEvent{
		Type: eventType,
		Data: string(jsonBytes),
	}
}
