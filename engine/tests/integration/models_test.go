//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// modelCreateResp captures the minimal response fields needed by update/delete
// tests — the engine has varied on returning "id" vs "name" as the path key.
type modelCreateResp struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// createModelForTest POSTs a model; returns decoded id+name.
func createModelForTest(t *testing.T, name string) modelCreateResp {
	t.Helper()
	resp := do(t, http.MethodPost, "/api/v1/models",
		mustJSON(map[string]any{
			"name":       name,
			"type":       "openai_compatible",
			"kind":       "chat",
			"model_name": "test-model",
			"api_key":    "test-key",
			"base_url":   "https://api.test.com",
		}), adminToken)
	body := readBody(t, resp)
	assertStatusAny(t, resp, http.StatusOK, http.StatusCreated)

	var parsed modelCreateResp
	_ = json.Unmarshal(body, &parsed)
	if parsed.Name == "" {
		parsed.Name = name
	}
	return parsed
}

// modelPathKey picks the URL segment for /models/{...} — prefer id, fall back
// to name. The server routes use {name} today but some EE builds override.
func modelPathKey(m modelCreateResp) string {
	if m.Name != "" {
		return m.Name
	}
	return m.ID
}

// TC-MDL-01: POST /models → 201.
func TestMDL01_CreateModel(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	m := createModelForTest(t, "tc-mdl-01")
	assert.NotEmpty(t, m.Name, "name should come back on create")
}

// TC-MDL-02: GET /models lists the created model.
func TestMDL02_ListContainsCreated(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	_ = createModelForTest(t, "tc-mdl-02")

	resp := do(t, http.MethodGet, "/api/v1/models", nil, adminToken)
	body := readBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body), "tc-mdl-02", "list must contain created model: %s", body)
}

// TC-MDL-03: PUT /models/{name} updates temperature.
func TestMDL03_UpdateModel(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	m := createModelForTest(t, "tc-mdl-03")

	resp := do(t, http.MethodPut, "/api/v1/models/"+modelPathKey(m),
		mustJSON(map[string]any{
			"name":        m.Name,
			"type":        "openai_compatible",
			"kind":        "chat",
			"model_name":  "test-model",
			"api_key":     "test-key",
			"base_url":    "https://api.test.com",
			"temperature": 0.42,
		}), adminToken)
	_ = readBody(t, resp)
	assertStatusAny(t, resp, http.StatusOK, http.StatusNoContent)
}

// TC-MDL-04: DELETE /models/{name}.
func TestMDL04_DeleteModel(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	m := createModelForTest(t, "tc-mdl-04")

	resp := do(t, http.MethodDelete, "/api/v1/models/"+modelPathKey(m), nil, adminToken)
	_ = readBody(t, resp)
	assertStatusAny(t, resp, http.StatusOK, http.StatusNoContent)
}

// TC-MDL-05: Duplicate name → 409 or 422.
func TestMDL05_DuplicateName(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	_ = createModelForTest(t, "tc-mdl-05")

	resp := do(t, http.MethodPost, "/api/v1/models",
		mustJSON(map[string]any{
			"name":       "tc-mdl-05",
			"type":       "openai_compatible",
			"provider":   "openrouter",
			"model_name": "test-model",
			"api_key":    "test-key",
			"base_url":   "https://api.test.com",
		}), adminToken)
	_ = readBody(t, resp)
	assertStatusAny(t, resp, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusBadRequest)
}

// TC-MDL-06: Invalid type → 400/422.
func TestMDL06_InvalidType(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	resp := do(t, http.MethodPost, "/api/v1/models",
		mustJSON(map[string]any{
			"name":       "tc-mdl-06",
			"type":       "nonsense",
			"provider":   "openrouter",
			"model_name": "x",
			"api_key":    "k",
			"base_url":   "https://api.test.com",
		}), adminToken)
	_ = readBody(t, resp)
	assertStatusAny(t, resp, http.StatusBadRequest, http.StatusUnprocessableEntity)
}

// TC-MDL-07: extra_body round-trip. Operator-supplied openai_compatible
// passthrough (e.g. OpenRouter provider routing) must survive POST → GET → list.
// Closes the chirp-reported gap where there was no way to pin a sub-provider.
func TestMDL07_ExtraBodyRoundTrip(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	resp := do(t, http.MethodPost, "/api/v1/models",
		mustJSON(map[string]any{
			"name":       "tc-mdl-07",
			"type":       "openai_compatible",
			"kind":       "chat",
			"model_name": "z-ai/glm-4.7",
			"api_key":    "test-key",
			"base_url":   "https://openrouter.ai/api/v1",
			"extra_body": map[string]any{
				"provider": map[string]any{
					"order":           []string{"zai", "google"},
					"allow_fallbacks": false,
				},
			},
		}), adminToken)
	body := readBody(t, resp)
	assertStatusAny(t, resp, http.StatusOK, http.StatusCreated)

	var created struct {
		ExtraBody map[string]any `json:"extra_body"`
	}
	require.NoError(t, json.Unmarshal(body, &created), "decode create: %s", body)
	require.Contains(t, created.ExtraBody, "provider",
		"create response must echo extra_body: %s", body)

	listResp := do(t, http.MethodGet, "/api/v1/models", nil, adminToken)
	listBody := readBody(t, listResp)
	require.Equal(t, http.StatusOK, listResp.StatusCode)
	assert.Contains(t, string(listBody), `"allow_fallbacks":false`,
		"list response must surface extra_body for clients: %s", listBody)
	assert.Contains(t, string(listBody), `"order":["zai","google"]`)
}

// TC-MDL-08: PATCH with nil extra_body preserves existing value; empty map clears.
// Guards against PUT-wipe regression (BUG-MT-03 symmetric for the new field).
func TestMDL08_ExtraBodyPatchPreservesAndClears(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	createResp := do(t, http.MethodPost, "/api/v1/models",
		mustJSON(map[string]any{
			"name":       "tc-mdl-08",
			"type":       "openai_compatible",
			"kind":       "chat",
			"model_name": "z-ai/glm-4.7",
			"api_key":    "test-key",
			"base_url":   "https://openrouter.ai/api/v1",
			"extra_body": map[string]any{"provider": map[string]any{"order": []string{"zai"}}},
		}), adminToken)
	_ = readBody(t, createResp)
	assertStatusAny(t, createResp, http.StatusOK, http.StatusCreated)

	patchResp := do(t, http.MethodPatch, "/api/v1/models/tc-mdl-08",
		mustJSON(map[string]any{"model_name": "z-ai/glm-4.7-flash"}), adminToken)
	pBody := readBody(t, patchResp)
	require.Equal(t, http.StatusOK, patchResp.StatusCode, "patch: %s", pBody)

	listResp := do(t, http.MethodGet, "/api/v1/models", nil, adminToken)
	listBody := readBody(t, listResp)
	assert.Contains(t, string(listBody), `"order":["zai"]`,
		"PATCH without extra_body must NOT clear it: %s", listBody)

	clearResp := do(t, http.MethodPatch, "/api/v1/models/tc-mdl-08",
		mustJSON(map[string]any{"extra_body": map[string]any{}}), adminToken)
	_ = readBody(t, clearResp)
	require.Equal(t, http.StatusOK, clearResp.StatusCode)

	afterClear := do(t, http.MethodGet, "/api/v1/models", nil, adminToken)
	afterBody := readBody(t, afterClear)
	assert.NotContains(t, string(afterBody), `"order":["zai"]`,
		"explicit empty map must clear extra_body: %s", afterBody)
}
