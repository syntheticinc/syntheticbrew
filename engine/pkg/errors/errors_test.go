package errors

import (
	"errors"
	stderrors "errors"
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		code    string
		message string
		want    string
	}{
		{
			name:    "create error with code and message",
			code:    CodeInvalidInput,
			message: "invalid user input",
			want:    "[INVALID_INPUT] invalid user input",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := New(tt.code, tt.message)
			if err.Error() != tt.want {
				t.Errorf("New() error = %v, want %v", err.Error(), tt.want)
			}
		})
	}
}

func TestWrap(t *testing.T) {
	baseErr := errors.New("base error")

	tests := []struct {
		name    string
		err     error
		code    string
		message string
		want    string
	}{
		{
			name:    "wrap error with code and message",
			err:     baseErr,
			code:    CodeInternal,
			message: "internal error occurred",
			want:    "[INTERNAL_ERROR] internal error occurred: base error",
		},
		{
			name:    "wrap nil error returns nil",
			err:     nil,
			code:    CodeInternal,
			message: "should not create error",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Wrap(tt.err, tt.code, tt.message)
			if tt.err == nil {
				if err != nil {
					t.Errorf("Wrap() with nil error should return nil, got %v", err)
				}
				return
			}
			if err.Error() != tt.want {
				t.Errorf("Wrap() error = %v, want %v", err.Error(), tt.want)
			}
		})
	}
}

func TestIs(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
		want bool
	}{
		{
			name: "matches error code",
			err:  New(CodeNotFound, "not found"),
			code: CodeNotFound,
			want: true,
		},
		{
			name: "does not match error code",
			err:  New(CodeNotFound, "not found"),
			code: CodeInvalidInput,
			want: false,
		},
		{
			name: "non-domain error returns false",
			err:  errors.New("standard error"),
			code: CodeInternal,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Is(tt.err, tt.code); got != tt.want {
				t.Errorf("Is() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "get code from domain error",
			err:  New(CodeNotFound, "not found"),
			want: CodeNotFound,
		},
		{
			name: "get code from standard error returns internal",
			err:  errors.New("standard error"),
			want: CodeInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetCode(tt.err); got != tt.want {
				t.Errorf("GetCode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHelperFunctions(t *testing.T) {
	tests := []struct {
		name     string
		fn       func() *DomainError
		wantCode string
	}{
		{
			name:     "InvalidInput",
			fn:       func() *DomainError { return InvalidInput("invalid") },
			wantCode: CodeInvalidInput,
		},
		{
			name:     "NotFound",
			fn:       func() *DomainError { return NotFound("not found") },
			wantCode: CodeNotFound,
		},
		{
			name:     "AlreadyExists",
			fn:       func() *DomainError { return AlreadyExists("exists") },
			wantCode: CodeAlreadyExists,
		},
		{
			name:     "Unauthorized",
			fn:       func() *DomainError { return Unauthorized("unauthorized") },
			wantCode: CodeUnauthorized,
		},
		{
			name:     "Forbidden",
			fn:       func() *DomainError { return Forbidden("forbidden") },
			wantCode: CodeForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			if err.Code != tt.wantCode {
				t.Errorf("Code = %v, want %v", err.Code, tt.wantCode)
			}
		})
	}
}

func TestDeepestCode(t *testing.T) {
	if got := DeepestCode(nil); got != CodeInternal {
		t.Fatalf("nil: want %s, got %s", CodeInternal, got)
	}
	// plain error → internal
	if got := DeepestCode(stderrors.New("boom")); got != CodeInternal {
		t.Fatalf("plain: want %s, got %s", CodeInternal, got)
	}
	// typed unavailable wrapped under a generic internal wrap → recovers UNAVAILABLE
	inner := Unavailable("svc down", stderrors.New("circuit open"))
	wrapped := Wrap(inner, CodeInternal, "agent stream failed")
	if got := DeepestCode(wrapped); got != CodeUnavailable {
		t.Fatalf("wrapped: want %s, got %s", CodeUnavailable, got)
	}
	if got := UserMessage(wrapped); got != "svc down" {
		t.Fatalf("user message: want 'svc down', got %q", got)
	}
}

func TestUserMessage_UntypedReturnsGenericNotRaw(t *testing.T) {
	// An untyped error must never surface its raw string to the client — it can
	// carry provider URLs or wrapped technical detail.
	raw := stderrors.New("dial tcp 10.0.0.5:5432: connection refused")
	if got := UserMessage(raw); got != genericUserMessage {
		t.Fatalf("untyped error: want generic fallback, got %q", got)
	}
	// A curated typed message is still surfaced verbatim.
	typed := Unavailable("Service is temporarily unavailable.", raw)
	if got := UserMessage(typed); got != "Service is temporarily unavailable." {
		t.Fatalf("typed message: want it verbatim, got %q", got)
	}
}
