package sessionprocessor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	pb "github.com/syntheticinc/syntheticbrew/api/proto/gen"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools"
	"github.com/syntheticinc/syntheticbrew/internal/service/orchestrator"
)

// SessionRegistry provides session context and message channel (consumer-side interface).
//
// RegisterAskUser/UnregisterAskUser/SendAskUserReply remain on the registry
// because confirm_before reuses the same per-session reply-channel mechanism
// (callID → channel) to await the user's confirm/cancel decision. The names
// are historical — the legacy ask_user tool that originally needed them was
// replaced by show_structured_output(form), which is non-blocking and does
// not register a reply channel.
type SessionRegistry interface {
	GetSessionContext(sessionID string) (projectRoot, platform, projectKey, userID, agentName string, ok bool)
	MessageChannel(sessionID string) <-chan string
	PublishEvent(sessionID string, event *pb.SessionEvent)
	ResetCancel(sessionID string)
	StoreTurnCancel(sessionID string, cancel context.CancelFunc)
	HasSession(sessionID string) bool
	RegisterAskUser(sessionID, callID string) <-chan string
	UnregisterAskUser(sessionID, callID string)
}

// TurnExecutorFactory creates a TurnExecutor for a given session (consumer-side interface).
//
// `ctx` carries per-request values (notably BYOK credentials extracted by
// the BYOK middleware via llm.BYOKCredentialsFrom). Pass context.Background()
// in code paths that have no per-request context.
type TurnExecutorFactory interface {
	CreateForSession(ctx context.Context, proxy tools.ClientOperationsProxy, sessionID, projectKey, projectRoot, platform, agentName, userID string) orchestrator.TurnExecutor
}

// AgentPoolRegistrar registers per-session resources on the AgentPool (consumer-side interface).
// Used to deliver lifecycle events and provide proxy for code agent tool execution.
type AgentPoolRegistrar interface {
	SetEventCallbackForSession(sessionID string, cb func(event *domain.AgentEvent) error)
	SetProxyForSession(sessionID string, proxy interface{})
	RemoveSession(sessionID string)
}

// Processor runs background message-processing loops for server-streaming sessions.
// It is shared between gRPC SubscribeSession and bridge MobileRequestHandler.
type Processor struct {
	registry           SessionRegistry
	factory            TurnExecutorFactory
	agentPoolRegistrar AgentPoolRegistrar // optional, nil-safe
	eventStore         EventStore         // persists events for reliable replay
	interrupts         InterruptCreator   // optional, nil-safe

	mu          sync.Mutex
	active      map[string]context.CancelFunc
	turnsActive map[string]bool // sessions with an actively executing turn
}

// New creates a new Processor.
// interrupts may be nil for tests / no-DB mode; HITL state-tracker rows are
// then skipped (events still publish to the client).
func New(registry SessionRegistry, factory TurnExecutorFactory, eventStore EventStore, interrupts InterruptCreator) *Processor {
	return &Processor{
		registry:    registry,
		factory:     factory,
		eventStore:  eventStore,
		interrupts:  interrupts,
		active:      make(map[string]context.CancelFunc),
		turnsActive: make(map[string]bool),
	}
}

// SetAgentPoolRegistrar sets the registrar for agent pool resources.
// When set, processMessage will register event callbacks and proxy on the AgentPool
// so that lifecycle events reach WS/mobile clients and code agents can execute tools.
func (p *Processor) SetAgentPoolRegistrar(registrar AgentPoolRegistrar) {
	p.agentPoolRegistrar = registrar
}

// StartProcessing launches the message processing loop for a session.
// Idempotent: if already running for this session, does nothing.
func (p *Processor) StartProcessing(ctx context.Context, sessionID string) {
	p.mu.Lock()
	if _, exists := p.active[sessionID]; exists {
		p.mu.Unlock()
		return
	}

	// Use context.Background() — processing must NOT be tied to the HTTP
	// request context. The HTTP handler may return (e.g., after SSE flush)
	// while the LLM is still generating. If we used ctx here, the request
	// cancellation would kill the LLM turn ("turn cancelled by user").
	// Values from the original context (RequestContext for MCP headers) are
	// copied via context.WithoutCancel if available, otherwise Background.
	procCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	p.active[sessionID] = cancel
	p.mu.Unlock()

	go p.processMessages(procCtx, sessionID)
}

// StopProcessing stops the message processing loop for a session.
func (p *Processor) StopProcessing(sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if cancel, exists := p.active[sessionID]; exists {
		cancel()
		delete(p.active, sessionID)
	}
}

// IsProcessing returns true if a processing loop is active for the session.
func (p *Processor) IsProcessing(sessionID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, exists := p.active[sessionID]
	return exists
}

// IsTurnActive returns true if a turn (message processing) is currently executing.
// Unlike IsProcessing which tracks the background loop, this tracks the actual
// turn execution between ProcessingStarted and ProcessingStopped.
func (p *Processor) IsTurnActive(sessionID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.turnsActive[sessionID]
}

func (p *Processor) processMessages(ctx context.Context, sessionID string) {
	defer func() {
		p.mu.Lock()
		delete(p.active, sessionID)
		p.mu.Unlock()
	}()

	msgCh := p.registry.MessageChannel(sessionID)

	for {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-msgCh:
			if !ok {
				return
			}
			p.processMessage(ctx, sessionID, message)
		}
	}
}

// ResumeMessagePrefix marks an enqueued message as a HITL resume Q+A so
// processMessage skips the user_message broadcast (the widget's answered
// state already shows the user's answer).
const ResumeMessagePrefix = "\x00bb-resume\x00"

func (p *Processor) processMessage(ctx context.Context, sessionID, message string) {
	projectRoot, platform, projectKey, userID, agentName, ok := p.registry.GetSessionContext(sessionID)
	if !ok {
		slog.ErrorContext(ctx, "[SessionProcessor] session context not found", "session_id", sessionID)
		return
	}

	p.mu.Lock()
	p.turnsActive[sessionID] = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.turnsActive, sessionID)
		p.mu.Unlock()
	}()

	eventStream := NewEventStream(ctx, sessionID, p.registry, p.eventStore, p.interrupts)

	// Resume turns must not surface as user_message (SSE or messages-table) —
	// the widget's answered state already represents the user's answer.
	if strings.HasPrefix(message, ResumeMessagePrefix) {
		message = strings.TrimPrefix(message, ResumeMessagePrefix)
		ctx = domain.WithResumeTurn(ctx)
	} else {
		eventStream.PublishUserMessage(message)
	}

	eventStream.PublishProcessingStarted()

	// confirm_before tools share the per-session reply-channel mechanism with
	// the legacy ask_user handler that lived here previously. ask_user has
	// been removed (replaced by show_structured_output in non-blocking form
	// mode); the underlying RegisterAskUser/SendAskUserReply primitives are
	// retained because confirm_before still needs them.
	confirmRequester := &sseConfirmationRequester{
		sessionID:   sessionID,
		registry:    p.registry,
		eventStream: eventStream,
	}
	proxy := tools.NewInProcessProxy(
		tools.WithConfirmRequester(confirmRequester),
	)
	defer proxy.Dispose()

	turnExecutor := p.factory.CreateForSession(ctx, proxy, sessionID, projectKey, projectRoot, platform, agentName, userID)
	if turnExecutor == nil {
		slog.ErrorContext(ctx, "[SessionProcessor] failed to create turn executor — check model configuration in Admin Dashboard",
			"session_id", sessionID, "agent", agentName)
		eventStream.PublishError(fmt.Errorf("no model available for agent %q — configure a model via Admin Dashboard", agentName))
		eventStream.PublishProcessingStopped()
		return
	}

	chunkCallback := func(chunk string) error {
		eventStream.PublishAnswerChunk(chunk)
		return nil
	}

	eventCallback := func(event *domain.AgentEvent) error {
		return eventStream.Send(event)
	}

	// Register proxy and lifecycle callback on AgentPool so code agents can
	// execute tools and lifecycle events reach WS/mobile clients.
	if p.agentPoolRegistrar != nil {
		p.agentPoolRegistrar.SetProxyForSession(sessionID, proxy)
		p.agentPoolRegistrar.SetEventCallbackForSession(sessionID, eventCallback)
		defer p.agentPoolRegistrar.RemoveSession(sessionID)
	}

	turnCtx, turnCancel := context.WithCancel(ctx)
	defer turnCancel()

	p.registry.StoreTurnCancel(sessionID, turnCancel)
	defer p.registry.StoreTurnCancel(sessionID, nil)

	err := turnExecutor.ExecuteTurn(turnCtx, sessionID, projectKey, message, chunkCallback, eventCallback)

	p.registry.ResetCancel(sessionID)

	if err != nil {
		if turnCtx.Err() != nil {
			slog.InfoContext(ctx, "[SessionProcessor] turn cancelled by user", "session_id", sessionID)
		} else {
			slog.ErrorContext(ctx, "[SessionProcessor] turn execution failed", "session_id", sessionID, "error", err)
			eventStream.PublishError(err)
		}
	}

	eventStream.PublishProcessingStopped()
}

// sseConfirmationRequester implements tools.ConfirmationRequester for the SSE path.
// It sends a confirmation event to the client and waits for user response.
type sseConfirmationRequester struct {
	sessionID   string
	registry    SessionRegistry
	eventStream *EventStream
}

func (r *sseConfirmationRequester) RequestConfirmation(ctx context.Context, toolName string, args string) (bool, error) {
	callID := fmt.Sprintf("confirm-%d", time.Now().UnixNano())
	replyCh := r.registry.RegisterAskUser(r.sessionID, callID)
	defer r.registry.UnregisterAskUser(r.sessionID, callID)

	question := fmt.Sprintf("Confirm execution of %s with arguments: %s", toolName, args)

	r.eventStream.Send(&domain.AgentEvent{
		Type:    domain.EventTypeUserQuestion,
		Content: question,
		Metadata: map[string]interface{}{
			"call_id":   callID,
			"tool_name": toolName,
		},
	})

	askTimeout := 60 * time.Second
	select {
	case reply := <-replyCh:
		lower := strings.ToLower(strings.TrimSpace(reply))
		denied := lower == "cancel" || lower == "no" || lower == "deny" || lower == "reject" || lower == "cancelled"
		return !denied, nil
	case <-time.After(askTimeout):
		slog.WarnContext(ctx, "[SSEConfirmationRequester] timed out", "session_id", r.sessionID, "tool", toolName)
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}
