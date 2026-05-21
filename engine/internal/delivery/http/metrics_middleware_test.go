package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/metrics"
)

// gatherCounter extracts the counter value for the given label set from the default registry.
func gatherCounter(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	for _, fam := range families {
		if fam.GetName() != name {
			continue
		}
		for _, m := range fam.GetMetric() {
			if matchLabels(m.GetLabel(), labels) {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

// gatherHistogramCount extracts the sample count for the given label set.
func gatherHistogramCount(t *testing.T, name string, labels map[string]string) uint64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	for _, fam := range families {
		if fam.GetName() != name {
			continue
		}
		for _, m := range fam.GetMetric() {
			if matchLabels(m.GetLabel(), labels) {
				return m.GetHistogram().GetSampleCount()
			}
		}
	}
	return 0
}

func matchLabels(pairs []*io_prometheus_client.LabelPair, want map[string]string) bool {
	if len(pairs) != len(want) {
		return false
	}
	for _, p := range pairs {
		v, ok := want[p.GetName()]
		if !ok || v != p.GetValue() {
			return false
		}
	}
	return true
}

func TestMetricsMiddleware_IncrementsCounter(t *testing.T) {
	// Reset the counter for a unique path so we get deterministic results.
	path := "/api/v1/health"

	// Capture baseline.
	baseline := gatherCounter(t, "syntheticbrew_http_requests_total", map[string]string{
		"method": "GET", "path": path, "status": "200",
	})

	handler := MetricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	after := gatherCounter(t, "syntheticbrew_http_requests_total", map[string]string{
		"method": "GET", "path": path, "status": "200",
	})

	assert.Equal(t, baseline+1, after, "counter should increment by 1")
}

func TestMetricsMiddleware_RecordsDuration(t *testing.T) {
	path := "/api/v1/config/export"

	baseline := gatherHistogramCount(t, "syntheticbrew_http_request_duration_seconds", map[string]string{
		"method": "GET", "path": path,
	})

	handler := MetricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	after := gatherHistogramCount(t, "syntheticbrew_http_request_duration_seconds", map[string]string{
		"method": "GET", "path": path,
	})

	assert.Equal(t, baseline+1, after, "histogram sample count should increment by 1")
}

func TestMetricsMiddleware_CapturesStatusCode(t *testing.T) {
	path := "/api/v1/agents"

	baseline404 := gatherCounter(t, "syntheticbrew_http_requests_total", map[string]string{
		"method": "POST", "path": path, "status": "404",
	})

	handler := MetricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	req := httptest.NewRequest(http.MethodPost, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	after404 := gatherCounter(t, "syntheticbrew_http_requests_total", map[string]string{
		"method": "POST", "path": path, "status": "404",
	})

	assert.Equal(t, baseline404+1, after404, "counter with status=404 should increment")
}

func TestMetricsMiddleware_DefaultStatusIs200(t *testing.T) {
	// Handler that writes body without explicit WriteHeader — Go defaults to 200.
	path := "/api/v1/health"

	baseline := gatherCounter(t, "syntheticbrew_http_requests_total", map[string]string{
		"method": "GET", "path": path, "status": "200",
	})

	handler := MetricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	after := gatherCounter(t, "syntheticbrew_http_requests_total", map[string]string{
		"method": "GET", "path": path, "status": "200",
	})

	assert.Equal(t, baseline+1, after)
}

func TestSanitizePath_CollapsesIDs(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "static path unchanged",
			path: "/api/v1/health",
			want: "/api/v1/health",
		},
		{
			name: "agent name collapsed",
			path: "/api/v1/agents/my-agent",
			want: "/api/v1/agents/{name}",
		},
		{
			name: "agent name with subpath",
			path: "/api/v1/agents/my-agent/chat",
			want: "/api/v1/agents/{name}/chat",
		},
		{
			name: "agent name with knowledge",
			path: "/api/v1/agents/my-agent/knowledge/status",
			want: "/api/v1/agents/{name}/knowledge/status",
		},
		{
			name: "session UUID",
			path: "/api/v1/sessions/550e8400-e29b-41d4-a716-446655440000/respond",
			want: "/api/v1/sessions/{id}/respond",
		},
		{
			name: "task numeric ID",
			path: "/api/v1/tasks/42",
			want: "/api/v1/tasks/{id}",
		},
		{
			name: "task numeric ID with subpath",
			path: "/api/v1/tasks/42/input",
			want: "/api/v1/tasks/{id}/input",
		},
		{
			name: "auth token UUID",
			path: "/api/v1/auth/tokens/550e8400-e29b-41d4-a716-446655440000",
			want: "/api/v1/auth/tokens/{id}",
		},
		{
			name: "mcp server ID",
			path: "/api/v1/mcp-servers/123",
			want: "/api/v1/mcp-servers/{id}",
		},
		{
			name: "root path",
			path: "/",
			want: "/",
		},
		{
			name: "agent list (no dynamic segment)",
			path: "/api/v1/agents",
			want: "/api/v1/agents",
		},
		{
			name: "session UUID only (no subpath)",
			path: "/api/v1/sessions/550e8400-e29b-41d4-a716-446655440000",
			want: "/api/v1/sessions/{id}",
		},
		{
			name: "admin path unchanged",
			path: "/admin/",
			want: "/admin/",
		},
		{
			name: "auth login unchanged",
			path: "/api/v1/auth/login",
			want: "/api/v1/auth/login",
		},
		{
			name: "metrics unchanged",
			path: "/metrics",
			want: "/metrics",
		},
		{
			name: "schema name (post 1.1.0 — UUIDs no longer accepted in URL)",
			path: "/api/v1/schemas/support-handbook",
			want: "/api/v1/schemas/{name}",
		},
		{
			name: "knowledge base name",
			path: "/api/v1/knowledge-bases/team-docs",
			want: "/api/v1/knowledge-bases/{name}",
		},
		{
			name: "setting numeric ID",
			path: "/api/v1/settings/99",
			want: "/api/v1/settings/{id}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizePath(tt.path)
			assert.Equal(t, tt.want, got)
		})
	}
}

// Verify that importing the metrics package actually registered metrics.
func TestMetricsRegistered(t *testing.T) {
	// Touch the metric to ensure the promauto registration is complete.
	_ = metrics.HTTPRequestsTotal
	_ = metrics.HTTPRequestDuration
	_ = metrics.ActiveSessions
	_ = metrics.ToolCallsTotal
	_ = metrics.LLMRequestsTotal
	_ = metrics.LLMRequestDuration

	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	names := make(map[string]bool)
	for _, f := range families {
		names[f.GetName()] = true
	}

	// promauto registers lazily — counters/histograms only appear after first observation.
	// So we trigger a single observation for each metric.
	metrics.HTTPRequestsTotal.WithLabelValues("GET", "/test", "200").Inc()
	metrics.HTTPRequestDuration.WithLabelValues("GET", "/test").Observe(0.01)
	metrics.ActiveSessions.Set(0)
	metrics.ToolCallsTotal.WithLabelValues("read_file", "success").Inc()
	metrics.LLMRequestsTotal.WithLabelValues("openai", "gpt-4").Inc()
	metrics.LLMRequestDuration.WithLabelValues("openai", "gpt-4").Observe(1.5)

	families, err = prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	names = make(map[string]bool)
	for _, f := range families {
		names[f.GetName()] = true
	}

	assert.True(t, names["syntheticbrew_http_requests_total"], "syntheticbrew_http_requests_total should be registered")
	assert.True(t, names["syntheticbrew_http_request_duration_seconds"], "syntheticbrew_http_request_duration_seconds should be registered")
	assert.True(t, names["syntheticbrew_active_sessions"], "syntheticbrew_active_sessions should be registered")
	assert.True(t, names["syntheticbrew_tool_calls_total"], "syntheticbrew_tool_calls_total should be registered")
	assert.True(t, names["syntheticbrew_llm_requests_total"], "syntheticbrew_llm_requests_total should be registered")
	assert.True(t, names["syntheticbrew_llm_request_duration_seconds"], "syntheticbrew_llm_request_duration_seconds should be registered")
}
