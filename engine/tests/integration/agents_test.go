//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TC-AGT-01: POST /agents creates a new agent and returns the name in the body.
func TestAGT01_CreateAgent(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	name := "tc-agt-01-agent"
	resp := do(t, http.MethodPost, "/api/v1/agents",
		mustJSON(map[string]any{
			"name":          name,
			"system_prompt": "test prompt",
		}), adminToken)
	body := readBody(t, resp)
	assertStatusAny(t, resp, http.StatusOK, http.StatusCreated)
	assert.Contains(t, string(body), name, "create response should contain the agent name")
}

// TC-AGT-02: GET /agents lists the created agent.
func TestAGT02_ListContainsCreated(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	name := "tc-agt-02-agent"
	createResp := do(t, http.MethodPost, "/api/v1/agents",
		mustJSON(map[string]any{"name": name, "system_prompt": "p"}), adminToken)
	_ = readBody(t, createResp)
	assertStatusAny(t, createResp, http.StatusOK, http.StatusCreated)

	listResp := do(t, http.MethodGet, "/api/v1/agents", nil, adminToken)
	body := readBody(t, listResp)
	require.Equal(t, http.StatusOK, listResp.StatusCode)
	assert.Contains(t, string(body), `"name":"`+name+`"`,
		"list should contain created agent: %s", body)
}

// TC-AGT-03: GET /agents/{name} on an existing agent → 200.
func TestAGT03_GetExisting(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	name := "tc-agt-03-agent"
	createResp := do(t, http.MethodPost, "/api/v1/agents",
		mustJSON(map[string]any{"name": name, "system_prompt": "p"}), adminToken)
	_ = readBody(t, createResp)
	assertStatusAny(t, createResp, http.StatusOK, http.StatusCreated)

	getResp := do(t, http.MethodGet, "/api/v1/agents/"+name, nil, adminToken)
	body := readBody(t, getResp)
	assert.Equal(t, http.StatusOK, getResp.StatusCode, "body=%s", body)
}

// TC-AGT-04: GET /agents/{name} on nonexistent agent → 404.
func TestAGT04_GetNonexistent(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	resp := do(t, http.MethodGet, "/api/v1/agents/does-not-exist", nil, adminToken)
	_ = readBody(t, resp)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TC-AGT-05: PUT /agents/{name} updates system_prompt; subsequent GET
// reflects the change.
func TestAGT05_UpdateAgent(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	name := "tc-agt-05-agent"
	createResp := do(t, http.MethodPost, "/api/v1/agents",
		mustJSON(map[string]any{"name": name, "system_prompt": "initial"}), adminToken)
	_ = readBody(t, createResp)
	assertStatusAny(t, createResp, http.StatusOK, http.StatusCreated)

	newPrompt := "updated-prompt-value-xyz"
	updResp := do(t, http.MethodPut, "/api/v1/agents/"+name,
		mustJSON(map[string]any{
			"name":          name,
			"system_prompt": newPrompt,
		}), adminToken)
	_ = readBody(t, updResp)
	assertStatusAny(t, updResp, http.StatusOK, http.StatusNoContent)

	getResp := do(t, http.MethodGet, "/api/v1/agents/"+name, nil, adminToken)
	body := readBody(t, getResp)
	require.Equal(t, http.StatusOK, getResp.StatusCode)
	assert.Contains(t, string(body), newPrompt,
		"GET after PUT should reflect updated prompt: %s", body)
}

// TC-AGT-06: DELETE /agents/{name} removes the agent; subsequent GET → 404.
func TestAGT06_DeleteAgent(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	name := "tc-agt-06-agent"
	createResp := do(t, http.MethodPost, "/api/v1/agents",
		mustJSON(map[string]any{"name": name, "system_prompt": "p"}), adminToken)
	_ = readBody(t, createResp)
	assertStatusAny(t, createResp, http.StatusOK, http.StatusCreated)

	delResp := do(t, http.MethodDelete, "/api/v1/agents/"+name, nil, adminToken)
	_ = readBody(t, delResp)
	assertStatusAny(t, delResp, http.StatusOK, http.StatusNoContent)

	getResp := do(t, http.MethodGet, "/api/v1/agents/"+name, nil, adminToken)
	_ = readBody(t, getResp)
	assert.Equal(t, http.StatusNotFound, getResp.StatusCode,
		"deleted agent must not be fetchable")
}

// TC-AGT-07: Duplicate name → 409 Conflict (422 also accepted).
func TestAGT07_DuplicateName(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	name := "tc-agt-07-agent"
	first := do(t, http.MethodPost, "/api/v1/agents",
		mustJSON(map[string]any{"name": name, "system_prompt": "first"}), adminToken)
	_ = readBody(t, first)
	assertStatusAny(t, first, http.StatusOK, http.StatusCreated)

	second := do(t, http.MethodPost, "/api/v1/agents",
		mustJSON(map[string]any{"name": name, "system_prompt": "second"}), adminToken)
	_ = readBody(t, second)
	assertStatusAny(t, second, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusBadRequest)
}

// TC-AGT-09: Immediate GET after create — no registry staleness.
func TestAGT09_ImmediateRead(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	name := "tc-agt-09-agent"
	createResp := do(t, http.MethodPost, "/api/v1/agents",
		mustJSON(map[string]any{"name": name, "system_prompt": "p"}), adminToken)
	_ = readBody(t, createResp)
	assertStatusAny(t, createResp, http.StatusOK, http.StatusCreated)

	getResp := do(t, http.MethodGet, "/api/v1/agents/"+name, nil, adminToken)
	_ = readBody(t, getResp)
	assert.Equal(t, http.StatusOK, getResp.StatusCode,
		"agent must be readable immediately after create")
}

// TC-AGT-10: public=true is accepted by the schema.
func TestAGT10_PublicFlag(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	resp := do(t, http.MethodPost, "/api/v1/agents",
		mustJSON(map[string]any{
			"name":          "tc-agt-10-agent",
			"system_prompt": "p",
			"public":        true,
		}), adminToken)
	body := readBody(t, resp)
	assertStatusAny(t, resp, http.StatusOK, http.StatusCreated)
	_ = body
}

// createTestAgentAndReturnID is a helper for the UUID-or-name resolver tests.
// Posts an agent, decodes the response, and returns (uuid, name).
func createTestAgentAndReturnID(t *testing.T, name string) (string, string) {
	t.Helper()
	resp := do(t, http.MethodPost, "/api/v1/agents",
		mustJSON(map[string]any{"name": name, "system_prompt": "p"}), adminToken)
	body := readBody(t, resp)
	assertStatusAny(t, resp, http.StatusOK, http.StatusCreated)

	var created struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(body, &created), "decode create body=%s", body)
	require.NotEmpty(t, created.ID, "agent id must be returned")
	require.Equal(t, name, created.Name)
	return created.ID, created.Name
}

// TC-AGT-11: PATCH /agents/{uuid} resolves to the agent by UUID and applies
// runtime cap fields. Closes the chirp-reported 1.1.6 bug where external
// consumers received 404 when using the UUID from GET /agents.
func TestAGT11_PatchByUUID(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	id, _ := createTestAgentAndReturnID(t, "tc-agt-11-agent")

	resp := do(t, http.MethodPatch, "/api/v1/agents/"+id,
		mustJSON(map[string]any{
			"max_steps":         80,
			"max_context_size":  128000,
			"max_turn_duration": 300,
		}), adminToken)
	body := readBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode, "PATCH by UUID must succeed: body=%s", body)

	var updated struct {
		MaxSteps        int `json:"max_steps"`
		MaxContextSize  int `json:"max_context_size"`
		MaxTurnDuration int `json:"max_turn_duration"`
	}
	require.NoError(t, json.Unmarshal(body, &updated))
	assert.Equal(t, 80, updated.MaxSteps)
	assert.Equal(t, 128000, updated.MaxContextSize)
	assert.Equal(t, 300, updated.MaxTurnDuration)
}

// TC-AGT-12: PATCH /agents/{name} still works after Fix B (no regression).
func TestAGT12_PatchByName(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	_, name := createTestAgentAndReturnID(t, "tc-agt-12-agent")

	resp := do(t, http.MethodPatch, "/api/v1/agents/"+name,
		mustJSON(map[string]any{"max_steps": 42}), adminToken)
	body := readBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode, "PATCH by name must still work: body=%s", body)
	assert.Contains(t, string(body), `"max_steps":42`)
}

// TC-AGT-13: PATCH /agents/{nonexistent-uuid} → 404 (info hiding for
// cross-tenant probes; same response as truly-not-found name).
func TestAGT13_PatchByUUIDUnknown(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	resp := do(t, http.MethodPatch, "/api/v1/agents/00000000-0000-0000-0000-000000000000",
		mustJSON(map[string]any{"max_steps": 99}), adminToken)
	body := readBody(t, resp)
	require.Equal(t, http.StatusNotFound, resp.StatusCode,
		"unknown UUID must yield 404, not 500: body=%s", body)
	assert.Contains(t, string(body), "agent not found")
}

// TC-AGT-14: GET /agents/{uuid} resolves via the same path (symmetry guard).
func TestAGT14_GetByUUID(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	id, name := createTestAgentAndReturnID(t, "tc-agt-14-agent")

	resp := do(t, http.MethodGet, "/api/v1/agents/"+id, nil, adminToken)
	body := readBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode, "GET by UUID must succeed: body=%s", body)
	assert.Contains(t, string(body), `"name":"`+name+`"`)
}

// TC-AGT-15: DELETE /agents/{uuid} resolves and tears down the agent.
func TestAGT15_DeleteByUUID(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	id, name := createTestAgentAndReturnID(t, "tc-agt-15-agent")

	resp := do(t, http.MethodDelete, "/api/v1/agents/"+id, nil, adminToken)
	_ = readBody(t, resp)
	assertStatusAny(t, resp, http.StatusOK, http.StatusNoContent)

	check := do(t, http.MethodGet, "/api/v1/agents/"+name, nil, adminToken)
	_ = readBody(t, check)
	assert.Equal(t, http.StatusNotFound, check.StatusCode, "agent must be gone after DELETE")
}
