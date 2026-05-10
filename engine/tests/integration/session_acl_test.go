//go:build integration

package integration

import (
	"net/http"
	"testing"

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

// TestSEC24_SessionCreate_APITokenWriteOwnUserSub — Phase 0 + Phase 2
// impersonation guard. Non-trusted actor (api_token without ScopeAdmin)
// trying to create a session under a different user_sub via the body field
// gets the body silently overwritten with the caller's identity. The
// canonical service identity is the api_token's name (info.Name), which the
// auth middleware now stamps into ctx.UserSub.
func TestSEC24_SessionCreate_APITokenWriteOwnUserSub(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	// Token has Sessions write scope but NOT admin. Sets up a client that
	// could call POST /sessions but cannot impersonate other user_subs.
	const tokenName = "tc-sec-24-sessions-write"
	tok := issueAPIToken(t, tokenName, 32768) // ScopeSessionsWrite

	resp := do(t, http.MethodPost, "/api/v1/sessions",
		mustJSON(map[string]any{
			"id":        "11111111-1111-4111-a111-111111111111",
			"user_sub":  "victim-user-sub", // attempted impersonation
			"schema_id": "00000000-0000-0000-0000-000000000000",
			"title":     "should belong to token name, not victim",
		}), tok)
	body := readBody(t, resp)
	assertStatusAny(t, resp, http.StatusOK, http.StatusCreated)

	var created struct {
		UserSub string `json:"user_sub"`
	}
	require.NoError(t, jsonUnmarshalOrNil(body, &created),
		"decode created session: body=%s", body)
	assert.Equal(t, tokenName, created.UserSub,
		"non-admin api_token must NOT impersonate via body.user_sub; got %q want %q",
		created.UserSub, tokenName)
	assert.NotEqual(t, "victim-user-sub", created.UserSub,
		"impersonation guard regression — body.user_sub leaked through")
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
func TestSEC28_SessionMetadata_RoundTrip(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	tok := issueAPIToken(t, "tc-sec-28-admin", 16) // admin

	created := do(t, http.MethodPost, "/api/v1/sessions",
		mustJSON(map[string]any{
			"id":        "22222222-2222-4222-a222-222222222222",
			"user_sub":  "tc-sec-28-user",
			"schema_id": "00000000-0000-0000-0000-000000000000",
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
