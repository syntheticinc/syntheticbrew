package http

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateResourceName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		// Happy path
		{"single char", "a", nil},
		{"kebab case", "support-handbook", nil},
		{"alphanumeric", "schema123", nil},
		{"numeric-led", "1schema", nil},
		{"hyphens", "a-b-c-d", nil},
		{"max length", strings.Repeat("a", 100), nil},

		// Empty
		{"empty", "", ErrNameEmpty},

		// Length
		{"101 chars", strings.Repeat("a", 101), ErrNameTooLong},
		{"500 chars", strings.Repeat("x", 500), ErrNameTooLong},

		// Format
		{"uppercase", "Foo", ErrInvalidName},
		{"slash", "foo/bar", ErrInvalidName},
		{"dot", "foo.bar", ErrInvalidName},
		{"underscore", "foo_bar", ErrInvalidName},
		{"space", "foo bar", ErrInvalidName},
		{"leading hyphen", "-foo", ErrInvalidName},
		{"trailing hyphen", "foo-", ErrInvalidName},
		{"only hyphens", "---", ErrInvalidName},
		{"unicode", "fôo", ErrInvalidName},
		{"percent", "foo%20bar", ErrInvalidName},
		{"hash", "foo#bar", ErrInvalidName},
		{"question", "foo?", ErrInvalidName},

		// UUID-shaped
		{"uuid v4", "550e8400-e29b-41d4-a716-446655440000", ErrUUIDShapedName},
		{"uuid lowercase", "00000000-0000-0000-0000-000000000001", ErrUUIDShapedName},

		// Reserved
		{"reserved chat", "chat", ErrReservedName},
		{"reserved agents", "agents", ErrReservedName},
		{"reserved agent-relations", "agent-relations", ErrReservedName},
		{"reserved memory", "memory", ErrReservedName},
		{"reserved files", "files", ErrReservedName},
		{"reserved health", "health", ErrReservedName},
		{"reserved auth", "auth", ErrReservedName},
		{"reserved tasks", "tasks", ErrReservedName},
		{"reserved models", "models", ErrReservedName},
		{"reserved knowledge-bases", "knowledge-bases", ErrReservedName},
		{"reserved schemas", "schemas", ErrReservedName},
		{"reserved mcp-servers", "mcp-servers", ErrReservedName},
		{"reserved tokens", "tokens", ErrReservedName},
		{"reserved sessions", "sessions", ErrReservedName},
		{"reserved metrics", "metrics", ErrReservedName},

		// Reserved-prefix is OK (only exact match is reserved)
		{"chat-prefixed", "chat-bot", nil},
		{"agents-suffixed", "my-agents", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ValidateResourceName(tc.input)
			if !errors.Is(got, tc.wantErr) {
				t.Fatalf("ValidateResourceName(%q) = %v, want %v", tc.input, got, tc.wantErr)
			}
		})
	}
}

func TestValidateResourceName_RejectsBeforeReachingRouter(t *testing.T) {
	// Sanity: every reserved word must itself fail validation. Belt-and-braces
	// against future devs adding a reserved entry without testing it.
	for reserved := range reservedNames {
		if err := ValidateResourceName(reserved); !errors.Is(err, ErrReservedName) {
			t.Errorf("reserved %q passed validation: got %v, want ErrReservedName", reserved, err)
		}
	}
}
