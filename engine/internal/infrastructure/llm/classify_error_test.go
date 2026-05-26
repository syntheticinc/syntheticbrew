package llm

import (
	"errors"
	"fmt"
	"testing"

	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

func TestClassifyLLMError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode string // empty = expect unwrapped (no DomainError wrap)
		wantNil  bool
		wantOrig bool // expect err to be returned unmodified
	}{
		// nil propagation
		{name: "nil error", err: nil, wantNil: true},

		// Auth — case-insensitive substring matching.
		{name: "401", err: errors.New("HTTP 401"), wantCode: pkgerrors.CodeLLMAuth},
		{name: "403", err: errors.New("HTTP 403 forbidden"), wantCode: pkgerrors.CodeLLMAuth},
		{name: "unauthorized lowercase", err: errors.New("unauthorized"), wantCode: pkgerrors.CodeLLMAuth},
		{name: "Unauthorized mixed case", err: errors.New("Unauthorized: bad token"), wantCode: pkgerrors.CodeLLMAuth},
		{name: "authentication", err: errors.New("authentication failed"), wantCode: pkgerrors.CodeLLMAuth},
		{name: "invalid api key", err: errors.New("Incorrect API key provided: ... invalid api key"), wantCode: pkgerrors.CodeLLMAuth},

		// Rate-limit / quota
		{name: "429", err: errors.New("HTTP 429"), wantCode: pkgerrors.CodeRateLimited},
		{name: "too many requests", err: errors.New("Too Many Requests"), wantCode: pkgerrors.CodeRateLimited},
		{name: "rate limit", err: errors.New("rate limit reached for gpt-4"), wantCode: pkgerrors.CodeRateLimited},
		{name: "quota exceeded", err: errors.New("quota exceeded for project"), wantCode: pkgerrors.CodeRateLimited},

		// Transient
		{name: "502", err: errors.New("HTTP 502 bad gateway"), wantCode: pkgerrors.CodeTransient},
		{name: "503", err: errors.New("HTTP 503"), wantCode: pkgerrors.CodeTransient},
		{name: "service unavailable", err: errors.New("service unavailable"), wantCode: pkgerrors.CodeTransient},
		{name: "bad gateway", err: errors.New("bad gateway"), wantCode: pkgerrors.CodeTransient},
		{name: "timeout", err: errors.New("request timeout"), wantCode: pkgerrors.CodeTransient},
		{name: "deadline exceeded", err: errors.New("context deadline exceeded"), wantCode: pkgerrors.CodeTransient},
		{name: "connection refused", err: errors.New("connection refused"), wantCode: pkgerrors.CodeTransient},
		{name: "connection reset", err: errors.New("connection reset by peer"), wantCode: pkgerrors.CodeTransient},
		{name: "EOF", err: errors.New("unexpected EOF"), wantCode: pkgerrors.CodeTransient},

		// Caller-side bugs (400/404/invalid) — typed as CodeInvalidInput so
		// downstream classifiers know not to retry them silently.
		{name: "400", err: errors.New("HTTP 400 bad request"), wantCode: pkgerrors.CodeInvalidInput},
		{name: "404", err: errors.New("HTTP 404 not found"), wantCode: pkgerrors.CodeInvalidInput},
		{name: "invalid", err: errors.New("invalid model name"), wantCode: pkgerrors.CodeInvalidInput},

		// Unknown shapes — unwrapped propagation so downstream defaults apply.
		{name: "unknown shape", err: errors.New("something exploded"), wantOrig: true},
		{name: "empty message", err: errors.New(""), wantOrig: true},

		// Order check: auth wins over generic 4xx phrasing.
		{
			name:     "401 with invalid token text",
			err:      errors.New("HTTP 401: invalid token"),
			wantCode: pkgerrors.CodeLLMAuth,
		},
		// Wrapped errors — classification still works on the message text.
		{
			name:     "wrapped 429",
			err:      fmt.Errorf("openai call failed: %w", errors.New("HTTP 429 too many requests")),
			wantCode: pkgerrors.CodeRateLimited,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyLLMError(tt.err)

			if tt.wantNil {
				if got != nil {
					t.Fatalf("got %v, want nil", got)
				}
				return
			}

			if tt.wantOrig {
				if got == nil {
					t.Fatalf("got nil, want original error returned unmodified")
				}
				// Should be exact same pointer/value, not a DomainError wrap.
				var de *pkgerrors.DomainError
				if errors.As(got, &de) {
					t.Fatalf("got DomainError wrap, expected original error unmodified: %v", got)
				}
				if got != tt.err {
					t.Fatalf("got different error: %v, want original: %v", got, tt.err)
				}
				return
			}

			if !pkgerrors.Is(got, tt.wantCode) {
				t.Fatalf("got code mismatch, want %q, error: %v", tt.wantCode, got)
			}
			// Verify the original error is wrapped (preservable via errors.Is).
			if !errors.Is(got, tt.err) {
				t.Fatalf("original error not wrapped: got %v, want unwrap to reveal %v", got, tt.err)
			}
		})
	}
}

// TestClassifyLLMError_PreservesOriginalError verifies that classification
// preserves the underlying error so callers can still use errors.Is/As
// against the original error chain.
func TestClassifyLLMError_PreservesOriginalError(t *testing.T) {
	original := errors.New("HTTP 429 too many requests")
	classified := classifyLLMError(original)

	if !errors.Is(classified, original) {
		t.Fatalf("classified error %v should wrap original %v", classified, original)
	}

	if !pkgerrors.Is(classified, pkgerrors.CodeRateLimited) {
		t.Fatalf("classified should have CodeRateLimited, got %v", classified)
	}
}
