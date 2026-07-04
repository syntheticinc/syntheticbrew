package http

import (
	"encoding/json"
	"net/http"
	"time"
)

// HealthResponse is the JSON body returned by the health endpoint.
type HealthResponse struct {
	Status          string `json:"status"`
	Version         string `json:"version"`
	Uptime          string `json:"uptime"`
	AgentsCount     int    `json:"agents_count"`
	Database        string `json:"database,omitempty"`
	UpdateAvailable string `json:"update_available,omitempty"`
	// PlatformDefaultModel is true when a usable process-wide default model is
	// installed (a plugin called ModelSelector.SetDefault). Clients use it to
	// skip mandatory key-setup onboarding when the deployment already funds a
	// default model. Absent/false on self-hosted deployments with no default.
	PlatformDefaultModel bool `json:"platform_default_model"`
}

// AgentCounter provides a count of currently registered agents.
type AgentCounter interface {
	Count() int
}

// DBPinger checks database connectivity.
type DBPinger interface {
	Ping() error
}

// UpdateAvailableChecker reports whether a newer Engine version is available.
type UpdateAvailableChecker interface {
	UpdateAvailable() string
}

// PlatformDefaultChecker reports whether a usable process-wide default model
// is installed. Implemented by *llm.ModelSelector.
type PlatformDefaultChecker interface {
	HasPlatformDefault() bool
}

// HealthHandler serves GET /api/v1/health.
type HealthHandler struct {
	version         string
	startedAt       time.Time
	agentCounter    AgentCounter
	dbPinger        DBPinger                // optional, nil if no DB
	updateChecker   UpdateAvailableChecker  // optional, nil if not configured
	platformDefault PlatformDefaultChecker  // optional, nil if not wired
}

// NewHealthHandler creates a HealthHandler.
func NewHealthHandler(version string, agentCounter AgentCounter) *HealthHandler {
	return &HealthHandler{
		version:      version,
		startedAt:    time.Now(),
		agentCounter: agentCounter,
	}
}

// SetDBPinger sets the database pinger for health checks.
func (h *HealthHandler) SetDBPinger(p DBPinger) { h.dbPinger = p }

// SetUpdateChecker sets the update availability checker.
func (h *HealthHandler) SetUpdateChecker(uc UpdateAvailableChecker) { h.updateChecker = uc }

// SetPlatformDefaultChecker wires the process-wide default-model checker.
func (h *HealthHandler) SetPlatformDefaultChecker(c PlatformDefaultChecker) { h.platformDefault = c }

// ServeHTTP handles the health check request.
func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	dbStatus := ""

	if h.dbPinger != nil {
		if err := h.dbPinger.Ping(); err != nil {
			status = "degraded"
			dbStatus = "error: " + err.Error()
		} else {
			dbStatus = "connected"
		}
	}

	var updateAvailable string
	if h.updateChecker != nil {
		updateAvailable = h.updateChecker.UpdateAvailable()
	}

	platformDefault := false
	if h.platformDefault != nil {
		platformDefault = h.platformDefault.HasPlatformDefault()
	}

	resp := HealthResponse{
		Status:               status,
		Version:              h.version,
		Uptime:               time.Since(h.startedAt).Round(time.Second).String(),
		AgentsCount:          h.agentCounter.Count(),
		Database:             dbStatus,
		UpdateAvailable:      updateAvailable,
		PlatformDefaultModel: platformDefault,
	}

	statusCode := http.StatusOK
	if status != "ok" {
		statusCode = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(resp)
}
