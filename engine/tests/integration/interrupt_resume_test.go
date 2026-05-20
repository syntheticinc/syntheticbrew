//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// HITL Interrupt Primitive — POST body validation tests (engine 1.2.0).
//
// These cover the validation layer of the resume_interrupt path without
// requiring a real LLM call. The full happy-path flow (agent calls
// show_structured_output → engine emits interrupt_request SSE → user
// resumes → assistant continues) needs an LLM and is gated behind
// OPENROUTER_TEST_KEY in the existing TestCHAT01 — see chat_sse_test.go.

func TestINTERRUPT01_RejectsBothMessageAndResume(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	agentBody := createAgentForTest(t, "tc-int-01-agent")
	var agent struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(agentBody, &agent))
	s := createSchemaForTest(t, map[string]any{
		"name":           "tc-int-01-schema",
		"chat_enabled":   true,
		"entry_agent_id": agent.ID,
	})

	resp := do(t, http.MethodPost, "/api/v1/schemas/"+s.Name+"/chat",
		mustJSON(map[string]any{
			"message": "hi",
			"resume_interrupt": map[string]any{
				"interrupt_id": "00000000-0000-0000-0000-000000000001",
				"payload":      map[string]any{"answers": []any{}},
			},
		}), adminToken)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode,
		"engine must reject body with both message AND resume_interrupt")
}

func TestINTERRUPT02_RejectsEmptyBody(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	agentBody := createAgentForTest(t, "tc-int-02-agent")
	var agent struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(agentBody, &agent))
	s := createSchemaForTest(t, map[string]any{
		"name":           "tc-int-02-schema",
		"chat_enabled":   true,
		"entry_agent_id": agent.ID,
	})

	resp := do(t, http.MethodPost, "/api/v1/schemas/"+s.Name+"/chat",
		mustJSON(map[string]any{}), adminToken)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode,
		"engine must reject body with neither message NOR resume_interrupt")
}

func TestINTERRUPT03_UnknownInterruptIDReturns404(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	agentBody := createAgentForTest(t, "tc-int-03-agent")
	var agent struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(agentBody, &agent))
	s := createSchemaForTest(t, map[string]any{
		"name":           "tc-int-03-schema",
		"chat_enabled":   true,
		"entry_agent_id": agent.ID,
	})

	resp := do(t, http.MethodPost, "/api/v1/schemas/"+s.Name+"/chat",
		mustJSON(map[string]any{
			"session_id": "00000000-0000-0000-0000-000000000001",
			"resume_interrupt": map[string]any{
				"interrupt_id": "00000000-0000-0000-0000-000000000999",
				"payload":      map[string]any{"answers": []any{}},
			},
		}), adminToken)
	defer func() { _ = resp.Body.Close() }()

	// NotFound is what the resume usecase returns for an unknown interrupt_id.
	// Engine wraps DomainError → HTTP, NotFound → 404.
	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"unknown interrupt_id must return 404 (no existence leak)")
}

func TestINTERRUPT04_MissingSessionIDReturns400(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	agentBody := createAgentForTest(t, "tc-int-04-agent")
	var agent struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(agentBody, &agent))
	s := createSchemaForTest(t, map[string]any{
		"name":           "tc-int-04-schema",
		"chat_enabled":   true,
		"entry_agent_id": agent.ID,
	})

	// session_id is required for resume_interrupt — the resume usecase rejects
	// empty session_id with InvalidInput → 400.
	resp := do(t, http.MethodPost, "/api/v1/schemas/"+s.Name+"/chat",
		mustJSON(map[string]any{
			"resume_interrupt": map[string]any{
				"interrupt_id": "00000000-0000-0000-0000-000000000001",
				"payload":      map[string]any{"answers": []any{}},
			},
		}), adminToken)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode,
		"missing session_id on resume_interrupt must return 400")
}

func TestINTERRUPT05_PlainMessagePathStillWorks(t *testing.T) {
	// Regression guard: adding the resume_interrupt branch must not break the
	// classic message path. Without an LLM key we can't observe a full SSE
	// response, but we can confirm the request reaches the engine without a
	// validation 400 and either returns 200 (streaming) or 5xx (LLM error
	// downstream — acceptable for this regression check).
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	agentBody := createAgentForTest(t, "tc-int-05-agent")
	var agent struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(agentBody, &agent))
	s := createSchemaForTest(t, map[string]any{
		"name":           "tc-int-05-schema",
		"chat_enabled":   true,
		"entry_agent_id": agent.ID,
	})

	resp := do(t, http.MethodPost, "/api/v1/schemas/"+s.Name+"/chat",
		mustJSON(map[string]any{"message": "hi"}), adminToken)
	defer func() { _ = resp.Body.Close() }()

	// Validation must NOT 400 — body shape is well-formed.
	assert.NotEqual(t, http.StatusBadRequest, resp.StatusCode,
		"plain message body must not be rejected as malformed")
}
