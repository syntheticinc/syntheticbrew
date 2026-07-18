package llm

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// postChatThroughChain sends a chat-completion body through the production transport
// chain built by newOpenAICompatTransport. Detection is driven by m.BaseURL (build
// time); the request itself targets the local capture server (run time) — the two are
// decoupled at RoundTrip, so an OpenRouter base URL can be exercised against a fake.
func postChatThroughChain(t *testing.T, m models.LLMProviderModel) []byte {
	t.Helper()
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c","object":"chat.completion","created":1,"choices":[],"usage":{}}`))
	}))
	defer srv.Close()

	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	req, err := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := newOpenAICompatTransport(m, http.DefaultTransport).RoundTrip(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.NotEmpty(t, captured, "fake provider must have received a request")
	return captured
}

// TestUsageReporting_WiredForOpenRouter — the L2 wire proof: a model whose base URL is
// OpenRouter gets usage:{include:true} on the wire via CreateClientFromDBModel's chain.
func TestUsageReporting_WiredForOpenRouter(t *testing.T) {
	captured := postChatThroughChain(t, models.LLMProviderModel{
		Type:            "openai_compatible",
		BaseURL:         "https://openrouter.ai/api/v1",
		ModelName:       "qwen/qwen3.7-plus",
		APIKeyEncrypted: "k",
	})
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(captured, &top))
	require.Contains(t, top, "usage", "OpenRouter request must carry the usage flag")
	assert.JSONEq(t, `{"include":true}`, string(top["usage"]))
}

// TestUsageReporting_NotWiredForNonOpenRouter — a non-OpenRouter compat gateway must be
// byte-untouched (no unknown usage key that a strict gateway could 400 on).
func TestUsageReporting_NotWiredForNonOpenRouter(t *testing.T) {
	captured := postChatThroughChain(t, models.LLMProviderModel{
		Type:            "openai_compatible",
		BaseURL:         "https://dashscope.aliyuncs.com/compatible-mode/v1",
		ModelName:       "qwen-plus",
		APIKeyEncrypted: "k",
	})
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(captured, &top))
	assert.NotContains(t, top, "usage", "non-OpenRouter request must not carry the usage flag")
}

// TestUsageReporting_OperatorExtraBodyUsageWins — operator usage via extra_body takes
// precedence: extra body runs outer and injects it, usage reporting then no-ops.
func TestUsageReporting_OperatorExtraBodyUsageWins(t *testing.T) {
	m := models.LLMProviderModel{
		Type:            "openai_compatible",
		BaseURL:         "https://openrouter.ai/api/v1",
		ModelName:       "qwen/qwen3.7-plus",
		APIKeyEncrypted: "k",
	}
	m.SetConfig(models.ModelConfig{ExtraBody: map[string]any{"usage": map[string]any{"include": false}}})

	captured := postChatThroughChain(t, m)
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(captured, &top))
	require.Contains(t, top, "usage")
	assert.JSONEq(t, `{"include":false}`, string(top["usage"]),
		"operator-supplied usage must win over the engine default")
}
