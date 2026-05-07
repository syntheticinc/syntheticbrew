//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// llmRequired skips tests that need a real LLM. Most CI runs lack the key, so
// the SSE-driven cases are gated behind OPENROUTER_TEST_KEY.
func llmRequired(t *testing.T) {
	t.Helper()
	if os.Getenv("OPENROUTER_TEST_KEY") == "" {
		t.Skip("OPENROUTER_TEST_KEY not set — skipping real-LLM chat test")
	}
}

// TC-CHAT-01: SSE chat against a chat-enabled schema emits at least one
// event. Requires a real LLM key.
func TestCHAT01_SSEEvents(t *testing.T) {
	requireSuite(t)
	llmRequired(t)
	t.Cleanup(func() { truncateTables(t) })

	// Schema must have an entry_agent_id — chat dispatcher needs an agent to
	// route the message. Pre-1.1.0 this test omitted entry_agent and the
	// engine 400'd at chat_http_adapter.go (ErrNoEntryAgent). Seed an agent
	// then point the schema's entry_agent_id at it. agent.id is a UUID FK,
	// not a route param, so engine 1.1.0 name-keyed migration does not
	// affect this field.
	agentBody := createAgentForTest(t, "tc-chat-01-agent")
	var agent struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(agentBody, &agent))
	require.NotEmpty(t, agent.ID)

	s := createSchemaForTest(t, map[string]any{
		"name":           "tc-chat-01-schema",
		"chat_enabled":   true,
		"entry_agent_id": agent.ID,
	})

	body := mustJSONBytes(map[string]any{"message": "Hello"})
	req, err := http.NewRequest(http.MethodPost,
		baseURL+"/api/v1/schemas/"+s.Name+"/chat",
		bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+adminToken)

	// Short timeout — we only need "server produced something".
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	buf := make([]byte, 4096)
	n, readErr := resp.Body.Read(buf)
	// EOF after some bytes is fine; we just need evidence the server streamed.
	assert.True(t, n > 0 || readErr == io.EOF,
		"expected at least some SSE output (n=%d err=%v)", n, readErr)
}

// TC-CHAT-02: Chat against a schema WITHOUT chat_enabled should be rejected.
func TestCHAT02_ChatDisabledSchema(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	s := createSchemaForTest(t, map[string]any{
		"name":         "tc-chat-02-schema",
		"chat_enabled": false,
	})

	resp := do(t, http.MethodPost, "/api/v1/schemas/"+s.Name+"/chat",
		mustJSON(map[string]any{"message": "hi"}), adminToken)
	_ = readBody(t, resp)
	// Expect a 4xx, never 5xx.
	assert.Less(t, resp.StatusCode, 500, "chat on disabled schema must not 5xx")
	assertStatusAny(t, resp, http.StatusBadRequest, http.StatusUnprocessableEntity,
		http.StatusForbidden, http.StatusNotFound, http.StatusConflict)
}

// TC-CHAT-03: Chat on a nonexistent schema → 404.
//
// Engine 1.1.0+: URL is name-keyed. Sends a valid-format name that simply
// doesn't exist in the tenant — must return 404 without leaking existence.
func TestCHAT03_UnknownSchema(t *testing.T) {
	requireSuite(t)

	resp := do(t, http.MethodPost, "/api/v1/schemas/does-not-exist/chat",
		mustJSON(map[string]any{"message": "hi"}), adminToken)
	_ = readBody(t, resp)
	assertStatusAny(t, resp, http.StatusNotFound)
}

// TC-CHAT-04: After a successful chat, session appears in GET /sessions.
// Requires live LLM — skip otherwise.
func TestCHAT04_SessionAppears(t *testing.T) {
	requireSuite(t)
	llmRequired(t)
	t.Cleanup(func() { truncateTables(t) })

	s := createSchemaForTest(t, map[string]any{
		"name":         "tc-chat-04-schema",
		"chat_enabled": true,
	})

	// Fire a chat — short read so we don't block the whole test.
	body := mustJSONBytes(map[string]any{"message": "Hi"})
	req, _ := http.NewRequest(http.MethodPost,
		baseURL+"/api/v1/schemas/"+s.Name+"/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	chatResp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, chatResp.Body)
	_ = chatResp.Body.Close()

	listResp := do(t, http.MethodGet, "/api/v1/sessions", nil, adminToken)
	listBody := readBody(t, listResp)
	require.Equal(t, http.StatusOK, listResp.StatusCode)
	assert.NotEmpty(t, listBody, "sessions list should not be empty after a chat")
}

// TC-CHAT-05: GET /sessions/{id}/messages on a fresh session id — 200 or 404.
// (We don't strictly depend on OPENROUTER here — the shape test is enough.)
func TestCHAT05_MessagesEndpoint(t *testing.T) {
	requireSuite(t)

	// Use an implausible session id — expect 404 (not found), never 5xx.
	resp := do(t, http.MethodGet,
		"/api/v1/sessions/sess-does-not-exist/messages", nil, adminToken)
	_ = readBody(t, resp)
	if resp.StatusCode >= 500 {
		t.Fatalf("messages endpoint must not 5xx: %d", resp.StatusCode)
	}
}

// TC-CHAT-06: GET /sessions without token → 401.
func TestCHAT06_SessionsRequireAuth(t *testing.T) {
	requireSuite(t)

	resp := do(t, http.MethodGet, "/api/v1/sessions", nil, "")
	_ = readBody(t, resp)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// TC-CHAT-07: GET /sessions returns 200 (possibly empty list).
func TestCHAT07_SessionsList(t *testing.T) {
	requireSuite(t)

	resp := do(t, http.MethodGet, "/api/v1/sessions", nil, adminToken)
	body := readBody(t, resp)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "body=%s", body)
	// Accept any JSON-valid shape.
	if len(body) > 0 {
		var anyVal any
		assert.NoError(t, json.Unmarshal(body, &anyVal), "body=%s", body)
	}
}

// TC-CHAT-08: GET /sessions/{id} on an unknown id — 4xx (not 5xx).
func TestCHAT08_SessionByIDShape(t *testing.T) {
	requireSuite(t)

	resp := do(t, http.MethodGet, "/api/v1/sessions/sess-unknown", nil, adminToken)
	_ = readBody(t, resp)
	if resp.StatusCode >= 500 {
		t.Fatalf("GET /sessions/{id} unknown must not 5xx: %d", resp.StatusCode)
	}
}
