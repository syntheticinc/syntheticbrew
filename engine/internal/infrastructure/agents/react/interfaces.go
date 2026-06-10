package react

import (
	"context"

	"github.com/cloudwego/eino/schema"
)

// ContextLoggerInterface defines the interface for context logging
type ContextLoggerInterface interface {
	// LogContext logs the current context composition to a step-specific file
	LogContext(ctx context.Context, messages []*schema.Message, step int)

	// LogContextSummary logs a summary of the context
	LogContextSummary(ctx context.Context, messages []*schema.Message)
}

// ContextReminderProvider is implemented by components that need to inject
// reminders into the LLM context (e.g., plan status, pending actions)
// Returns (content, priority, hasReminder) - priority determines order (higher = later in context)
type ContextReminderProvider interface {
	GetContextReminder(ctx context.Context, sessionID string) (content string, priority int, ok bool)
}
