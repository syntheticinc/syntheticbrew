//go:build integration

package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSchemaDelete_WithSessions_Returns409NoLeak is the BUG E guard: deleting a
// schema that still has chat sessions must return a clean 409 Conflict, NOT a
// 500 that leaks the raw Postgres foreign-key/SQLSTATE string
// (fk_sessions_schema_id … 23503) to the client. RED baseline is the captured
// live-prod behavior (HTTP 500 + raw FK); this asserts the fixed behavior.
func TestSchemaDelete_WithSessions_Returns409NoLeak(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })
	require.NotNil(t, testDB, "integration suite must expose testDB")

	s := createSchemaForTest(t, map[string]any{"name": "tc-sch-del-conflict"})

	const sessID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	require.NoError(t, testDB.WithContext(context.Background()).Exec(
		`INSERT INTO sessions (id, schema_id, user_sub, status, metadata, created_at, updated_at)
		 VALUES (?::uuid, ?::uuid, ?, 'active', '{}'::jsonb, now(), now())`,
		sessID, s.ID, "e2e-conflict-user").Error, "seed a session referencing the schema")

	resp := do(t, http.MethodDelete, "/api/v1/schemas/"+s.Name, nil, adminToken)
	body := string(readBody(t, resp))

	assert.Equal(t, http.StatusConflict, resp.StatusCode,
		"deleting a schema that still has sessions must be 409, not 500; body=%s", body)

	// No raw Postgres internals may reach the client.
	low := strings.ToLower(body)
	for _, leak := range []string{"23503", "fk_", "constraint", "sqlstate"} {
		assert.NotContains(t, low, leak,
			"response must not leak internal detail %q; body=%s", leak, body)
	}
}
