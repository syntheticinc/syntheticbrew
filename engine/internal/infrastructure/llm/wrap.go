package llm

import (
	"time"

	"github.com/cloudwego/eino/components/model"
)

// Default retry parameters for production-wrapped LLM clients.
// Tuned conservatively to avoid hot-looping against persistently failing
// providers while still smoothing over brief transient hiccups.
//
//   - 3 attempts (1 original + 2 retries) covers most one-shot 5xx/timeouts
//     without amplifying cost during prolonged outages.
//   - 500ms base delay with exponential backoff (500ms / 1s / 2s) is short
//     enough to be invisible on the happy path and long enough to dodge
//     short-lived provider hiccups.
//   - 5min per-attempt timeout is generous enough for reasoning models
//     (o1-class, Claude Opus on long prompts) while still bounding the
//     worst-case stall. Outer agent contexts impose their own shorter
//     deadlines where appropriate (see agent.go streamTimeout), so this
//     value is a safety net, not the primary bound.
const (
	defaultMaxRetries = 2
	defaultBaseDelay  = 500 * time.Millisecond
	defaultTimeout    = 5 * time.Minute
)

// WrapWithRetry is the single chokepoint for wrapping production-bound
// chat models in a RetryWrapper. Every constructor in this package and in
// internal/app/llm_factory.go MUST funnel its output through this helper
// before returning to upstream code — otherwise the agent layer will see
// raw provider errors and lose the typed-error classification guarantee.
//
// The wrapper is transparent: it forwards Generate/Stream/WithTools and
// reports the inner model's IsCallbacksEnabled value so Eino's callback
// auto-injection does not double-emit chunks.
func WrapWithRetry(inner model.ToolCallingChatModel) model.ToolCallingChatModel {
	return NewRetryWrapper(inner, defaultMaxRetries, defaultBaseDelay, defaultTimeout)
}
