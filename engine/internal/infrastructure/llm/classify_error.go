package llm

import (
	"strings"

	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// classifyLLMError maps a raw error from an LLM provider SDK into a typed
// pkgerrors.DomainError with one of the LLM-scoped codes (CodeLLMAuth,
// CodeRateLimited, CodeTransient, CodeInvalidInput).
//
// This is the only place in the engine where substring matching against
// HTTP status codes and provider error phrases is permitted: it lives on
// the boundary with third-party SDKs (Eino's openai/azure/gemini clients)
// that return opaque error strings. Localising the matching here keeps
// the agent-layer classifier (react/agent.go classifyRecovery) free of
// any text-based decisions.
//
// Returns:
//   - nil if err is nil.
//   - A pkgerrors.DomainError wrap with the appropriate code for known
//     LLM-provider error shapes (auth/rate-limit/transient/invalid-request).
//   - The original err unwrapped for unknown shapes, so callers that
//     default-retry unknown errors (RetryWrapper) preserve that behaviour.
func classifyLLMError(err error) error {
	if err == nil {
		return nil
	}

	msg := strings.ToLower(err.Error())

	// Auth shape — fail fast. Order matters: check auth before generic 4xx
	// because "401" / "403" are auth signals, not generic client bugs.
	for _, p := range authPatterns {
		if strings.Contains(msg, p) {
			return pkgerrors.Wrap(err, pkgerrors.CodeLLMAuth, "llm auth failed")
		}
	}

	// Rate-limit / quota — backoff retry.
	for _, p := range rateLimitPatterns {
		if strings.Contains(msg, p) {
			return pkgerrors.Wrap(err, pkgerrors.CodeRateLimited, "llm rate limited")
		}
	}

	// Transient — server-side / network failures. Safe to retry.
	for _, p := range transientPatterns {
		if strings.Contains(msg, p) {
			return pkgerrors.Wrap(err, pkgerrors.CodeTransient, "llm transient")
		}
	}

	// Caller-side bugs — bad request shape, unknown model, malformed input.
	// Not a retry candidate; LLM should be given a chance to fix its
	// request via feedback retry at the agent layer.
	for _, p := range invalidPatterns {
		if strings.Contains(msg, p) {
			return pkgerrors.Wrap(err, pkgerrors.CodeInvalidInput, "llm invalid request")
		}
	}

	// Unknown shape — propagate unwrapped. Downstream callers may apply
	// their own default policy (RetryWrapper retries unknowns; the agent
	// layer feedback-retries unknowns). This preserves existing behaviour.
	return err
}

// LLM error classification patterns. These lists are deliberately
// conservative — only well-known phrases produced by major providers
// (OpenAI, Anthropic, Gemini, Azure OpenAI, Ollama via openai-compatible
// shim) and common transport-layer error wording.
var (
	authPatterns = []string{
		"401",
		"403",
		"unauthorized",
		"authentication",
		"invalid api key",
	}

	rateLimitPatterns = []string{
		"429",
		"too many requests",
		"rate limit",
		"quota exceeded",
	}

	transientPatterns = []string{
		"502",
		"503",
		"service unavailable",
		"bad gateway",
		"timeout",
		"deadline exceeded",
		"connection refused",
		"connection reset",
		"eof",
	}

	invalidPatterns = []string{
		"400",
		"404",
		"invalid",
	}
)
