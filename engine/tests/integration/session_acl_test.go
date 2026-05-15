//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Engine 1.1.4 — auth scope sweep + session ACL hardening regression suite.
//
// /sessions endpoints migrated from RequireAdminSession to RequireScope. The
// new scopes (ScopeSessionsRead = 16384, ScopeSessionsWrite = 32768) gate the
// mount; ScopeAdmin (=16) still passes via the superscope bypass inside
// RequireScope. These tests exercise the scope enforcement layer on the
// authoritative single-tenant CE stack — multi-tenant cross-user ACL lives
// in EE integration tests (see bytebrew-ee/tests/integration/session_acl_*.go).

// helper — issues an api_token via the admin route, returns the raw token.
// Skips the test if /api/v1/auth/tokens is not registered in this build.
func issueAPIToken(t *testing.T, name string, scopesMask int) string {
	t.Helper()
	resp := do(t, http.MethodPost, "/api/v1/auth/tokens",
		mustJSON(map[string]any{
			"name":        name,
			"scopes_mask": scopesMask,
		}), adminToken)
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("POST /api/v1/auth/tokens not registered in this build")
	}
	body := readBody(t, resp)
	require.Equal(t, http.StatusCreated, resp.StatusCode,
		"create api_token (%s): got %d, body=%s", name, resp.StatusCode, body)

	var created struct {
		Token string `json:"token"`
	}
	require.NoError(t, jsonUnmarshalOrNil(body, &created))
	require.NotEmpty(t, created.Token, "response must include raw token: %s", body)
	return created.Token
}

// TestSEC20_SessionList_NoToken_401 — SCC-01 GATE: unauthenticated request
// must yield 401 (not 403, not silently 200). Auth middleware rejects before
// scope check.
func TestSEC20_SessionList_NoToken_401(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	resp := do(t, http.MethodGet, "/api/v1/sessions", nil, "")
	_ = readBody(t, resp)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"unauthenticated /api/v1/sessions must yield 401 SCC-01")
}

// TestSEC21_SessionList_APITokenNoSessionScope_403 — api_token with a scope
// that does NOT include ScopeSessionsRead or ScopeAdmin must be rejected by
// RequireScope, even though the token is otherwise valid. Regression guard
// for the 1.1.4 scope-sweep — ensures we did not accidentally drop the gate.
func TestSEC21_SessionList_APITokenNoSessionScope_403(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	// ScopeChat (=1) only — does not cover sessions:read.
	tok := issueAPIToken(t, "tc-sec-21-chat-only", 1)

	resp := do(t, http.MethodGet, "/api/v1/sessions", nil, tok)
	_ = readBody(t, resp)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode,
		"api_token without ScopeSessionsRead/ScopeAdmin must be 403")
}

// TestSEC22_SessionList_APITokenSessionsRead_200 — narrow-scope api_token with
// exactly ScopeSessionsRead (=16384) is the canonical chirp ai-assistant
// proxy use case. Authorizes GET /sessions tenant-wide.
func TestSEC22_SessionList_APITokenSessionsRead_200(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	tok := issueAPIToken(t, "tc-sec-22-sessions-read", 16384)

	resp := do(t, http.MethodGet, "/api/v1/sessions", nil, tok)
	body := readBody(t, resp)
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"api_token with ScopeSessionsRead must authorize GET /sessions; body=%s", body)
}

// TestSEC23_SessionList_APITokenScopeAdmin_200 — ScopeAdmin acts as the
// superscope per RequireScope; existing admin tokens continue to work
// without modification.
func TestSEC23_SessionList_APITokenScopeAdmin_200(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	// ScopeAdmin (=16) — superscope.
	tok := issueAPIToken(t, "tc-sec-23-admin", 16)

	resp := do(t, http.MethodGet, "/api/v1/sessions", nil, tok)
	body := readBody(t, resp)
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"api_token with ScopeAdmin must authorize GET /sessions; body=%s", body)
}

// TestSEC24_SessionCreate_APITokenTrustedProxy — by-design contract pinned
// in docs/architecture/auth-scopes.md (1.1.5): api_token actors are treated
// as trusted proxies (chirp's ai-assistant pattern) and CAN attribute
// sessions to arbitrary end-user `user_sub` values via the body field. This
// is intentional — the proxy sits between engine and many end-users and
// needs to namespace sessions per its own users.
//
// The end-user impersonation guard runs only against regular end-user JWT
// actors (which the CE local-admin token isn't). For that path, see the EE
// integration suite (bytebrew-ee/tests/integration/session_acl_*.go).
//
// This test is the regression guard for the trusted-proxy contract: any
// silent change that strips body.user_sub for api_tokens would break chirp
// and any other ai-assistant proxy that relies on the documented behavior.
//
// 1.1.5 update: schema_id is resolved via tenant-scoped lookup before the
// session is persisted, so the test seeds a real schema instead of passing
// the zero UUID (which 1.1.5+ rejects with 400 InvalidInput).
func TestSEC24_SessionCreate_APITokenTrustedProxy(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	schema := createSchemaForTest(t, map[string]any{"name": "tc-sec-24-schema"})

	// Token has Sessions write scope but NOT admin. Trusted-proxy pattern
	// applies to any api_token regardless of scope — proxy identity is the
	// actor type, not the scope bitmask.
	const tokenName = "tc-sec-24-sessions-write"
	tok := issueAPIToken(t, tokenName, 32768) // ScopeSessionsWrite

	const endUserSub = "end-user-behind-proxy"
	resp := do(t, http.MethodPost, "/api/v1/sessions",
		mustJSON(map[string]any{
			"id":        "11111111-1111-4111-a111-111111111111",
			"user_sub":  endUserSub, // proxy attributing session to its end-user
			"schema_id": schema.ID,
			"title":     "trusted-proxy attributes session to end-user",
		}), tok)
	body := readBody(t, resp)
	assertStatusAny(t, resp, http.StatusOK, http.StatusCreated)

	var created struct {
		UserSub string `json:"user_sub"`
	}
	require.NoError(t, jsonUnmarshalOrNil(body, &created),
		"decode created session: body=%s", body)
	assert.Equal(t, endUserSub, created.UserSub,
		"api_token (trusted proxy) must preserve body.user_sub per docs/architecture/auth-scopes.md; got %q want %q",
		created.UserSub, endUserSub)
}

// TestSEC25_SessionGetByID_NotExist_404 — SCC-02 GATE: non-existent session
// UUID returns 404 (does not 500, does not 200 leak). Baseline for the ACL
// 404-on-cross-user response in EE tests.
func TestSEC25_SessionGetByID_NotExist_404(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	tok := issueAPIToken(t, "tc-sec-25-admin", 16) // admin

	const nonExistentID = "deadbeef-dead-4ead-bead-deadbeefdead"
	resp := do(t, http.MethodGet, "/api/v1/sessions/"+nonExistentID, nil, tok)
	_ = readBody(t, resp)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"non-existent session must yield 404 SCC-02")
}

// TestSEC26_AuditEndpoint_APITokenAuditRead_200 — regression: /audit was
// admin-JWT-only on 1.1.3; 1.1.4 migrated to RequireScope(ScopeAuditRead).
// Programmatic clients can now read audit logs with a narrow scope.
func TestSEC26_AuditEndpoint_APITokenAuditRead_200(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	tok := issueAPIToken(t, "tc-sec-26-audit-read", 262144) // ScopeAuditRead

	resp := do(t, http.MethodGet, "/api/v1/audit", nil, tok)
	body := readBody(t, resp)
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"api_token with ScopeAuditRead must authorize GET /audit; body=%s", body)
}

// TestSEC27_ToolMetadata_APITokenToolsRead_200 — same shape: /tools/metadata
// migrated to RequireScope(ScopeToolsRead).
func TestSEC27_ToolMetadata_APITokenToolsRead_200(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	tok := issueAPIToken(t, "tc-sec-27-tools-read", 2097152) // ScopeToolsRead

	resp := do(t, http.MethodGet, "/api/v1/tools/metadata", nil, tok)
	body := readBody(t, resp)
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"api_token with ScopeToolsRead must authorize GET /tools/metadata; body=%s", body)
}

// TestSEC28_SessionMetadata_RoundTrip — Phase 4 sanity: the new opaque
// metadata JSONB column accepts client-supplied JSON and returns it verbatim.
// Engine never parses; this test asserts persistence + shape preservation.
//
// 1.1.5 update: schema_id now resolves via tenant-scoped lookup, so we
// create a real schema first (was a stub UUID under 1.1.4 — passed through
// without validation).
func TestSEC28_SessionMetadata_RoundTrip(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	tok := issueAPIToken(t, "tc-sec-28-admin", 16) // admin
	schema := createSchemaForTest(t, map[string]any{"name": "tc-sec-28-schema"})

	created := do(t, http.MethodPost, "/api/v1/sessions",
		mustJSON(map[string]any{
			"id":        "22222222-2222-4222-a222-222222222222",
			"user_sub":  "tc-sec-28-user",
			"schema_id": schema.ID,
			"metadata":  map[string]any{"org_id": "org-abc", "tier": "free"},
		}), tok)
	body := readBody(t, created)
	assertStatusAny(t, created, http.StatusOK, http.StatusCreated)

	var resp map[string]any
	require.NoError(t, jsonUnmarshalOrNil(body, &resp), "decode created: %s", body)
	meta, _ := resp["metadata"].(map[string]any)
	require.NotNil(t, meta, "metadata field must round-trip on POST response: %s", body)
	assert.Equal(t, "org-abc", meta["org_id"])
	assert.Equal(t, "free", meta["tier"])

	getResp := do(t, http.MethodGet, "/api/v1/sessions/22222222-2222-4222-a222-222222222222", nil, tok)
	getBody := readBody(t, getResp)
	require.Equal(t, http.StatusOK, getResp.StatusCode, "GET after POST: body=%s", getBody)

	var got map[string]any
	require.NoError(t, jsonUnmarshalOrNil(getBody, &got))
	gotMeta, _ := got["metadata"].(map[string]any)
	require.NotNil(t, gotMeta, "metadata field must round-trip on GET: %s", getBody)
	assert.Equal(t, "org-abc", gotMeta["org_id"])
}

// Engine 1.1.5 — UUID-or-name resolver on body FK refs.

// TestSEC29_SessionCreate_SchemaIDByName_201 — operator-declared schema name
// passes through `resolveSchemaRef` in sessionServiceHTTPAdapter; engine
// resolves to the schema UUID via tenant-scoped lookup. This was the chirp
// fourth-follow-up #1 ask: POST /sessions returned 500 SQLSTATE 22P02 on
// `{"schema_id":"chirp"}` before 1.1.5.
func TestSEC29_SessionCreate_SchemaIDByName_201(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	tok := issueAPIToken(t, "tc-sec-29-admin", 16) // admin
	schema := createSchemaForTest(t, map[string]any{"name": "tc-sec-29-schema"})

	created := do(t, http.MethodPost, "/api/v1/sessions",
		mustJSON(map[string]any{
			"user_sub":  "tc-sec-29-user",
			"schema_id": schema.Name, // operator-declared name, not UUID
		}), tok)
	body := readBody(t, created)
	assertStatusAny(t, created, http.StatusOK, http.StatusCreated)

	var resp map[string]any
	require.NoError(t, jsonUnmarshalOrNil(body, &resp), "decode created: %s", body)
	gotSchemaID, _ := resp["schema_id"].(string)
	assert.Equal(t, schema.ID, gotSchemaID,
		"engine must resolve schema name → UUID on POST /sessions (chirp #1 fix); got %q want %q",
		gotSchemaID, schema.ID)
}

// TestSEC30_SessionCreate_SchemaIDUnknownName_400 — unknown schema reference
// must yield 400 InvalidInput (not 500 SQL leakage). Mirrors the
// pkgerrors.InvalidInput → writeDomainError mapping used by
// resolveAgentModel / resolveEntryAgentRef.
func TestSEC30_SessionCreate_SchemaIDUnknownName_400(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	tok := issueAPIToken(t, "tc-sec-30-admin", 16)

	resp := do(t, http.MethodPost, "/api/v1/sessions",
		mustJSON(map[string]any{
			"user_sub":  "tc-sec-30-user",
			"schema_id": "no-such-schema-name",
		}), tok)
	body := readBody(t, resp)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode,
		"unknown schema ref must yield 400 InvalidInput (not 500 SQL); body=%s", body)
	assert.Contains(t, string(body), "schema not found",
		"error message must surface the unresolved ref: %s", body)
}

// TestSEC31_SessionCreate_SchemaIDByUUID_201 — backwards-compatible UUID
// path. Pre-1.1.5 clients that passed schema_id as a UUID continue to work.
// Resolver branches on isUUID; UUID path verifies tenant ownership and
// returns the same value.
func TestSEC31_SessionCreate_SchemaIDByUUID_201(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	tok := issueAPIToken(t, "tc-sec-31-admin", 16)
	schema := createSchemaForTest(t, map[string]any{"name": "tc-sec-31-schema"})

	created := do(t, http.MethodPost, "/api/v1/sessions",
		mustJSON(map[string]any{
			"user_sub":  "tc-sec-31-user",
			"schema_id": schema.ID, // explicit UUID — back-compat path
		}), tok)
	body := readBody(t, created)
	assertStatusAny(t, created, http.StatusOK, http.StatusCreated)

	var resp map[string]any
	require.NoError(t, jsonUnmarshalOrNil(body, &resp))
	gotSchemaID, _ := resp["schema_id"].(string)
	assert.Equal(t, schema.ID, gotSchemaID, "UUID path must pass through unchanged")
}

// TestSEC32_KBCreate_EmbeddingModelByName_201 — symmetric to TestSEC29 on
// the KB endpoint. POST /api/v1/knowledge-bases accepts embedding_model_id
// as either UUID or tenant-local model name via resolveEmbeddingModelRef.
// kind=embedding check is preserved.
func TestSEC32_KBCreate_EmbeddingModelByName_201(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	tok := issueAPIToken(t, "tc-sec-32-admin", 16)

	// Create an embedding model first (createModelForTest defaults to chat).
	embedResp := do(t, http.MethodPost, "/api/v1/models",
		mustJSON(map[string]any{
			"name":       "tc-sec-32-embed",
			"type":       "openai_compatible",
			"kind":       "embedding",
			"model_name": "test-embedding",
			"api_key":    "test-key",
			"base_url":   "https://api.test.com",
		}), adminToken)
	assertStatusAny(t, embedResp, http.StatusOK, http.StatusCreated)
	_ = readBody(t, embedResp)

	kbResp := do(t, http.MethodPost, "/api/v1/knowledge-bases",
		mustJSON(map[string]any{
			"name":               "tc-sec-32-kb",
			"description":        "kb with embedding model by name",
			"embedding_model_id": "tc-sec-32-embed", // operator-declared name
		}), tok)
	body := readBody(t, kbResp)
	assertStatusAny(t, kbResp, http.StatusOK, http.StatusCreated)

	var got map[string]any
	require.NoError(t, jsonUnmarshalOrNil(body, &got), "decode KB: %s", body)
	embedID, _ := got["embedding_model_id"].(string)
	assert.NotEmpty(t, embedID, "engine must resolve embedding_model name → UUID: %s", body)
}

// TestSEC33_KBCreate_EmbeddingModelUnknownName_400 — unknown embedding model
// reference must yield 400 InvalidInput. Engine's pre-1.1.5 raw SQL fall-
// through could have produced 500 on certain inputs; 1.1.5 normalises to
// a clean DomainError mapping.
func TestSEC33_KBCreate_EmbeddingModelUnknownName_400(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	tok := issueAPIToken(t, "tc-sec-33-admin", 16)

	resp := do(t, http.MethodPost, "/api/v1/knowledge-bases",
		mustJSON(map[string]any{
			"name":               "tc-sec-33-kb",
			"embedding_model_id": "no-such-embedding-model",
		}), tok)
	body := readBody(t, resp)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode,
		"unknown embedding model must yield 400; body=%s", body)
	assert.Contains(t, string(body), "embedding model not found",
		"error must name the unresolved ref: %s", body)
}

// TestSEC34_SessionList_ResponseIncludesPerPageMax — Phase 3: server surfaces
// the enforced per_page upper bound (currently 100) so paginating clients
// can detect runaway loops without out-of-band knowledge. Additive JSON
// field, no breaking impact on existing parsers.
func TestSEC34_SessionList_ResponseIncludesPerPageMax(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	tok := issueAPIToken(t, "tc-sec-34-admin", 16)

	resp := do(t, http.MethodGet, "/api/v1/sessions", nil, tok)
	body := readBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode, "GET /sessions: body=%s", body)

	var got map[string]any
	require.NoError(t, jsonUnmarshalOrNil(body, &got))
	maxVal, ok := got["per_page_max"].(float64) // JSON numbers decode to float64
	require.True(t, ok, "response must include per_page_max field: %s", body)
	assert.Equal(t, float64(100), maxVal,
		"per_page_max must equal SessionPaginationMaxPerPage (100)")
}

// TestSEC35_ToolResultIsErrorRoundTrip verifies GET /sessions/{id}/messages
// surfaces payload.is_error=true for tool_result rows that originated from
// a failed tool call (MCP isError, circuit-breaker open, [ERROR]-prefixed
// content, Eino OnToolError). Happy-path rows must omit the field for
// backwards compat.
func TestSEC35_ToolResultIsErrorRoundTrip(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	schema := createSchemaForTest(t, map[string]any{
		"name":         "tc-sec-35-schema",
		"chat_enabled": false,
	})

	sessionID := "55555555-5555-4555-a555-555555555555"
	resp := do(t, http.MethodPost, "/api/v1/sessions",
		mustJSON(map[string]any{
			"id":        sessionID,
			"user_sub":  "tc-sec-35-user",
			"schema_id": schema.ID,
			"title":     "round-trip is_error",
		}), adminToken)
	body := readBody(t, resp)
	assertStatusAny(t, resp, http.StatusOK, http.StatusCreated)
	_ = body

	require.NotNil(t, testDB, "integration suite must expose testDB")

	insert := func(eventType, callID string, payload map[string]any) {
		t.Helper()
		raw, err := json.Marshal(payload)
		require.NoError(t, err)
		require.NoError(t, testDB.WithContext(context.Background()).Exec(
			`INSERT INTO messages (id, session_id, event_type, call_id, payload, created_at)
			 VALUES (?, ?, ?, ?, ?::jsonb, ?)`,
			uuid.New().String(), sessionID, eventType, callID, string(raw), time.Now(),
		).Error)
	}

	// Failed tool call: payload carries is_error:true.
	insert("tool_call", "call-err", map[string]any{
		"tool":      "rule.list",
		"arguments": map[string]string{},
	})
	insert("tool_result", "call-err", map[string]any{
		"tool":     "rule.list",
		"content":  "[UNAVAILABLE] circuit breaker open for chirp-platform",
		"is_error": true,
	})

	// Successful tool call: payload must omit is_error.
	insert("tool_call", "call-ok", map[string]any{
		"tool":      "echo_message",
		"arguments": map[string]string{"text": "hi"},
	})
	insert("tool_result", "call-ok", map[string]any{
		"tool":    "echo_message",
		"content": "ok",
	})

	resp = do(t, http.MethodGet, "/api/v1/sessions/"+sessionID+"/messages", nil, adminToken)
	body = readBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode, "GET messages: body=%s", body)

	var events []struct {
		EventType string          `json:"event_type"`
		CallID    string          `json:"call_id"`
		Payload   json.RawMessage `json:"payload"`
	}
	require.NoError(t, json.Unmarshal(body, &events), "decode events: %s", body)

	var errResult, okResult json.RawMessage
	for _, e := range events {
		if e.EventType != "tool_result" {
			continue
		}
		switch e.CallID {
		case "call-err":
			errResult = e.Payload
		case "call-ok":
			okResult = e.Payload
		}
	}
	require.NotEmpty(t, errResult, "tool_result for call-err must be returned: %s", body)
	require.NotEmpty(t, okResult, "tool_result for call-ok must be returned: %s", body)

	var errP struct {
		IsError bool `json:"is_error"`
	}
	require.NoError(t, json.Unmarshal(errResult, &errP))
	assert.True(t, errP.IsError,
		"failed tool_result must surface is_error=true on the wire; got %s", string(errResult))

	assert.NotContains(t, string(okResult), "is_error",
		"happy-path tool_result must omit is_error for back-compat; got %s", string(okResult))
}
