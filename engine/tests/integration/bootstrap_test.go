//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBootstrap_FullStack имитирует полный Ops-сценарий: с пустой БД через REST
// API создаётся рабочая агентская система (модели, MCP, агенты, KB, схема,
// связи), затем проверяется финальное состояние и идемпотентность повторного
// вызова.
//
// Тест намеренно единый — Ops-скрипт должен проходить как единое целое без
// хранения стейта между шагами.
func TestBootstrap_FullStack(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	// ── Шаг 1: создание chat-модели ──────────────────────────────────────────

	chatModelResp := do(t, http.MethodPost, "/api/v1/models",
		mustJSON(map[string]any{
			"name":       "test-chat-model",
			"type":       "openai_compatible",
			"kind":       "chat",
			"base_url":   "https://openrouter.ai/api/v1",
			"model_name": "openai/gpt-4o-mini",
			"api_key":    "sk-test-key",
		}), adminToken)
	chatModelBody := readBody(t, chatModelResp)
	assertStatusAny(t, chatModelResp, http.StatusOK, http.StatusCreated)

	var chatModel struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(chatModelBody, &chatModel),
		"step 1: parse chat model response: %s", chatModelBody)
	assert.NotEmpty(t, chatModel.ID, "step 1: chat model id must be non-empty")

	// ── Шаг 2: создание embedding-модели ─────────────────────────────────────

	embedModelResp := do(t, http.MethodPost, "/api/v1/models",
		mustJSON(map[string]any{
			"name":          "test-embed-model",
			"type":          "openai_compatible",
			"kind":          "embedding",
			"base_url":      "https://api.openai.com/v1",
			"model_name":    "text-embedding-3-small",
			"api_key":       "sk-test-embed",
			"embedding_dim": 1536,
		}), adminToken)
	embedModelBody := readBody(t, embedModelResp)
	assertStatusAny(t, embedModelResp, http.StatusOK, http.StatusCreated)

	var embedModel struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(embedModelBody, &embedModel),
		"step 2: parse embed model response: %s", embedModelBody)
	assert.NotEmpty(t, embedModel.ID, "step 2: embed model id must be non-empty")

	// ── Шаг 3: создание MCP-сервера ──────────────────────────────────────────

	mcpResp := do(t, http.MethodPost, "/api/v1/mcp-servers",
		mustJSON(map[string]any{
			"name":         "test-mcp",
			"type":         "http",
			"url":          "https://example.com/mcp",
			"auth_type":    "api_key",
			"auth_key_env": "TAVILY_API_KEY",
		}), adminToken)
	_ = readBody(t, mcpResp)
	assertStatusAny(t, mcpResp, http.StatusOK, http.StatusCreated)

	// ── Шаг 4: создание router-агента ────────────────────────────────────────

	routerResp := do(t, http.MethodPost, "/api/v1/agents",
		mustJSON(map[string]any{
			"name":          "test-router",
			"model":         "test-chat-model",
			"system_prompt": "You route requests.",
			"lifecycle":     "persistent",
			"tools":         []string{"spawn_agent", "show_structured_output"},
			"can_spawn":     []string{"test-worker"},
			"mcp_servers":   []string{"test-mcp"},
		}), adminToken)
	routerBody := readBody(t, routerResp)
	assertStatusAny(t, routerResp, http.StatusOK, http.StatusCreated)

	var routerAgent struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(routerBody, &routerAgent),
		"step 4: parse router agent response: %s", routerBody)
	assert.NotEmpty(t, routerAgent.ID, "step 4: router agent id must be non-empty")
	assert.Equal(t, "test-router", routerAgent.Name, "step 4: router agent name mismatch")

	// ── Шаг 5: создание worker-агента ────────────────────────────────────────

	workerResp := do(t, http.MethodPost, "/api/v1/agents",
		mustJSON(map[string]any{
			"name":          "test-worker",
			"model":         "test-chat-model",
			"system_prompt": "You handle work.",
			"lifecycle":     "spawn",
			"tools":         []string{"show_structured_output"},
		}), adminToken)
	workerBody := readBody(t, workerResp)
	assertStatusAny(t, workerResp, http.StatusOK, http.StatusCreated)

	var workerAgent struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(workerBody, &workerAgent),
		"step 5: parse worker agent response: %s", workerBody)
	assert.NotEmpty(t, workerAgent.ID, "step 5: worker agent id must be non-empty")

	// ── Шаг 6: добавление memory capability к router ──────────────────────────

	capResp := do(t, http.MethodPost, "/api/v1/agents/test-router/capabilities",
		mustJSON(map[string]any{
			"type":    "memory",
			"enabled": true,
		}), adminToken)
	capBody := readBody(t, capResp)
	assertStatusAny(t, capResp, http.StatusOK, http.StatusCreated)

	var capability struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Enabled bool   `json:"enabled"`
	}
	require.NoError(t, json.Unmarshal(capBody, &capability),
		"step 6: parse capability response: %s", capBody)
	assert.NotEmpty(t, capability.ID, "step 6: capability id must be non-empty")
	assert.Equal(t, "memory", capability.Type, "step 6: capability type mismatch")

	// ── Шаг 7: создание knowledge base ───────────────────────────────────────

	kbResp := do(t, http.MethodPost, "/api/v1/knowledge-bases",
		mustJSON(map[string]any{
			"name":               "test-kb",
			"description":        "Test KB",
			"embedding_model_id": embedModel.ID,
		}), adminToken)
	kbBody := readBody(t, kbResp)
	assertStatusAny(t, kbResp, http.StatusOK, http.StatusCreated)

	var kb struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(kbBody, &kb),
		"step 7: parse KB response: %s", kbBody)
	require.NotEmpty(t, kb.ID, "step 7: KB id must be non-empty")

	// ── Шаг 8: связать KB с router-агентом ───────────────────────────────────

	linkResp := do(t, http.MethodPost,
		"/api/v1/knowledge-bases/"+kb.Name+"/agents/test-router", nil, adminToken)
	_ = readBody(t, linkResp)
	assertStatusAny(t, linkResp,
		http.StatusOK, http.StatusCreated, http.StatusNoContent)

	// ── Шаг 9: создание схемы ─────────────────────────────────────────────────

	chatEnabled := true
	schemaResp := do(t, http.MethodPost, "/api/v1/schemas",
		mustJSON(map[string]any{
			"name":         "test-schema",
			"description":  "Test",
			"chat_enabled": chatEnabled,
		}), adminToken)
	schemaBody := readBody(t, schemaResp)
	assertStatusAny(t, schemaResp, http.StatusOK, http.StatusCreated)

	var schema struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		ChatEnabled bool   `json:"chat_enabled"`
	}
	require.NoError(t, json.Unmarshal(schemaBody, &schema),
		"step 9: parse schema response: %s", schemaBody)
	require.NotEmpty(t, schema.ID, "step 9: schema id must be non-empty")
	assert.Equal(t, "test-schema", schema.Name, "step 9: schema name mismatch")

	// ── Шаг 10: создание agent-relation (router → worker) ────────────────────

	relResp := do(t, http.MethodPost,
		"/api/v1/schemas/"+schema.Name+"/agent-relations",
		mustJSON(map[string]any{
			"source": "test-router",
			"target": "test-worker",
		}), adminToken)
	relBody := readBody(t, relResp)
	assertStatusAny(t, relResp, http.StatusOK, http.StatusCreated)

	var relation struct {
		ID     string `json:"id"`
		Source string `json:"source"`
		Target string `json:"target"`
	}
	require.NoError(t, json.Unmarshal(relBody, &relation),
		"step 10: parse relation response: %s", relBody)
	assert.NotEmpty(t, relation.ID, "step 10: relation id must be non-empty")

	// ── Шаг 11: GET schema — проверить финальное состояние ───────────────────

	getSchemaResp := do(t, http.MethodGet, "/api/v1/schemas/"+schema.Name, nil, adminToken)
	getSchemaBody := readBody(t, getSchemaResp)
	require.Equal(t, http.StatusOK, getSchemaResp.StatusCode,
		"step 11: GET schema must return 200: %s", getSchemaBody)

	var schemaDetail struct {
		ID          string `json:"id"`
		ChatEnabled bool   `json:"chat_enabled"`
	}
	require.NoError(t, json.Unmarshal(getSchemaBody, &schemaDetail),
		"step 11: parse schema detail: %s", getSchemaBody)
	assert.Equal(t, schema.ID, schemaDetail.ID,
		"step 11: schema id must match")
	assert.True(t, schemaDetail.ChatEnabled,
		"step 11: chat_enabled must be true: %s", getSchemaBody)

	// ── Шаг 12: GET schema agents — ровно 2 элемента ─────────────────────────

	agentsResp := do(t, http.MethodGet,
		"/api/v1/schemas/"+schema.Name+"/agents", nil, adminToken)
	agentsBody := readBody(t, agentsResp)
	require.Equal(t, http.StatusOK, agentsResp.StatusCode,
		"step 12: GET schema agents must return 200: %s", agentsBody)

	var agentNames []string
	require.NoError(t, json.Unmarshal(agentsBody, &agentNames),
		"step 12: parse agents list: %s", agentsBody)
	assert.Len(t, agentNames, 2,
		"step 12: schema must contain exactly 2 agents (router + worker): %v", agentNames)

	hasRouter := false
	hasWorker := false
	for _, name := range agentNames {
		if name == "test-router" {
			hasRouter = true
		}
		if name == "test-worker" {
			hasWorker = true
		}
	}
	assert.True(t, hasRouter, "step 12: test-router must be in schema agents: %v", agentNames)
	assert.True(t, hasWorker, "step 12: test-worker must be in schema agents: %v", agentNames)

	// ── Шаг 13: GET agent-relations — ровно 1 элемент ────────────────────────

	relsResp := do(t, http.MethodGet,
		"/api/v1/schemas/"+schema.Name+"/agent-relations", nil, adminToken)
	relsBody := readBody(t, relsResp)
	require.Equal(t, http.StatusOK, relsResp.StatusCode,
		"step 13: GET agent-relations must return 200: %s", relsBody)

	var relations []struct {
		ID     string `json:"id"`
		Source string `json:"source"`
		Target string `json:"target"`
	}
	require.NoError(t, json.Unmarshal(relsBody, &relations),
		"step 13: parse relations list: %s", relsBody)
	require.Len(t, relations, 1,
		"step 13: schema must have exactly 1 agent relation: %v", relations)
	assert.Equal(t, "test-router", relations[0].Source,
		"step 13: relation source must be test-router (names resolved by adapter)")
	assert.Equal(t, "test-worker", relations[0].Target,
		"step 13: relation target must be test-worker (names resolved by adapter)")

	// ── Шаг 14: GET capabilities router — ровно 1 memory capability ──────────

	capsResp := do(t, http.MethodGet,
		"/api/v1/agents/test-router/capabilities", nil, adminToken)
	capsBody := readBody(t, capsResp)
	require.Equal(t, http.StatusOK, capsResp.StatusCode,
		"step 14: GET capabilities must return 200: %s", capsBody)

	var capabilities []struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Enabled bool   `json:"enabled"`
	}
	require.NoError(t, json.Unmarshal(capsBody, &capabilities),
		"step 14: parse capabilities list: %s", capsBody)
	require.Len(t, capabilities, 1,
		"step 14: router must have exactly 1 capability: %v", capabilities)
	assert.Equal(t, "memory", capabilities[0].Type,
		"step 14: capability type must be memory")
	assert.True(t, capabilities[0].Enabled,
		"step 14: memory capability must be enabled")

	// ── Шаг 15: идемпотентность — повторный POST /models → 409/422/400 ───────

	dupModelResp := do(t, http.MethodPost, "/api/v1/models",
		mustJSON(map[string]any{
			"name":       "test-chat-model",
			"type":       "openai_compatible",
			"kind":       "chat",
			"base_url":   "https://openrouter.ai/api/v1",
			"model_name": "openai/gpt-4o-mini",
			"api_key":    "sk-test-key",
		}), adminToken)
	dupModelBody := readBody(t, dupModelResp)
	assertStatusAny(t, dupModelResp,
		http.StatusConflict, http.StatusUnprocessableEntity, http.StatusBadRequest)
	t.Logf("step 15: duplicate model → %d: %s", dupModelResp.StatusCode, dupModelBody)

	// ── Шаг 16: PATCH model — обновляемое поле ────────────────────────────────

	patchResp := do(t, http.MethodPatch, "/api/v1/models/test-chat-model",
		mustJSON(map[string]any{
			"model_name": "openai/gpt-4o-mini-updated",
		}), adminToken)
	patchBody := readBody(t, patchResp)
	assertStatusAny(t, patchResp, http.StatusOK, http.StatusNoContent)
	t.Logf("step 16: PATCH model → %d: %s", patchResp.StatusCode, patchBody)

	// ── Шаг 17: GET /models — модель не задублировалась ──────────────────────

	listModelsResp := do(t, http.MethodGet, "/api/v1/models", nil, adminToken)
	listModelsBody := readBody(t, listModelsResp)
	require.Equal(t, http.StatusOK, listModelsResp.StatusCode,
		"step 17: GET models must return 200: %s", listModelsBody)

	var allModels []struct {
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(listModelsBody, &allModels),
		"step 17: parse models list: %s", listModelsBody)

	chatModelCount := 0
	for _, m := range allModels {
		if m.Name == "test-chat-model" {
			chatModelCount++
		}
	}
	assert.Equal(t, 1, chatModelCount,
		"step 17: test-chat-model must not be duplicated, got count=%d", chatModelCount)

	// ── Шаг 18: повторный POST /agents → 409/422/400 ─────────────────────────

	dupAgentResp := do(t, http.MethodPost, "/api/v1/agents",
		mustJSON(map[string]any{
			"name":          "test-router",
			"system_prompt": "Duplicate attempt",
		}), adminToken)
	dupAgentBody := readBody(t, dupAgentResp)
	assertStatusAny(t, dupAgentResp,
		http.StatusConflict, http.StatusUnprocessableEntity, http.StatusBadRequest)
	t.Logf("step 18: duplicate agent → %d: %s", dupAgentResp.StatusCode, dupAgentBody)

	// ── Шаг 19: повторный POST agent-relation → 409 ──────────────────────────

	dupRelResp := do(t, http.MethodPost,
		"/api/v1/schemas/"+schema.Name+"/agent-relations",
		mustJSON(map[string]any{
			"source": "test-router",
			"target": "test-worker",
		}), adminToken)
	dupRelBody := readBody(t, dupRelResp)
	assertStatusAny(t, dupRelResp,
		http.StatusConflict, http.StatusUnprocessableEntity, http.StatusBadRequest)
	t.Logf("step 19: duplicate relation → %d: %s", dupRelResp.StatusCode, dupRelBody)

	// Количество relation не должно расти.
	relsRecheckResp := do(t, http.MethodGet,
		"/api/v1/schemas/"+schema.Name+"/agent-relations", nil, adminToken)
	relsRecheckBody := readBody(t, relsRecheckResp)
	require.Equal(t, http.StatusOK, relsRecheckResp.StatusCode,
		"step 19 recheck: GET relations must return 200: %s", relsRecheckBody)

	var recheckRelations []struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(relsRecheckBody, &recheckRelations),
		"step 19 recheck: parse relations: %s", relsRecheckBody)
	assert.Len(t, recheckRelations, 1,
		"step 19: rejected duplicate must leave exactly 1 relation, got %d", len(recheckRelations))
}
