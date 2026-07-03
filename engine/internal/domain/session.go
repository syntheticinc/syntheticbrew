package domain

import (
	"context"
	"fmt"
	"time"
)

// --- Session id context key ---

type sessionIDCtxKey struct{}

// WithSessionID returns a context carrying the current turn's session id.
// Attached at turn start so observers deep in the agent callback chain (which
// have no session id of their own) can correlate per-step signals back to the
// session — e.g. the usage-limit step accumulator.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionIDCtxKey{}, sessionID)
}

// SessionIDFromContext extracts the session id from context, or "" when absent.
func SessionIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(sessionIDCtxKey{}).(string)
	return v
}

// SessionStatus represents the lifecycle stage of a session.
// Values must match target-schema.dbml sessions.status CHECK:
//
//	active | completed | expired | failed
type SessionStatus string

const (
	SessionActive    SessionStatus = "active"
	SessionCompleted SessionStatus = "completed"
	SessionExpired   SessionStatus = "expired"
	SessionFailed    SessionStatus = "failed"
)

// Session represents a user session that can persist across server restarts
type Session struct {
	ID             string
	ProjectKey     string
	Status         SessionStatus
	TenantID       string
	SchemaID       string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastActivityAt time.Time
}

// NewSession creates a new Session with validation
func NewSession(id, projectKey string) (*Session, error) {
	now := time.Now()
	session := &Session{
		ID:             id,
		ProjectKey:     projectKey,
		Status:         SessionActive,
		CreatedAt:      now,
		UpdatedAt:      now,
		LastActivityAt: now,
	}

	if err := session.Validate(); err != nil {
		return nil, err
	}

	return session, nil
}

// Validate validates the Session
func (s *Session) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("session id is required")
	}
	if s.ProjectKey == "" {
		return fmt.Errorf("project_key is required")
	}

	switch s.Status {
	case SessionActive, SessionCompleted, SessionExpired, SessionFailed:
		// Valid
	default:
		return fmt.Errorf("invalid session status: %s", s.Status)
	}

	return nil
}

// Activate transitions session to active status
func (s *Session) Activate() {
	s.Status = SessionActive
	s.UpdatedAt = time.Now()
	s.LastActivityAt = time.Now()
}

// Expire transitions session to expired status (replaces Suspend —
// "suspended" is not a valid DBML value for sessions.status).
func (s *Session) Expire() {
	s.Status = SessionExpired
	s.UpdatedAt = time.Now()
}

// Fail transitions session to failed status.
func (s *Session) Fail() {
	s.Status = SessionFailed
	s.UpdatedAt = time.Now()
}

// Complete transitions session to completed status
func (s *Session) Complete() {
	s.Status = SessionCompleted
	s.UpdatedAt = time.Now()
}

// TouchActivity updates last activity timestamp
func (s *Session) TouchActivity() {
	s.LastActivityAt = time.Now()
	s.UpdatedAt = time.Now()
}

// IsTerminal returns true if the session is in a terminal state.
// Completed, expired, and failed are all terminal — only "active" allows
// further activity.
func (s *Session) IsTerminal() bool {
	return s.Status == SessionCompleted ||
		s.Status == SessionExpired ||
		s.Status == SessionFailed
}
