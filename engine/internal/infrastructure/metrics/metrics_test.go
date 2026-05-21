package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetricsRegistered(t *testing.T) {
	// Trigger at least one observation per metric so they appear in Gather().
	HTTPRequestsTotal.WithLabelValues("GET", "/healthz", "200").Inc()
	HTTPRequestDuration.WithLabelValues("GET", "/healthz").Observe(0.005)
	ActiveSessions.Set(3)
	ToolCallsTotal.WithLabelValues("search_code", "success").Inc()
	LLMRequestsTotal.WithLabelValues("anthropic", "claude-4").Inc()
	LLMRequestDuration.WithLabelValues("anthropic", "claude-4").Observe(2.1)

	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	names := make(map[string]bool)
	for _, f := range families {
		names[f.GetName()] = true
	}

	expected := []string{
		"syntheticbrew_http_requests_total",
		"syntheticbrew_http_request_duration_seconds",
		"syntheticbrew_active_sessions",
		"syntheticbrew_tool_calls_total",
		"syntheticbrew_llm_requests_total",
		"syntheticbrew_llm_request_duration_seconds",
	}

	for _, name := range expected {
		assert.True(t, names[name], "metric %q should be registered in default registry", name)
	}
}

func TestHTTPRequestsTotal_Labels(t *testing.T) {
	// Verify the counter can be used with expected label set.
	c := HTTPRequestsTotal.WithLabelValues("POST", "/api/v1/agents", "201")
	c.Inc()
	// No panic means the label set is valid.
}

func TestHTTPRequestDuration_Buckets(t *testing.T) {
	// Observe different values and ensure no panic.
	h := HTTPRequestDuration.WithLabelValues("GET", "/api/v1/health")
	h.Observe(0.001)
	h.Observe(0.5)
	h.Observe(5.0)
}

func TestLLMRequestDuration_CustomBuckets(t *testing.T) {
	// LLM duration uses custom buckets [0.1, 0.5, 1, 2, 5, 10, 30, 60].
	h := LLMRequestDuration.WithLabelValues("ollama", "llama3")
	h.Observe(0.05)
	h.Observe(45.0)
}

func TestActiveSessions_GaugeOperations(t *testing.T) {
	ActiveSessions.Set(0)
	ActiveSessions.Inc()
	ActiveSessions.Inc()
	ActiveSessions.Dec()

	// Gather and check value.
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	for _, f := range families {
		if f.GetName() != "syntheticbrew_active_sessions" {
			continue
		}
		for _, m := range f.GetMetric() {
			val := m.GetGauge().GetValue()
			assert.Equal(t, float64(1), val, "after set(0)+inc+inc+dec, gauge should be 1")
		}
	}
}
