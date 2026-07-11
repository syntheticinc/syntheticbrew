package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// sessionACL captures actor identity + privilege flags relevant for /sessions
// per-user filtering. It is derived from request context once at the top of
// each handler and passed to access checks.
//
// trustedProxy actors (api_token) read tenant-wide because they sit between
// engine and many end-users (chirp's ai-assistant pattern). isAdmin actors
// (ScopeAdmin bit set) bypass per-user filter for tooling. Everyone else
// — regular end-user JWT — is force-scoped to their own user_sub regardless
// of any ?user_sub URL override (see chirp 1.1.3 audit).
type sessionACL struct {
	actorType    string // "api_token" | "admin" | ""
	userSub      string // canonical identity from auth ctx
	isAdmin      bool   // ScopeAdmin & scopes != 0
	trustedProxy bool   // actorType == "api_token"
}

// extractSessionACL builds the ACL view from request context. Auth middleware
// always populates ContextKeyActorType + ContextKeyScopes for authenticated
// routes; UserSub is populated for both api_token (1.1.4 fix) and admin JWT.
func extractSessionACL(r *http.Request) sessionACL {
	ctx := r.Context()
	actorType, _ := ctx.Value(ContextKeyActorType).(string)
	scopes, _ := ctx.Value(ContextKeyScopes).(int)
	return sessionACL{
		actorType:    actorType,
		userSub:      domain.UserSubFromContext(ctx),
		isAdmin:      scopes&ScopeAdmin != 0,
		trustedProxy: actorType == "api_token",
	}
}

// canSeeAllUsers returns true when the actor is allowed to read sessions
// across user_sub values inside the tenant (admin tooling, trusted proxies).
func (a sessionACL) canSeeAllUsers() bool { return a.isAdmin || a.trustedProxy }

// effectiveUserSubFilter returns the user_sub the handler should pass to the
// service for List operations. For unrestricted actors the URL-supplied
// override is honoured (or empty = tenant-wide); for restricted actors the
// override is ignored and the caller's own user_sub is forced.
func (a sessionACL) effectiveUserSubFilter(urlUserSub string) string {
	if a.canSeeAllUsers() {
		return urlUserSub
	}
	return a.userSub
}

// allowSession returns true iff the caller is allowed to view/mutate the
// given session row. Cross-user access by non-admin / non-proxy actors is
// rejected with information hiding (404, never 403) per security checklist
// SCC-02.
func (a sessionACL) allowSession(sessionUserSub string) bool {
	if a.canSeeAllUsers() {
		return true
	}
	return a.userSub != "" && a.userSub == sessionUserSub
}

// SessionResponse is the API representation of a session.
type SessionResponse struct {
	ID        string          `json:"id"`
	Title     string          `json:"title,omitempty"`
	SchemaID  string          `json:"schema_id,omitempty"`
	UserSub   string          `json:"user_sub,omitempty"`
	Status    string          `json:"status"`
	// Metadata is opaque per-session JSON storage. Engine never reads or
	// interprets the contents — clients can use it to attach their own
	// org/user mapping (e.g. multi-tenant ai-assistant proxies on top of a
	// single SyntheticBrew tenant). Default is `{}`. Capped at 16KB on writes.
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

// SessionPaginationMaxPerPage is the server-enforced upper bound on the
// `per_page` query parameter for GET /api/v1/sessions. Surfaced in
// PaginatedSessionResponse.PerPageMax so programmatic consumers (e.g. thin
// proxies that page through the entire list) can detect runaway loops if
// their own math drifts from the server's effective bound.
const SessionPaginationMaxPerPage = 100

// PaginatedSessionResponse wraps a page of sessions with pagination metadata.
type PaginatedSessionResponse struct {
	Data       []SessionResponse `json:"data"`
	Total      int64             `json:"total"`
	Page       int               `json:"page"`
	PerPage    int               `json:"per_page"`
	PerPageMax int               `json:"per_page_max"`
	TotalPages int               `json:"total_pages"`
}

// CreateSessionRequest is the body for POST /api/v1/sessions.
type CreateSessionRequest struct {
	ID       string          `json:"id,omitempty"`
	Title    string          `json:"title,omitempty"`
	SchemaID string          `json:"schema_id,omitempty"`
	UserSub  string          `json:"user_sub,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// UpdateSessionRequest is the body for PUT /api/v1/sessions/{id}.
type UpdateSessionRequest struct {
	Title    *string         `json:"title,omitempty"`
	Status   *string         `json:"status,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// SessionMetadataMaxBytes is the upper bound on per-session JSON metadata.
// Engine treats the field as opaque storage; clients should not exceed this
// per call. 16KB is a generous envelope for opaque per-session client
// metadata.
const SessionMetadataMaxBytes = 16 * 1024

// SessionService provides session CRUD operations.
type SessionService interface {
	ListSessions(ctx context.Context, agentName, userSub, status, from, to string, page, perPage int) ([]SessionResponse, int64, error)
	GetSession(ctx context.Context, id string) (*SessionResponse, error)
	CreateSession(ctx context.Context, req CreateSessionRequest) (*SessionResponse, error)
	UpdateSession(ctx context.Context, id string, req UpdateSessionRequest) (*SessionResponse, error)
	DeleteSession(ctx context.Context, id string) error
}

// EventResponse is the API representation of a session event (message, tool call, reasoning, etc.).
type EventResponse struct {
	ID        string          `json:"id"`
	EventType string          `json:"event_type"`
	AgentID   string          `json:"agent_id,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt string          `json:"created_at"`
}

// EventService provides event query operations for a session.
type EventService interface {
	ListEvents(ctx context.Context, sessionID string) ([]EventResponse, error)
}

// SessionHandler serves /api/v1/sessions endpoints.
type SessionHandler struct {
	service    SessionService
	eventSvc EventService
}

// NewSessionHandler creates a SessionHandler.
func NewSessionHandler(service SessionService) *SessionHandler {
	return &SessionHandler{service: service}
}

// SetEventService sets the optional EventService for listing chat history.
func (h *SessionHandler) SetEventService(svc EventService) {
	h.eventSvc = svc
}

// List handles GET /api/v1/sessions.
//
// Per-user filtering is applied via sessionACL.effectiveUserSubFilter:
//   - api_token actors (trusted proxy): URL ?user_sub honoured (or empty = tenant-wide).
//   - ScopeAdmin actors: same.
//   - Regular end-user JWT actors: ?user_sub IGNORED, scoped to ctx user_sub.
//
// This prevents the chirp 1.1.3 cross-user enumeration where
// `?user_sub=victim` against an end-user JWT would have returned victim's
// sessions inside the same tenant.
func (h *SessionHandler) List(w http.ResponseWriter, r *http.Request) {
	acl := extractSessionACL(r)
	agentName := r.URL.Query().Get("agent_name")
	userSub := acl.effectiveUserSubFilter(r.URL.Query().Get("user_sub"))
	status := r.URL.Query().Get("status")
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")

	page := 1
	perPage := 20

	if v := r.URL.Query().Get("page"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			page = p
		}
	}
	if v := r.URL.Query().Get("per_page"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			if p > SessionPaginationMaxPerPage {
				p = SessionPaginationMaxPerPage
			}
			perPage = p
		}
	}

	sessions, total, err := h.service.ListSessions(r.Context(), agentName, userSub, status, from, to, page, perPage)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	totalPages := int(total) / perPage
	if int(total)%perPage != 0 {
		totalPages++
	}

	writeJSON(w, http.StatusOK, PaginatedSessionResponse{
		Data:       sessions,
		Total:      total,
		Page:       page,
		PerPage:    perPage,
		PerPageMax: SessionPaginationMaxPerPage,
		TotalPages: totalPages,
	})
}

func parseSessionID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid session id: must be a UUID")
		return "", false
	}
	return id, true
}

// Get handles GET /api/v1/sessions/{id}.
//
// Cross-user access is rejected with 404 (information hiding — same code
// as truly-not-found, so an attacker cannot probe session UUID existence
// to discover which IDs belong to other users in the tenant).
func (h *SessionHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseSessionID(w, r)
	if !ok {
		return
	}

	acl := extractSessionACL(r)
	session, err := h.service.GetSession(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if session == nil || !acl.allowSession(session.UserSub) {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("session not found: %s", id))
		return
	}

	writeJSON(w, http.StatusOK, session)
}

// Create handles POST /api/v1/sessions.
//
// Non-admin / non-proxy actors cannot create a session attributed to a
// different user_sub: the body field is silently overwritten with the
// caller's identity (matches the chat-handler impersonation guard).
func (h *SessionHandler) Create(w http.ResponseWriter, r *http.Request) {
	acl := extractSessionACL(r)
	var req CreateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}
	if len(req.Metadata) > SessionMetadataMaxBytes {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("metadata exceeds %d bytes", SessionMetadataMaxBytes))
		return
	}
	if !acl.canSeeAllUsers() {
		req.UserSub = acl.userSub
	}
	session, err := h.service.CreateSession(r.Context(), req)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, session)
}

// Update handles PUT /api/v1/sessions/{id}.
//
// Pre-fetches the session to check ownership before mutating; non-trusted
// callers attempting to modify another user's session get 404, never a
// success that silently no-ops.
func (h *SessionHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseSessionID(w, r)
	if !ok {
		return
	}

	acl := extractSessionACL(r)
	existing, err := h.service.GetSession(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if existing == nil || !acl.allowSession(existing.UserSub) {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("session not found: %s", id))
		return
	}

	var req UpdateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}
	if len(req.Metadata) > SessionMetadataMaxBytes {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("metadata exceeds %d bytes", SessionMetadataMaxBytes))
		return
	}

	session, err := h.service.UpdateSession(r.Context(), id, req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if session == nil {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("session not found: %s", id))
		return
	}

	writeJSON(w, http.StatusOK, session)
}

// Delete handles DELETE /api/v1/sessions/{id}.
func (h *SessionHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseSessionID(w, r)
	if !ok {
		return
	}

	acl := extractSessionACL(r)
	existing, err := h.service.GetSession(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if existing == nil || !acl.allowSession(existing.UserSub) {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("session not found: %s", id))
		return
	}

	if err := h.service.DeleteSession(r.Context(), id); err != nil {
		writeDomainError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ListMessages handles GET /api/v1/sessions/{id}/messages.
// Returns session events (messages, tool calls, reasoning) in chronological order.
func (h *SessionHandler) ListMessages(w http.ResponseWriter, r *http.Request) {
	id, ok := parseSessionID(w, r)
	if !ok {
		return
	}

	acl := extractSessionACL(r)
	if h.service != nil {
		sess, err := h.service.GetSession(r.Context(), id)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		if sess == nil || !acl.allowSession(sess.UserSub) {
			writeJSONError(w, http.StatusNotFound, "session not found")
			return
		}
	}

	if h.eventSvc == nil {
		writeJSON(w, http.StatusOK, []EventResponse{})
		return
	}

	events, err := h.eventSvc.ListEvents(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, events)
}
