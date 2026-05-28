package http

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// resolveAuditAction maps an HTTP method + path to a semantic audit action
// token ("agent.create", "auth.fail", etc.). Unknown combinations fall back
// to "api_call" so the audit log is never silently empty. Compliance auditors
// query by action, so this mapping is part of the audit contract.
//
// pathOrPattern is preferred to be the chi-matched route pattern (e.g.
// "/api/v1/schemas/{name}/agent-relations") rather than the raw URL path.
// Pattern dispatch sidesteps substring collisions — for engine 1.1.0+ a
// schema named "agent-relations" or "chat" would otherwise shadow the
// nested endpoints when path-based matching is used. Falls back gracefully
// when called with a raw path (no chi context, or routes that don't go
// through chi at all — e.g. health/metrics).
func resolveAuditAction(method, pathOrPattern string, status int) string {
	switch {
	// Auth endpoints — status-dependent semantics.
	case method == "POST" && strings.HasPrefix(pathOrPattern, "/api/v1/auth/local-session"):
		if status >= 400 {
			return "auth.fail"
		}
		return "auth.success"
	case method == "POST" && pathOrPattern == "/api/v1/auth/tokens":
		return "token.create"
	case method == "DELETE" && strings.HasPrefix(pathOrPattern, "/api/v1/auth/tokens/"):
		return "token.revoke"

	// Agent CRUD.
	case method == "POST" && pathOrPattern == "/api/v1/agents":
		return "agent.create"
	case (method == "PUT" || method == "PATCH") && strings.HasPrefix(pathOrPattern, "/api/v1/agents/"):
		return "agent.update"
	case method == "DELETE" && strings.HasPrefix(pathOrPattern, "/api/v1/agents/"):
		return "agent.delete"

	// Schema agent-relations — match before generic schema CRUD so the
	// nested mutations get their own action. HasSuffix on the route pattern
	// distinguishes the collection (`/agent-relations`) from a single
	// relation (`/agent-relations/{relationId}`); both map to mutations
	// scoped to the relation domain regardless of the schema name in the
	// URL.
	case method == "POST" && strings.HasSuffix(pathOrPattern, "/agent-relations"):
		return "agent_relation.create"
	case method == "DELETE" && strings.HasSuffix(pathOrPattern, "/agent-relations/{relationId}"):
		return "agent_relation.delete"
	// Fallback for raw paths (legacy callers / non-chi tests) — keep the
	// substring-based detection so pre-1.1.0 audit-log fixtures stay
	// resolvable. New callers always pass the chi route pattern.
	case method == "DELETE" && strings.Contains(pathOrPattern, "/agent-relations/"):
		return "agent_relation.delete"
	case method == "POST" && strings.HasSuffix(pathOrPattern, "/chat") && strings.HasPrefix(pathOrPattern, "/api/v1/schemas/"):
		return "chat.message"

	// Schema CRUD.
	case method == "POST" && pathOrPattern == "/api/v1/schemas":
		return "schema.create"
	case (method == "PUT" || method == "PATCH") && strings.HasPrefix(pathOrPattern, "/api/v1/schemas/"):
		return "schema.update"
	case method == "DELETE" && strings.HasPrefix(pathOrPattern, "/api/v1/schemas/"):
		return "schema.delete"

	// Model / MCP / KB / Settings CRUD.
	case method == "POST" && pathOrPattern == "/api/v1/models":
		return "model.create"
	case (method == "PUT" || method == "PATCH") && strings.HasPrefix(pathOrPattern, "/api/v1/models/"):
		return "model.update"
	case method == "DELETE" && strings.HasPrefix(pathOrPattern, "/api/v1/models/"):
		return "model.delete"
	case method == "POST" && pathOrPattern == "/api/v1/mcp-servers":
		return "mcp.create"
	case method == "DELETE" && strings.HasPrefix(pathOrPattern, "/api/v1/mcp-servers/"):
		return "mcp.delete"
	case method == "POST" && pathOrPattern == "/api/v1/knowledge-bases":
		return "kb.create"
	case method == "DELETE" && strings.HasPrefix(pathOrPattern, "/api/v1/knowledge-bases/"):
		return "kb.delete"
	case (method == "PUT" || method == "PATCH") && strings.HasPrefix(pathOrPattern, "/api/v1/settings/"):
		return "setting.update"

	// Knowledge Graph mutations — searchable as `action LIKE 'kg.%'`.
	case method == "POST" && strings.HasSuffix(pathOrPattern, "/api/v1/knowledge-graphs/{bundle}/import"):
		return "kg.bundle.import"
	case method == "DELETE" && pathOrPattern == "/api/v1/knowledge-graphs/{bundle}":
		return "kg.bundle.delete"
	case method == "POST" && strings.HasSuffix(pathOrPattern, "/entities/{entity_type}") && strings.HasPrefix(pathOrPattern, "/api/v1/knowledge-graphs/"):
		return "kg.entity.create"
	case method == "PUT" && strings.HasSuffix(pathOrPattern, "/entities/{entity_type}/{id}") && strings.HasPrefix(pathOrPattern, "/api/v1/knowledge-graphs/"):
		return "kg.entity.update"
	case method == "DELETE" && strings.HasSuffix(pathOrPattern, "/entities/{entity_type}/{id}") && strings.HasPrefix(pathOrPattern, "/api/v1/knowledge-graphs/"):
		return "kg.entity.delete"
	case method == "PUT" && strings.HasSuffix(pathOrPattern, "/schemas/{entity_type}") && strings.HasPrefix(pathOrPattern, "/api/v1/knowledge-graphs/"):
		return "kg.schema.upsert"

	// Session CRUD.
	case method == "DELETE" && strings.HasPrefix(pathOrPattern, "/api/v1/sessions/"):
		return "session.delete"
	}
	return "api_call"
}

// auditDispatchPath returns the chi-matched route pattern for r when
// available (post-routing context), falling back to the raw URL path. The
// pattern form is templated (`/api/v1/schemas/{name}/agent-relations`) so
// substring checks in resolveAuditAction can't collide with operator-chosen
// resource names — a schema named "agent-relations" never reaches the same
// switch arm as an actual relation endpoint because reserved-name validation
// rejects it at create time AND the routing layer matches by literal path
// segment.
func auditDispatchPath(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if pattern := rc.RoutePattern(); pattern != "" {
			return pattern
		}
	}
	return r.URL.Path
}

// AuditLogger is used by the audit middleware to record API calls.
type AuditLogger interface {
	Log(ctx context.Context, entry AuditEntry) error
}

// AuditEntry represents a single audit log entry for the middleware.
type AuditEntry struct {
	Timestamp time.Time
	ActorType string
	ActorID   string
	Action    string
	Resource  string
	Details   map[string]interface{}
	SessionID string
}

// AuditMiddleware returns middleware that logs all API calls to the audit log.
//
// Action resolution uses chi's matched route pattern (post-routing) rather
// than the raw URL path so that operator-chosen resource names — schema
// names, KB names — can't shadow nested route segments. The audit `Resource`
// field still records the raw method+path for forensics.
func AuditMiddleware(logger AuditLogger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			actorType, _ := r.Context().Value(ContextKeyActorType).(string)
			actorID, _ := r.Context().Value(ContextKeyActorID).(string)

			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)

			_ = logger.Log(r.Context(), AuditEntry{
				Timestamp: time.Now(),
				ActorType: actorType,
				ActorID:   actorID,
				Action:    resolveAuditAction(r.Method, auditDispatchPath(r), sw.status),
				Resource:  r.Method + " " + r.URL.Path,
				Details: map[string]interface{}{
					"method":      r.Method,
					"path":        r.URL.Path,
					"status_code": sw.status,
				},
			})
		})
	}
}

// statusWriter wraps http.ResponseWriter to capture the status code.
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.status = code
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap returns the underlying ResponseWriter, allowing middleware traversal
// (e.g. findFlusher in chat_handler.go can reach http.Flusher through the chain).
func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Flush delegates to the underlying ResponseWriter if it implements http.Flusher.
// This is critical for SSE streaming — without it, events buffer in Go's internal
// writer and browsers receive them in ~4KB TCP batches instead of per-token.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
