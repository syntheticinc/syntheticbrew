package tools

import (
	"errors"
	"strings"
	"testing"
)

// forbiddenDBTokens are internal database identifiers that SanitizeDBError must
// never surface to a client — constraint names, SQLSTATE codes, and the naming
// prefixes the engine uses for its constraints/indexes.
var forbiddenDBTokens = []string{"constraint", "SQLSTATE", "chk_", "idx_", "fk_", "violates"}

func TestSanitizeDBError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "unique violation by SQLSTATE",
			err:  errors.New(`ERROR: duplicate key value violates unique constraint "idx_models_name" (SQLSTATE 23505)`),
			want: "already exists",
		},
		{
			name: "unique violation sqlite phrasing",
			err:  errors.New("UNIQUE constraint failed: agents.name"),
			want: "already exists",
		},
		{
			name: "check violation by SQLSTATE",
			err:  errors.New(`ERROR: new row for relation "agents" violates check constraint "chk_agents_lifecycle" (SQLSTATE 23514)`),
			want: "one or more fields have an invalid value",
		},
		{
			name: "foreign key violation by SQLSTATE",
			err:  errors.New(`ERROR: update or delete on table "schemas" violates foreign key constraint "fk_sessions_schema_id" on table "sessions" (SQLSTATE 23503)`),
			want: "still referenced by other records",
		},
		{
			name: "not-null violation by SQLSTATE",
			err:  errors.New(`ERROR: null value in column "name" of relation "agents" violates not-null constraint (SQLSTATE 23502)`),
			want: "a required field is missing",
		},
		{
			name: "unknown error",
			err:  errors.New("dial tcp 127.0.0.1:5432: connect: connection refused"),
			want: "the operation could not be completed",
		},
		{
			name: "nil error",
			err:  nil,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeDBError(tt.err)
			if got != tt.want {
				t.Fatalf("SanitizeDBError = %q, want %q", got, tt.want)
			}
			for _, tok := range forbiddenDBTokens {
				if strings.Contains(got, tok) {
					t.Fatalf("sanitized message leaked forbidden token %q: %q", tok, got)
				}
			}
		})
	}
}
