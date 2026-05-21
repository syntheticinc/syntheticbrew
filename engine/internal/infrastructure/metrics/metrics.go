// Package metrics defines Prometheus metrics for the SyntheticBrew Engine.
// Metrics are auto-registered via promauto — import this package to expose them.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// HTTPRequestsTotal counts all HTTP requests by method, path, and status code.
	HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "syntheticbrew_http_requests_total",
		Help: "Total number of HTTP requests",
	}, []string{"method", "path", "status"})

	// HTTPRequestDuration records HTTP request latency in seconds.
	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "syntheticbrew_http_request_duration_seconds",
		Help:    "HTTP request duration in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})

	// ActiveSessions tracks the number of currently active chat sessions.
	ActiveSessions = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "syntheticbrew_active_sessions",
		Help: "Number of active chat sessions",
	})

	// ToolCallsTotal counts tool invocations by name and outcome.
	ToolCallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "syntheticbrew_tool_calls_total",
		Help: "Total number of tool calls",
	}, []string{"tool_name", "status"})

	// LLMRequestsTotal counts LLM API calls by provider and model.
	LLMRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "syntheticbrew_llm_requests_total",
		Help: "Total number of LLM API requests",
	}, []string{"provider", "model"})

	// LLMRequestDuration records LLM API call latency in seconds.
	LLMRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "syntheticbrew_llm_request_duration_seconds",
		Help:    "LLM API request duration in seconds",
		Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
	}, []string{"provider", "model"})
)
