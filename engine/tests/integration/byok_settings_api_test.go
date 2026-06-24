//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBYOKSettingsAndMiddleware exercises the BYOK Settings API end-to-end
// against the shared suite engine: the admin writes byok.* settings via the
// HTTP API, the engine hot-reloads the chat-route BYOK middleware (no restart),
// and the middleware then enforces enable/allowlist/required-key gates on the
// chat endpoint.
//
// The middleware (internal/delivery/http/byok_middleware.go) is mounted BEFORE
// RequireScope(ScopeChat) and the chat handler, so the disabled / disallowed /
// missing-key gates fire on header presence alone — no real downstream model is
// needed for the negative assertions.
func TestBYOKSettingsAndMiddleware(t *testing.T) {
	requireSuite(t)

	// Reset the shared engine BYOK state at the end so it doesn't leak into
	// other tests in this package (they don't send BYOK headers, so the gate
	// is normally a pass-through, but reset to the strict default anyway).
	t.Cleanup(func() {
		putSetting(t, "byok.enabled", "false")
		putSetting(t, "byok.allowed_providers", "")
	})

	// A chat-enabled schema with an entry agent so an allowed-provider request
	// (step 5) flows past the BYOK gate into the real chat dispatcher. The
	// negative gates (steps 4, 6, 7) never reach the handler, so they don't
	// strictly need it, but a single shared schema keeps the test self-contained.
	agentBody := createAgentForTest(t, "byok-api-agent")
	var agent struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(agentBody, &agent))
	require.NotEmpty(t, agent.ID)

	schema := createSchemaForTest(t, map[string]any{
		"name":           "byok-api-schema",
		"chat_enabled":   true,
		"entry_agent_id": agent.ID,
	})
	chatPath := "/api/v1/schemas/" + schema.Name + "/chat"

	t.Run("SCC-01 settings list requires auth", func(t *testing.T) {
		resp := do(t, http.MethodGet, "/api/v1/settings", nil, "")
		_ = readBody(t, resp)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("admin enables BYOK and sets CSV allowlist", func(t *testing.T) {
		enResp := do(t, http.MethodPut, "/api/v1/settings/byok.enabled",
			mustJSON(map[string]any{"value": "true"}), adminToken)
		_ = readBody(t, enResp)
		assert.Equal(t, http.StatusOK, enResp.StatusCode)

		provResp := do(t, http.MethodPut, "/api/v1/settings/byok.allowed_providers",
			mustJSON(map[string]any{"value": "openai,anthropic"}), adminToken)
		_ = readBody(t, provResp)
		assert.Equal(t, http.StatusOK, provResp.StatusCode)
	})

	t.Run("settings list reflects the allowlist", func(t *testing.T) {
		resp := do(t, http.MethodGet, "/api/v1/settings", nil, adminToken)
		body := readBody(t, resp)
		require.Equal(t, http.StatusOK, resp.StatusCode, "body=%s", body)

		var settings []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		require.NoError(t, json.Unmarshal(body, &settings), "body=%s", body)

		var raw string
		var found bool
		for _, s := range settings {
			if s.Key == "byok.allowed_providers" {
				raw = s.Value
				found = true
				break
			}
		}
		require.True(t, found, "byok.allowed_providers must appear in settings list: %s", body)
		// The Settings API stores the value as a jsonb string; accept either a
		// JSON-array string ("[\"openai\",...]") or the CSV form we wrote.
		assert.Equal(t, []string{"openai", "anthropic"}, parseAllowlistValue(raw),
			"allowlist value should parse to exactly [openai anthropic]: %q", raw)
	})

	// D1 LINCHPIN (RED→GREEN). Before the D1 fix, the Settings-API write landed
	// as a jsonb STRING (CSV "openai,anthropic"); loadBYOKConfig only understood
	// a jsonb array, so the string form was ignored, the allowlist stayed empty,
	// an empty allowlist means allow-all, and "cohere" would have PASSED the
	// gate. With the fix loadBYOKConfig parses the CSV/JSON-array string form, so
	// the API-set allowlist is actually enforced and a disallowed provider is
	// rejected here. This subtest is therefore the end-to-end guard for the
	// string-form allowlist read through the live middleware.
	t.Run("D1 disallowed provider rejected via API-set allowlist", func(t *testing.T) {
		resp := doHeaders(t, http.MethodPost, chatPath, mustJSON(map[string]any{"message": "hi"}),
			map[string]string{
				"Authorization":   "Bearer " + adminToken,
				"X-BYOK-Provider": "cohere",
				"X-BYOK-API-Key":  "dummy",
			})
		body := readBody(t, resp)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, "body=%s", body)
		assert.Contains(t, string(body), "provider not allowed", "body=%s", body)
	})

	t.Run("allowed provider passes the BYOK gate", func(t *testing.T) {
		resp := doHeaders(t, http.MethodPost, chatPath, mustJSON(map[string]any{"message": "hi"}),
			map[string]string{
				"Authorization":   "Bearer " + adminToken,
				"X-BYOK-Provider": "openai",
				"X-BYOK-API-Key":  "dummy",
			})
		body := readBody(t, resp)
		// The gate let an allowed provider through; the downstream status may be
		// any 4xx/5xx from the fake/unreachable model. Assert only that the BYOK
		// gate did NOT block.
		assert.NotContains(t, string(body), "provider not allowed", "body=%s", body)
		assert.NotContains(t, string(body), "BYOK is disabled", "body=%s", body)
	})

	t.Run("SCC-03 missing API key is 400 not 500", func(t *testing.T) {
		resp := doHeaders(t, http.MethodPost, chatPath, mustJSON(map[string]any{"message": "hi"}),
			map[string]string{
				"Authorization":   "Bearer " + adminToken,
				"X-BYOK-Provider": "openai",
			})
		body := readBody(t, resp)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "body=%s", body)
		assert.Contains(t, string(body), "required for BYOK", "body=%s", body)
	})

	t.Run("disabling BYOK blocks BYOK requests", func(t *testing.T) {
		disResp := do(t, http.MethodPut, "/api/v1/settings/byok.enabled",
			mustJSON(map[string]any{"value": "false"}), adminToken)
		_ = readBody(t, disResp)
		assert.Equal(t, http.StatusOK, disResp.StatusCode)

		resp := doHeaders(t, http.MethodPost, chatPath, mustJSON(map[string]any{"message": "hi"}),
			map[string]string{
				"Authorization":   "Bearer " + adminToken,
				"X-BYOK-Provider": "openai",
				"X-BYOK-API-Key":  "dummy",
			})
		body := readBody(t, resp)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, "body=%s", body)
		assert.Contains(t, string(body), "BYOK is disabled", "body=%s", body)
	})
}

// putSetting writes a setting via the admin Settings API. Used by cleanup to
// reset the shared engine BYOK state.
func putSetting(t *testing.T, key, value string) {
	t.Helper()
	resp := do(t, http.MethodPut, "/api/v1/settings/"+key,
		mustJSON(map[string]any{"value": value}), adminToken)
	_ = readBody(t, resp)
}

// parseAllowlistValue accepts the Settings-API string form of the allowlist —
// either a JSON-array string ("[\"openai\",\"anthropic\"]") or a CSV
// ("openai, anthropic") — and returns the trimmed, non-empty providers.
func parseAllowlistValue(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}
	}
	if strings.HasPrefix(raw, "[") {
		var arr []string
		if err := json.Unmarshal([]byte(raw), &arr); err == nil {
			out := make([]string, 0, len(arr))
			for _, p := range arr {
				if p = strings.TrimSpace(p); p != "" {
					out = append(out, p)
				}
			}
			return out
		}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
