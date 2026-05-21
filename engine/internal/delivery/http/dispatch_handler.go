package http

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// DispatchQueryer provides read access to dispatched task packets.
type DispatchQueryer interface {
	GetTask(taskID string) (*domain.TaskPacket, bool)
	ListTasksBySession(sessionID string) []*domain.TaskPacket
}

// SessionOwnerReader resolves a session's owning user_sub for ACL checks.
// Implemented by configrepo.GORMSessionRepository.GetUserSubBySessionID;
// the dispatch handler uses it to reject cross-user reads without echoing
// task data first (information hiding — same 404 as not-found).
type SessionOwnerReader interface {
	GetUserSubBySessionID(ctx context.Context, sessionID string) (string, bool, error)
}

// DispatchHandler serves dispatch task query endpoints.
type DispatchHandler struct {
	queryer       DispatchQueryer
	sessionOwners SessionOwnerReader
}

// NewDispatchHandler creates a new DispatchHandler.
//
// sessionOwners is required for the per-user ACL guard introduced in 1.1.4.
// Passing nil disables the guard (used only in unit tests that fake the
// queryer); production wiring in routes_register supplies the real
// repository.
func NewDispatchHandler(queryer DispatchQueryer, sessionOwners SessionOwnerReader) *DispatchHandler {
	return &DispatchHandler{queryer: queryer, sessionOwners: sessionOwners}
}

// allowDispatchSession returns true iff the actor may see dispatch packets
// for the given session. Mirrors sessionACL.allowSession — trusted proxy
// (api_token) and ScopeAdmin actors pass; everyone else must own the
// session. Missing session_id or owner-lookup error → deny.
func (h *DispatchHandler) allowDispatchSession(r *http.Request, sessionID string) bool {
	acl := extractSessionACL(r)
	if acl.canSeeAllUsers() {
		return true
	}
	if h.sessionOwners == nil || sessionID == "" {
		return false
	}
	ownerSub, ok, err := h.sessionOwners.GetUserSubBySessionID(r.Context(), sessionID)
	if err != nil || !ok {
		return false
	}
	return acl.userSub != "" && acl.userSub == ownerSub
}

// TaskPacketResponse is the JSON representation of a dispatched task.
type TaskPacketResponse struct {
	ID          string `json:"id"`
	AgentName   string `json:"agent_name"`
	Task        string `json:"task"`
	SessionID   string `json:"session_id"`
	State       string `json:"state"`
	Result      string `json:"result,omitempty"`
	Error       string `json:"error,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// Get handles GET /api/v1/dispatch/tasks/{taskId}.
//
// Cross-user dispatch reads inherit the same ACL as /sessions: actor must
// either be ScopeAdmin / api_token (trusted proxy) or own the session that
// the dispatch packet belongs to. Mismatch → 404 (info hiding).
func (h *DispatchHandler) Get(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	if taskID == "" {
		http.Error(w, `{"error":"task id required"}`, http.StatusBadRequest)
		return
	}

	packet, ok := h.queryer.GetTask(taskID)
	if !ok {
		http.Error(w, `{"error":"task not found"}`, http.StatusNotFound)
		return
	}
	if !h.allowDispatchSession(r, packet.SessionID) {
		// Same code as not-found — never confirm task existence to
		// non-owner actors.
		http.Error(w, `{"error":"task not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(toTaskPacketResponse(packet))
}

// ListBySession handles GET /api/v1/sessions/{sessionId}/dispatch-tasks.
//
// ACL gate: non-owner actors get 404, mirroring /sessions/{id} response so
// dispatch routes can't be used to enumerate session UUIDs across users.
func (h *DispatchHandler) ListBySession(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionId")
	if sessionID == "" {
		http.Error(w, `{"error":"session id required"}`, http.StatusBadRequest)
		return
	}
	if !h.allowDispatchSession(r, sessionID) {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	packets := h.queryer.ListTasksBySession(sessionID)

	responses := make([]TaskPacketResponse, 0, len(packets))
	for _, p := range packets {
		responses = append(responses, toTaskPacketResponse(p))
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(responses)
}

func toTaskPacketResponse(p *domain.TaskPacket) TaskPacketResponse {
	updatedAt := p.CreatedAt
	if !p.FinishedAt.IsZero() {
		updatedAt = p.FinishedAt
	} else if !p.StartedAt.IsZero() {
		updatedAt = p.StartedAt
	}

	return TaskPacketResponse{
		ID:        p.ID,
		AgentName: p.ChildAgent,
		Task:      p.Input,
		SessionID: p.SessionID,
		State:     string(p.Status),
		Result:    p.Result,
		Error:     p.Error,
		CreatedAt: p.CreatedAt.Format(time.RFC3339),
		UpdatedAt: updatedAt.Format(time.RFC3339),
	}
}
