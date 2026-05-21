package audit

import (
	"context"

	deliveryhttp "github.com/syntheticinc/syntheticbrew/internal/delivery/http"
)

// HTTPAdapter adapts audit.Logger to the delivery/http.AuditLogger interface.
type HTTPAdapter struct {
	logger *Logger
}

// NewHTTPAdapter creates an adapter that bridges delivery-layer audit entries
// to the infrastructure audit logger.
func NewHTTPAdapter(logger *Logger) *HTTPAdapter {
	return &HTTPAdapter{logger: logger}
}

// Log converts an http.AuditEntry to audit.Entry and persists it.
func (a *HTTPAdapter) Log(ctx context.Context, entry deliveryhttp.AuditEntry) error {
	return a.logger.Log(ctx, Entry{
		Timestamp: entry.Timestamp,
		ActorType: entry.ActorType,
		ActorID:   entry.ActorID,
		Action:    entry.Action,
		Resource:  entry.Resource,
		Details:   entry.Details,
		SessionID: entry.SessionID,
	})
}
