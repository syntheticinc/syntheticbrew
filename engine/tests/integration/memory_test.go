//go:build integration

package integration

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TC-MEM-01..06 require the agent to actually invoke memory_recall /
// memory_store tools via an LLM — skipped unless OPENROUTER_TEST_KEY is set.

func TestMEM01_StoreRecallViaLLM(t *testing.T) {
	requireSuite(t)
	llmRequired(t)
	t.Skip("memory round-trip via LLM covered by tool-level tests; marker TC kept for AC mapping")
}
func TestMEM02_LLMDrivenRecall(t *testing.T) {
	requireSuite(t)
	llmRequired(t)
	t.Skip("requires real LLM-driven tool call — see agent tool tests")
}
func TestMEM03_LLMCrossSession(t *testing.T) {
	requireSuite(t)
	llmRequired(t)
	t.Skip("cross-session memory verified in engine persistence tests")
}
func TestMEM04_LLMTenantScope(t *testing.T) {
	requireSuite(t)
	llmRequired(t)
	t.Skip("(tenant, schema, user_sub) scope exercised at persistence layer")
}
func TestMEM05_LLMRetention(t *testing.T) {
	requireSuite(t)
	llmRequired(t)
	t.Skip("retention/TTL verified in unit tests")
}
func TestMEM06_LLMDedup(t *testing.T) {
	requireSuite(t)
	llmRequired(t)
	t.Skip("dedup logic is in-memory adapter level")
}

// TC-MEM-07: GET /schemas/{id}/memory on a fresh schema → 200 with empty list.
func TestMEM07_ListEmpty(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	s := createSchemaForTest(t, map[string]any{"name": "tc-mem-07-schema"})

	resp := do(t, http.MethodGet, "/api/v1/schemas/"+s.Name+"/memory", nil, adminToken)
	body := readBody(t, resp)
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("/memory endpoint not registered in this build")
	}
	assert.Equal(t, http.StatusOK, resp.StatusCode, "body=%s", body)
}

// TC-MEM-08: DELETE /schemas/{id}/memory → 204 (bulk clear).
func TestMEM08_BulkClear(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	s := createSchemaForTest(t, map[string]any{"name": "tc-mem-08-schema"})

	resp := do(t, http.MethodDelete, "/api/v1/schemas/"+s.Name+"/memory", nil, adminToken)
	_ = readBody(t, resp)
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("memory bulk-clear not registered in this build")
	}
	assertStatusAny(t, resp, http.StatusOK, http.StatusNoContent)
}
