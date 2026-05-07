package http

import (
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/metrics"
)

// MetricsMiddleware records Prometheus metrics for every HTTP request:
// request count (by method, path, status) and duration (by method, path).
// Dynamic path segments are collapsed to prevent high-cardinality labels.
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Reuse the statusWriter from audit_middleware.go (same package).
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)

		duration := time.Since(start).Seconds()
		path := sanitizePath(r.URL.Path)
		status := strconv.Itoa(sw.status)

		metrics.HTTPRequestsTotal.WithLabelValues(r.Method, path, status).Inc()
		metrics.HTTPRequestDuration.WithLabelValues(r.Method, path).Observe(duration)
	})
}

// uuidPattern matches UUID-like segments (8-4-4-4-12 hex).
var uuidPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// numericPattern matches purely numeric segments (database IDs).
var numericPattern = regexp.MustCompile(`^[0-9]+$`)

// sanitizePath collapses dynamic path segments so Prometheus labels stay low-cardinality.
//
// Known API patterns (from server.go routes):
//
//	/api/v1/agents/{name}/chat              -> /api/v1/agents/{name}/chat
//	/api/v1/agents/{name}                   -> /api/v1/agents/{name}
//	/api/v1/schemas/{name}                  -> /api/v1/schemas/{name}
//	/api/v1/schemas/{name}/chat             -> /api/v1/schemas/{name}/chat
//	/api/v1/knowledge-bases/{name}          -> /api/v1/knowledge-bases/{name}
//	/api/v1/sessions/{id}/respond           -> /api/v1/sessions/{id}/respond
//	/api/v1/tasks/{id}/input                -> /api/v1/tasks/{id}/input
//	/api/v1/auth/tokens/{id}                -> /api/v1/auth/tokens/{id}
//	/api/v1/webhooks/{path}                 -> /api/v1/webhooks/{path}
//
// The function replaces UUIDs with {id} and numeric IDs with {id}.
// Agent / schema / KB / webhook names that are neither UUID nor numeric
// collapse to the resource-appropriate placeholder.
func sanitizePath(path string) string {
	// Replace UUIDs first (before segment-level analysis).
	path = uuidPattern.ReplaceAllString(path, "{id}")

	// Known resource prefixes where the segment after the prefix is dynamic.
	// Order matters: longer prefixes first.
	replacements := []struct {
		prefix      string
		placeholder string
	}{
		{"/api/v1/agents/", "{name}"},
		{"/api/v1/knowledge-bases/", "{name}"},
		{"/api/v1/schemas/", "{name}"},
		{"/api/v1/sessions/", "{id}"},
		{"/api/v1/tasks/", "{id}"},
		{"/api/v1/auth/tokens/", "{id}"},
		{"/api/v1/mcp-servers/", "{id}"},
		{"/api/v1/settings/", "{id}"},
	}

	for _, rep := range replacements {
		if len(path) <= len(rep.prefix) {
			continue
		}
		if path[:len(rep.prefix)] != rep.prefix {
			continue
		}

		rest := path[len(rep.prefix):]
		// Already replaced by UUID pattern.
		if len(rest) >= 4 && rest[:4] == "{id}" {
			continue
		}

		// Find the end of the dynamic segment.
		slashIdx := indexOf(rest, '/')
		if slashIdx == -1 {
			// Entire rest is the dynamic segment.
			path = rep.prefix + rep.placeholder
		} else {
			path = rep.prefix + rep.placeholder + rest[slashIdx:]
		}
		break
	}

	// Catch-all: replace any remaining pure-numeric segments.
	// Split, check, rejoin.
	parts := splitPath(path)
	for i, p := range parts {
		if numericPattern.MatchString(p) {
			parts[i] = "{id}"
		}
	}
	return joinPath(parts)
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func splitPath(path string) []string {
	if path == "" {
		return nil
	}
	// Remove leading slash.
	if path[0] == '/' {
		path = path[1:]
	}
	var parts []string
	start := 0
	for i := 0; i <= len(path); i++ {
		if i == len(path) || path[i] == '/' {
			parts = append(parts, path[start:i])
			start = i + 1
		}
	}
	return parts
}

func joinPath(parts []string) string {
	if len(parts) == 0 {
		return "/"
	}
	result := ""
	for _, p := range parts {
		result += "/" + p
	}
	return result
}
