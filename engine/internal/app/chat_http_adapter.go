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
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"github.com/syntheticinc/syntheticbrew/internal/service/sessionprocessor"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// usageGate is the pre-turn gate + post-turn settle the chat adapter depends
// on. Defined consumer-side so the adapter can be unit-tested against a fake;
// the concrete *usagelimit.Enforcer satisfies it.
type usageGate interface {
	CheckAllowed(ctx context.Context, userSub string) (domain.UsageDecision, error)
	RecordTurn(ctx context.Context, userSub string, steps int) error
	// RecordSteps settles a HITL resume: it consumes step budget without
	// counting a new turn (the resume continues the same turn).
	RecordSteps(ctx context.Context, userSub string, steps int) error
}

// activeUsersGate caps DISTINCT end users per window. Unlike usageGate it runs
// for BYOK turns too: it limits platform activity (a user existing), not whose
// model key pays for the turn. Defined consumer-side; the concrete
// *activeusers.Gate satisfies it.
//
// Trust model for the identity it counts: for authenticated end-users the
// user_sub is a verified JWT subject. For anonymous widget traffic the user_sub
// is "<token-name>:<visitor-id>" where the visitor-id is client-asserted (the
// widget stores it in localStorage). A holder of the public widget key can
// therefore rotate the visitor-id to mint fresh identities, so the distinct-
// user count for anonymous traffic is best-effort: it can be inflated (never
// deflated — rotation only adds), and per_user usage limits degrade to
// advisory for anonymous widgets. Abuse is bounded by the edge per-IP rate
// limit (an infra concern); enforce-mode blocking affects only the tenant's
// own scope, and an operator can configure the gate in monitor mode (count
// without blocking) instead of enforce.
type activeUsersGate interface {
	Check(ctx context.Context, userSub string) (domain.ActiveUsersDecision, error)
	RecordActivity(ctx context.Context, userSub string) error
}

// isOperatorChat reports whether the request is the operator's own builder-
// assistant rather than a deployment end-user. The admin builder-assistant
// runs through the same chat service; operators must not count toward or be
// blocked by the end-user limit. This is a route-level marker, NOT actor-based:
// JWT-authenticated end users (e.g. behind the identity broker) present as the
// "admin" actor too, so only the explicit marker set by the admin-assistant
// handler distinguishes operator traffic.
func isOperatorChat(ctx context.Context) bool {
	return deliveryhttp.IsOperatorChat(ctx)
}

// stepTaker registers and drains the per-session step count accumulated during a
// turn. The concrete *usagelimit.StepAccumulator satisfies it. Begin scopes
// accumulation to owners that settle, so non-settling step sources never leak.
type stepTaker interface {
	Begin(sessionID string)
	Take(sessionID string) int
	Discard(sessionID string)
}

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
	registryMgr *agentregistry.Manager       // non-nil in multi-tenant mode
	schemas     schemaChatRepo               // optional — nil in tests / no-DB mode
	sessions    chatSessionPersister         // optional — nil when no DB
	chatEnabled bool                         // false when no LLM model configured
	interrupts  interruptResumeRepo
	eventStore  resumeEventStore
	history     interruptResumeHistory // mirrors interrupt_resume into messages table for reload restore

	// usage gates a turn before it runs and settles the counters once it
	// completes. accumulator carries the per-session step count the settle
	// records. Both are nil in no-DB mode, in which case gating/settling is
	// skipped entirely.
	usage       usageGate
	accumulator stepTaker

	// activeUsers caps distinct end users per rolling window. Nil in no-DB
	// mode, in which case the gate is skipped entirely.
	activeUsers activeUsersGate
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

	// BYOK turns run on the end user's own model key, so they neither consume
	// nor are gated by the operator's usage limits — bringing a key is the
	// documented way to run without limits.
	byok := llm.BYOKCredentialsFrom(ctx) != nil

	if err := a.gateTurn(ctx, userSub, byok); err != nil {
		return nil, err
	}

	// Register this session so the global step callback attributes steps to it;
	// the settle (below) drains it. BYOK turns are neither counted nor Begin'd.
	if a.accumulator != nil && !byok {
		a.accumulator.Begin(sessionID)
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
		// Settle the turn once event fan-out ends. A turn that produced real
		// output/interrupt is billable → record it with its exact step count;
		// one that stopped before any output (errored early) is discarded so
		// its steps don't leak into the next turn on this session. BYOK turns
		// are neither gated nor settled.
		sawOutput := false
		defer a.settleTurn(ctx, sessionID, userSub, byok, &sawOutput)

		for protoEvent := range eventCh {
			sseEvent := convertSessionEventToSSE(protoEvent, sessionID)
			if sseEvent == nil {
				continue
			}
			if isBillableOutput(sseEvent.Type) {
				sawOutput = true
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

// isBillableOutput reports whether an SSE event type represents real output
// the user received: a streamed/complete answer or a HITL interrupt. A turn
// that emits any of these is settled against the usage counters.
func isBillableOutput(sseType string) bool {
	switch sseType {
	case "message_delta", "message", "interrupt_request":
		return true
	default:
		return false
	}
}

// gateTurn is the pre-turn gate: it blocks the turn with a UsageLimited
// (HTTP 402) error when the active-users limit or a configured usage limit is
// exhausted. The active-users check applies to every turn; the usage (turn/
// step) check is skipped when usage limiting is not wired or for BYOK turns
// (which run on the user's own key).
//
// This is a check-then-settle gate, not a reservation: at the exact boundary
// (used == limit-1) N turns that start concurrently can all pass the gate
// before any of them settles, so the enforced count can overshoot the limit by
// up to the request concurrency — once per window, never unbounded (the counter
// upsert is atomic; the next window resets it). This soft over-allowance is an
// accepted trade-off for a usage limit; a hard cap would need a reserve-before-
// serve counter.
func (a *chatServiceHTTPAdapter) gateTurn(ctx context.Context, userSub string, byok bool) error {
	// The active-users gate runs first and unconditionally — including for
	// BYOK turns: it caps distinct end users existing on the platform, not
	// whose model key pays for the turn. Same accepted check-then-settle race
	// as the turn gate below: concurrent first turns of new users can
	// overshoot the limit by the request concurrency, once per window.
	//
	// Operator traffic is exempt: the admin builder-assistant runs through this
	// same service, but the operator is not one of the deployment's END users
	// and must never be counted toward — or blocked by — the active-user limit.
	if a.activeUsers != nil && !isOperatorChat(ctx) {
		dec, err := a.activeUsers.Check(ctx, userSub)
		if err != nil {
			return fmt.Errorf("check user limit: %w", err)
		}
		if !dec.Allowed {
			return pkgerrors.UsageLimited(fmt.Sprintf(
				"user limit reached: %d/%d active users in the current period — existing users can continue",
				dec.Used, dec.Limit))
		}
	}

	if a.usage == nil || byok {
		return nil
	}
	dec, err := a.usage.CheckAllowed(ctx, userSub)
	if err != nil {
		return fmt.Errorf("check usage limit: %w", err)
	}
	if !dec.Allowed {
		return pkgerrors.UsageLimited(fmt.Sprintf(
			"usage limit reached: %d/%d %s for this %s — bring your own model key to continue",
			dec.Used, dec.Limit, dec.Unit, dec.BlockedScope))
	}
	return nil
}

// settleTurn settles once event fan-out ends: it records the user's activity
// (output-producing turns only, BYOK included) and then records or discards
// the turn's accumulated steps — the step settle alone is skipped when usage
// limiting is not wired or for BYOK turns. Writes run on a cancel-detached
// context so a client disconnect (which cancels ctx as the stream closes)
// does not abort them.
func (a *chatServiceHTTPAdapter) settleTurn(ctx context.Context, sessionID, userSub string, byok bool, sawOutput *bool) {
	// Activity settles for BYOK turns too (mirror of the unconditional check
	// in gateTurn), but only when the turn produced real output — a turn that
	// errored before any output must not mint an active user. Operator traffic
	// (admin builder-assistant) is exempt, mirroring the gate.
	if a.activeUsers != nil && *sawOutput && !isOperatorChat(ctx) {
		if err := a.activeUsers.RecordActivity(context.WithoutCancel(ctx), userSub); err != nil {
			slog.WarnContext(ctx, "record user activity failed", "session_id", sessionID, "error", err)
		}
	}

	if a.usage == nil || a.accumulator == nil || byok {
		return
	}
	if !*sawOutput {
		a.accumulator.Discard(sessionID)
		return
	}
	steps := a.accumulator.Take(sessionID)
	if err := a.usage.RecordTurn(context.WithoutCancel(ctx), userSub, steps); err != nil {
		slog.WarnContext(ctx, "record usage turn failed", "session_id", sessionID, "steps", steps, "error", err)
	}
}

// settleResumeSteps refreshes the user's activity, then drains and records a
// HITL resume's steps against the usage counters WITHOUT counting a new turn
// (turnsDelta 0). The step settle alone is skipped when usage limiting is not
// wired or for BYOK resumes.
func (a *chatServiceHTTPAdapter) settleResumeSteps(ctx context.Context, sessionID, userSub string, byok bool) {
	// A resume implies the user already received an interrupt (real output in
	// a prior settle), so refresh their activity unconditionally — BYOK too,
	// mirroring gateTurn/settleTurn.
	if a.activeUsers != nil {
		if err := a.activeUsers.RecordActivity(context.WithoutCancel(ctx), userSub); err != nil {
			slog.WarnContext(ctx, "record user activity failed", "session_id", sessionID, "error", err)
		}
	}

	if a.usage == nil || a.accumulator == nil || byok {
		return
	}
	steps := a.accumulator.Take(sessionID)
	if steps == 0 {
		return
	}
	if err := a.usage.RecordSteps(ctx, userSub, steps); err != nil {
		slog.WarnContext(ctx, "record usage resume steps failed", "session_id", sessionID, "steps", steps, "error", err)
	}
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

	// Resume continues an existing turn, but it runs a fresh, max_steps-bounded
	// React pass, so it must not be a free unbounded-usage lane: gate it (a
	// caller already over the limit cannot pump pre-stuffed interrupts) and
	// settle its steps below (turnsDelta 0 — no double-counting the wizard as a
	// new turn). BYOK resumes run on the user's own key: neither gated nor counted.
	byok := llm.BYOKCredentialsFrom(ctx) != nil
	if err := a.gateTurn(ctx, userSub, byok); err != nil {
		return nil, err
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

	// Register so the resume's steps are attributed to this session and settled
	// below. BYOK resumes are neither counted nor Begin'd.
	if a.accumulator != nil && !byok {
		a.accumulator.Begin(sessionID)
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
		// Settle the resume as steps-only (turnsDelta 0): a HITL wizard stays
		// ONE turn, but its resume work consumes step budget so resume-chaining
		// cannot do unbounded LLM work for free. Runs on a cancel-detached
		// context so a client disconnect does not abort the counter write.
		defer a.settleResumeSteps(context.WithoutCancel(ctx), sessionID, userSub, byok)

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
