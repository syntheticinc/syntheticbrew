package errors

import (
	"errors"
	"fmt"
)

// Error codes for domain errors
const (
	CodeInternal         = "INTERNAL_ERROR"
	CodeInvalidInput     = "INVALID_INPUT"
	CodeNotFound         = "NOT_FOUND"
	CodeAlreadyExists    = "ALREADY_EXISTS"
	CodeUnauthorized     = "UNAUTHORIZED"
	CodeForbidden        = "FORBIDDEN"
	CodeTimeout          = "TIMEOUT"
	CodeUnavailable      = "UNAVAILABLE"
	CodePermissionDenied = "PERMISSION_DENIED"
	CodeCancelled        = "CANCELLED"
	CodeUsageLimited     = "USAGE_LIMITED"

	// LLM-provider error classes — produced at the HTTP boundary in
	// internal/infrastructure/llm/classify_error.go and consumed by
	// react/agent.go classifyRecovery via errors.Is. Keep these tightly
	// scoped to LLM provider semantics; do not reuse them for other
	// transport or tool-level errors.
	CodeRateLimited          = "RATE_LIMITED"
	CodeLLMAuth              = "LLM_AUTH"
	CodeTransient            = "TRANSIENT"
	CodeAgentBudgetExhausted = "AGENT_BUDGET_EXHAUSTED"
)

// DomainError represents a domain-specific error with code and context
type DomainError struct {
	Code    string
	Message string
	Err     error
}

// Error implements the error interface
func (e *DomainError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// Unwrap returns the wrapped error
func (e *DomainError) Unwrap() error {
	return e.Err
}

// New creates a new DomainError
func New(code, message string) *DomainError {
	return &DomainError{
		Code:    code,
		Message: message,
	}
}

// Wrap wraps an error with code and message
func Wrap(err error, code, message string) *DomainError {
	if err == nil {
		return nil
	}
	return &DomainError{
		Code:    code,
		Message: message,
		Err:     err,
	}
}

// Internal creates an internal error
func Internal(message string, err error) *DomainError {
	return Wrap(err, CodeInternal, message)
}

// InvalidInput creates an invalid input error
func InvalidInput(message string) *DomainError {
	return New(CodeInvalidInput, message)
}

// NotFound creates a not found error
func NotFound(message string) *DomainError {
	return New(CodeNotFound, message)
}

// AlreadyExists creates an already exists error
func AlreadyExists(message string) *DomainError {
	return New(CodeAlreadyExists, message)
}

// Unauthorized creates an unauthorized error
func Unauthorized(message string) *DomainError {
	return New(CodeUnauthorized, message)
}

// Forbidden creates a forbidden error
func Forbidden(message string) *DomainError {
	return New(CodeForbidden, message)
}

// Timeout creates a timeout error
func Timeout(message string, err error) *DomainError {
	return Wrap(err, CodeTimeout, message)
}

// Unavailable creates an unavailable error
func Unavailable(message string, err error) *DomainError {
	return Wrap(err, CodeUnavailable, message)
}

// PermissionDenied creates a permission denied error
func PermissionDenied(message string) *DomainError {
	return New(CodePermissionDenied, message)
}

// Cancelled creates a cancelled error
func Cancelled(message string) *DomainError {
	return New(CodeCancelled, message)
}

// UsageLimited creates a usage-limit-reached error. It maps to HTTP 402 at the
// delivery boundary — the turn was refused because a configured usage limit is
// exhausted, not because of an auth or input problem.
func UsageLimited(message string) *DomainError {
	return New(CodeUsageLimited, message)
}

// Is checks if the error is a DomainError with the given code
func Is(err error, code string) bool {
	var domainErr *DomainError
	if errors.As(err, &domainErr) {
		return domainErr.Code == code
	}
	return false
}

// GetCode returns the error code if it's a DomainError, otherwise returns CodeInternal
func GetCode(err error) string {
	var domainErr *DomainError
	if errors.As(err, &domainErr) {
		return domainErr.Code
	}
	return CodeInternal
}

// DeepestCode returns the deepest non-CodeInternal DomainError code in the
// chain, recovering the real classification (e.g. UNAVAILABLE) when callers
// re-wrapped a typed cause as generic CodeInternal. Falls back to CodeInternal.
func DeepestCode(err error) string {
	code := CodeInternal
	for e := err; e != nil; e = errors.Unwrap(e) {
		if de, ok := e.(*DomainError); ok && de.Code != CodeInternal {
			code = de.Code
		}
	}
	return code
}

// genericUserMessage is surfaced to end users when an error carries no curated,
// user-facing DomainError message. It never exposes the raw error string, which
// can leak internal detail (provider URLs, wrapped technical chains) to the
// client over the session error event. Operators still get the full error via
// server-side logging and the stable DeepestCode carried alongside.
const genericUserMessage = "An unexpected error occurred. Please try again."

// UserMessage returns the curated, user-facing message for an error: the
// Message of the deepest non-CodeInternal DomainError when present (without the
// "[CODE]" prefix or wrapped technical detail), else a generic fallback. It
// never returns the raw error string — an untyped error would otherwise leak
// internal detail to the client.
func UserMessage(err error) string {
	if err == nil {
		return ""
	}
	var best *DomainError
	for e := err; e != nil; e = errors.Unwrap(e) {
		if de, ok := e.(*DomainError); ok && de.Code != CodeInternal {
			best = de
		}
	}
	if best != nil {
		return best.Message
	}
	return genericUserMessage
}
