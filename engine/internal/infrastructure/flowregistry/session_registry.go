package flowregistry

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/syntheticinc/syntheticbrew/api/proto/gen"
	"github.com/syntheticinc/syntheticbrew/internal/service/eventstore"
)

const (
	// subscriberBuffer is the buffer size for subscriber event channels.
	// Buffered channels prevent deadlock when PublishEvent is called under
	// entry.mu.Lock — without buffering, a slow subscriber blocks the publisher,
	// which holds the lock and blocks all subsequent PublishEvent calls.
	subscriberBuffer = 64

	// publishTimeout is how long PublishEvent waits for a slow subscriber
	// before dropping the event.
	publishTimeout = 5 * time.Second
)

// EventStoreReader reads persisted events for replay on reconnect (consumer-side interface).
type EventStoreReader interface {
	GetAfter(sessionID string, afterCreatedAt time.Time) ([]eventstore.StoredEvent, error)
	GetAll(sessionID string) ([]eventstore.StoredEvent, error)
}

// sessionContext holds metadata for a server-streaming session.
// Note: schema scope is NOT stored here — tool execution resolves it from
// AgentSchemaResolver(agent_name) via turn_executor_factory. Duplicating
// schema on the session would be dead data that nothing reads.
type sessionContext struct {
	ProjectRoot string
	Platform    string
	ProjectKey  string
	UserID      string
	AgentName   string
}

// sessionEntry holds all state for a server-streaming session.
type sessionEntry struct {
	mu             sync.RWMutex
	ctx            sessionContext
	subscribers    map[uint64]chan *pb.SessionEvent
	nextSubID      uint64
	messageCh      chan string // incoming user messages
	askReplies     map[string]chan string
	cancelled      atomic.Bool
	turnCancelFn   context.CancelFunc // cancels the currently running agent turn
	createdAt      time.Time
	lastActivityAt time.Time
}

// SessionInfo represents session metadata returned by ListSessions.
type SessionInfo struct {
	SessionID      string
	ProjectKey     string
	ProjectRoot    string
	Platform       string
	UserID         string
	AgentName      string
	HasAskUser     bool
	IsCancelled    bool
	CreatedAt      time.Time
	LastActivityAt time.Time
}

// SessionRegistry manages server-streaming sessions (subscribe/publish pattern).
// Separate from InMemoryRegistry which manages bidirectional ExecuteFlow sessions.
type SessionRegistry struct {
	mu        sync.RWMutex
	sessions  map[string]*sessionEntry
	eventHook func(sessionID string, event *pb.SessionEvent) // optional hook for broadcasting events externally
	store     EventStoreReader                               // optional, nil-safe — for replay from SQLite
}

// NewSessionRegistry creates a new SessionRegistry.
func NewSessionRegistry(store EventStoreReader) *SessionRegistry {
	return &SessionRegistry{
		sessions: make(map[string]*sessionEntry),
		store:    store,
	}
}

// SetEventHook registers a callback invoked after every PublishEvent.
// Used to wire EventBroadcaster for mobile event delivery.
func (r *SessionRegistry) SetEventHook(hook func(sessionID string, event *pb.SessionEvent)) {
	r.eventHook = hook
}

// CreateSession stores session context for later use.
func (r *SessionRegistry) CreateSession(sessionID, projectKey, userID, projectRoot, platform, agentName string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	r.sessions[sessionID] = &sessionEntry{
		ctx: sessionContext{
			ProjectRoot: projectRoot,
			Platform:    platform,
			ProjectKey:  projectKey,
			UserID:      userID,
			AgentName:   agentName,
		},
		subscribers:    make(map[uint64]chan *pb.SessionEvent),
		messageCh:      make(chan string, 32),
		askReplies:     make(map[string]chan string),
		createdAt:      now,
		lastActivityAt: now,
	}
}

// GetSessionContext returns session metadata.
func (r *SessionRegistry) GetSessionContext(sessionID string) (projectRoot, platform, projectKey, userID, agentName string, ok bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, exists := r.sessions[sessionID]
	if !exists {
		return "", "", "", "", "", false
	}
	return entry.ctx.ProjectRoot, entry.ctx.Platform, entry.ctx.ProjectKey, entry.ctx.UserID, entry.ctx.AgentName, true
}

// Subscribe returns an event channel and a cleanup function.
// The cleanup function MUST be called when the subscriber disconnects.
func (r *SessionRegistry) Subscribe(sessionID string) (<-chan *pb.SessionEvent, func()) {
	r.mu.RLock()
	entry, exists := r.sessions[sessionID]
	r.mu.RUnlock()

	if !exists {
		// Return a closed channel so the caller's select loop exits immediately.
		ch := make(chan *pb.SessionEvent)
		close(ch)
		return ch, func() {}
	}

	ch := make(chan *pb.SessionEvent, subscriberBuffer)

	entry.mu.Lock()
	subID := entry.nextSubID
	entry.nextSubID++
	entry.subscribers[subID] = ch
	entry.mu.Unlock()

	cleanup := func() {
		entry.mu.Lock()
		delete(entry.subscribers, subID)
		entry.mu.Unlock()
	}
	return ch, cleanup
}

// PublishEvent sends an event to all subscribers.
func (r *SessionRegistry) PublishEvent(sessionID string, event *pb.SessionEvent) {
	r.mu.RLock()
	entry, exists := r.sessions[sessionID]
	r.mu.RUnlock()

	if !exists {
		return
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	entry.lastActivityAt = time.Now()

	// Fan-out to subscribers (non-blocking with buffered channels).
	// Subscriber channels are buffered (subscriberBuffer=64) so events queue up
	// without blocking the publisher. If a subscriber is too slow and buffer is
	// full, the event is dropped for that subscriber to prevent deadlock.
	for _, ch := range entry.subscribers {
		select {
		case ch <- event:
		default:
			// Subscriber buffer full — drop event to avoid blocking
		}
	}

	// Notify external hook (e.g., EventBroadcaster for mobile clients)
	if r.eventHook != nil {
		r.eventHook(sessionID, event)
	}
}

// ReplayEvents returns events after the given lastEventID for reconnect.
// If lastEventID is empty, all events are returned.
// Uses event store when available; returns nil otherwise.
func (r *SessionRegistry) ReplayEvents(sessionID string, lastEventID string) []*pb.SessionEvent {
	if r.store == nil {
		return nil
	}

	all, err := r.store.GetAll(sessionID)
	if err != nil {
		slog.ErrorContext(context.Background(), "replay events failed", "session_id", sessionID, "error", err)
		return nil
	}

	if lastEventID == "" {
		protos := make([]*pb.SessionEvent, len(all))
		for i, e := range all {
			protos[i] = e.Proto
		}
		return protos
	}

	// Find the last seen event and return everything after it.
	var result []*pb.SessionEvent
	found := false
	for _, e := range all {
		if found {
			result = append(result, e.Proto)
		}
		if e.ID == lastEventID {
			found = true
		}
	}

	if !found {
		// Unknown lastEventID — return all events (safe fallback).
		protos := make([]*pb.SessionEvent, len(all))
		for i, e := range all {
			protos[i] = e.Proto
		}
		return protos
	}

	return result
}

// EnqueueMessage puts a user message into the session's message channel.
func (r *SessionRegistry) EnqueueMessage(sessionID, content string) error {
	r.mu.RLock()
	entry, exists := r.sessions[sessionID]
	r.mu.RUnlock()

	if !exists {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	select {
	case entry.messageCh <- content:
		return nil
	default:
		return fmt.Errorf("message queue full for session: %s", sessionID)
	}
}

// DequeueMessage returns the next user message (blocks until available or channel closed).
func (r *SessionRegistry) DequeueMessage(sessionID string) (string, bool) {
	r.mu.RLock()
	entry, exists := r.sessions[sessionID]
	r.mu.RUnlock()

	if !exists {
		return "", false
	}

	msg, ok := <-entry.messageCh
	return msg, ok
}

// MessageChannel returns the raw message channel for select-based reading.
func (r *SessionRegistry) MessageChannel(sessionID string) <-chan string {
	r.mu.RLock()
	entry, exists := r.sessions[sessionID]
	r.mu.RUnlock()

	if !exists {
		ch := make(chan string)
		close(ch)
		return ch
	}
	return entry.messageCh
}

// SendAskUserReply delivers a reply to a pending ask_user question.
func (r *SessionRegistry) SendAskUserReply(sessionID, callID, reply string) {
	r.mu.RLock()
	entry, exists := r.sessions[sessionID]
	r.mu.RUnlock()

	if !exists {
		return
	}

	entry.mu.RLock()
	ch, ok := entry.askReplies[callID]
	entry.mu.RUnlock()

	if ok {
		select {
		case ch <- reply:
		default:
		}
	}
}

// RegisterAskUser creates a reply channel for a pending ask_user question.
func (r *SessionRegistry) RegisterAskUser(sessionID, callID string) <-chan string {
	r.mu.RLock()
	entry, exists := r.sessions[sessionID]
	r.mu.RUnlock()

	if !exists {
		ch := make(chan string, 1)
		return ch
	}

	ch := make(chan string, 1)
	entry.mu.Lock()
	entry.askReplies[callID] = ch
	entry.mu.Unlock()
	return ch
}

// UnregisterAskUser removes a reply channel for a completed ask_user question.
func (r *SessionRegistry) UnregisterAskUser(sessionID, callID string) {
	r.mu.RLock()
	entry, exists := r.sessions[sessionID]
	r.mu.RUnlock()

	if !exists {
		return
	}

	entry.mu.Lock()
	delete(entry.askReplies, callID)
	entry.mu.Unlock()
}

// Cancel marks the session as cancelled and interrupts the running agent turn.
func (r *SessionRegistry) Cancel(sessionID string) bool {
	r.mu.RLock()
	entry, exists := r.sessions[sessionID]
	r.mu.RUnlock()

	if !exists {
		return false
	}

	entry.cancelled.Store(true)

	// Interrupt the current turn if it is executing.
	entry.mu.Lock()
	if entry.turnCancelFn != nil {
		entry.turnCancelFn()
	}
	entry.mu.Unlock()

	return true
}

// IsCancelled checks if the session has been cancelled.
func (r *SessionRegistry) IsCancelled(sessionID string) bool {
	r.mu.RLock()
	entry, exists := r.sessions[sessionID]
	r.mu.RUnlock()

	if !exists {
		return false
	}
	return entry.cancelled.Load()
}

// RemoveSession cleans up all session state.
func (r *SessionRegistry) RemoveSession(sessionID string) {
	r.mu.Lock()
	entry, exists := r.sessions[sessionID]
	if exists {
		delete(r.sessions, sessionID)
	}
	r.mu.Unlock()

	if !exists {
		return
	}

	entry.mu.Lock()
	// Close all subscriber channels
	for id, ch := range entry.subscribers {
		close(ch)
		delete(entry.subscribers, id)
	}
	// Close ask reply channels
	for id, ch := range entry.askReplies {
		close(ch)
		delete(entry.askReplies, id)
	}
	entry.mu.Unlock()
}

// DrainMessages discards all pending messages in the session's queue.
func (r *SessionRegistry) DrainMessages(sessionID string) {
	r.mu.RLock()
	entry, exists := r.sessions[sessionID]
	r.mu.RUnlock()

	if !exists {
		return
	}

	for {
		select {
		case <-entry.messageCh:
		default:
			return
		}
	}
}

// ResetCancel clears the cancelled flag so the session can accept new messages.
func (r *SessionRegistry) ResetCancel(sessionID string) {
	r.mu.RLock()
	entry, exists := r.sessions[sessionID]
	r.mu.RUnlock()

	if !exists {
		return
	}

	entry.cancelled.Store(false)
}

// StoreTurnCancel stores a cancel function for the currently running turn.
// Pass nil to clear it after the turn completes.
func (r *SessionRegistry) StoreTurnCancel(sessionID string, cancel context.CancelFunc) {
	r.mu.RLock()
	entry, exists := r.sessions[sessionID]
	r.mu.RUnlock()

	if !exists {
		return
	}

	entry.mu.Lock()
	entry.turnCancelFn = cancel
	entry.mu.Unlock()
}

// HasSession checks if a session exists.
func (r *SessionRegistry) HasSession(sessionID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.sessions[sessionID]
	return exists
}

// ListSessions returns metadata for all active sessions.
func (r *SessionRegistry) ListSessions() []SessionInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]SessionInfo, 0, len(r.sessions))
	for id, entry := range r.sessions {
		entry.mu.RLock()
		hasAskUser := len(entry.askReplies) > 0
		lastActivity := entry.lastActivityAt
		entry.mu.RUnlock()

		result = append(result, SessionInfo{
			SessionID:      id,
			ProjectKey:     entry.ctx.ProjectKey,
			ProjectRoot:    entry.ctx.ProjectRoot,
			Platform:       entry.ctx.Platform,
			UserID:         entry.ctx.UserID,
			AgentName:      entry.ctx.AgentName,
			HasAskUser:     hasAskUser,
			IsCancelled:    entry.cancelled.Load(),
			CreatedAt:      entry.createdAt,
			LastActivityAt: lastActivity,
		})
	}
	return result
}

