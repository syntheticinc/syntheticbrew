package domain

import (
	"context"
	"encoding/json"
	"time"
)

type resumeTurnCtxKey struct{}

// WithResumeTurn marks the context as belonging to a HITL resume turn so
// engine.Execute skips CollectUserMessage (otherwise the resume Q+A text
// surfaces as a duplicate user_message bubble).
func WithResumeTurn(ctx context.Context) context.Context {
	return context.WithValue(ctx, resumeTurnCtxKey{}, true)
}

func IsResumeTurn(ctx context.Context) bool {
	v, _ := ctx.Value(resumeTurnCtxKey{}).(bool)
	return v
}

type InterruptStatus string

const (
	InterruptStatusPending   InterruptStatus = "pending"
	InterruptStatusResolved  InterruptStatus = "resolved"
	InterruptStatusAbandoned InterruptStatus = "abandoned" // user sent a regular message without resuming → can no longer be resumed
)

// InterruptKind discriminates payload shape. Lives in the linked request
// event's proto_data, not in the DB row.
type InterruptKind string

const (
	InterruptKindStructuredOutput InterruptKind = "structured_output"
)

// Interrupt mirrors the `interrupts` table — pure state. Kind / schema /
// payload live in the linked session_event_log rows (RequestEventID /
// ResolveEventID).
type Interrupt struct {
	ID             string
	TenantID       string
	RequestEventID string
	Status         InterruptStatus
	ResolveEventID *string
	CreatedAt      time.Time
}

// InterruptRequestPayload is the JSON shape in the request event's
// SessionEvent.Content. The client receives it verbatim via the
// interrupt_request SSE event.
type InterruptRequestPayload struct {
	InterruptID string          `json:"interrupt_id"`
	Kind        InterruptKind   `json:"kind"`
	Schema      json.RawMessage `json:"schema"`
}

// InterruptResumePayload is the JSON shape in the resolve event's
// SessionEvent.Content, mirroring what the client POSTed.
type InterruptResumePayload struct {
	InterruptID string          `json:"interrupt_id"`
	Kind        InterruptKind   `json:"kind"`
	Payload     json.RawMessage `json:"payload"`
}
