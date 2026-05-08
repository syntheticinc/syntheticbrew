//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TC-SCH-10: POST /api/v1/schemas with entry_agent_id passed as agent NAME (not
// UUID) — fresh-DB single-apply scenario. Engine MUST resolve the name to UUID
// via resolveEntryAgentRef inside CreateSchema, not defer to a follow-up PATCH.
//
// Regression guard for chirp 1.1.2 dev-rollout bug #1: brewctl-style fresh-DB
// apply created the schema row with entry_agent_id = NULL because CreateSchema
// stored the empty / unresolved value as-is, while UpdateSchema (PATCH) was the
// only path calling resolveEntryAgentRef. After 1.1.3 both paths resolve.
func TestSCH10_CreateSchemaWithEntryAgentName(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	const agentName = "tc-sch-10-agent"
	const schemaName = "tc-sch-10-schema"

	// Create the agent first (mirrors brewctl's apply-order: agents → schemas).
	createAgentForTest(t, agentName)

	// POST schema sending agent NAME in entry_agent_id field — this is the wire
	// shape brewctl 0.2.3 produces on a fresh-DB apply where pre-resolution
	// from current state would have yielded an empty string under 0.2.2.
	resp := do(t, http.MethodPost, "/api/v1/schemas",
		mustJSON(map[string]any{
			"name":           schemaName,
			"entry_agent_id": agentName,
			"chat_enabled":   true,
		}), adminToken)
	assertStatusAny(t, resp, http.StatusOK, http.StatusCreated)

	// Single GET — entry_agent_name must already be populated. If FK was left
	// NULL by CreateSchema, this would come back as "" and the test fails.
	getResp := do(t, http.MethodGet, "/api/v1/schemas/"+schemaName, nil, adminToken)
	require.Equal(t, http.StatusOK, getResp.StatusCode)

	var got struct {
		EntryAgentName string `json:"entry_agent_name"`
	}
	require.NoError(t, json.Unmarshal(readBody(t, getResp), &got),
		"decode GET /schemas/%s body", schemaName)

	assert.Equal(t, agentName, got.EntryAgentName,
		"entry_agent_name must be populated on fresh-DB single apply (was the chirp 1.1.2 NULL bug)")
}

// TC-SCH-11: POST /api/v1/schemas with non-existent agent NAME → 400, not 500
// or silent NULL. Validates that resolveEntryAgentRef surfaces the failure
// loudly instead of accepting garbage.
func TestSCH11_CreateSchemaWithUnknownEntryAgentName_400(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	resp := do(t, http.MethodPost, "/api/v1/schemas",
		mustJSON(map[string]any{
			"name":           "tc-sch-11-schema",
			"entry_agent_id": "no-such-agent",
		}), adminToken)
	body := readBody(t, resp)

	require.Equal(t, http.StatusBadRequest, resp.StatusCode,
		"unknown entry_agent name must yield 400 InvalidInput, body=%s", body)
	assert.Contains(t, string(body), "agent not found",
		"error body should explain why: %s", body)
}
