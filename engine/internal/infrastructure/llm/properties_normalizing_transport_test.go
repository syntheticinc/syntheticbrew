package llm

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPropertiesNormalizingTransport_AddsEmptyPropertiesOnBareObjectSchema(t *testing.T) {
	cap := &captureTransport{}
	rt := &propertiesNormalizingTransport{base: cap}

	// Input: tool with parameters of type=object but no properties.
	req := newJSONRequest(t, `{"model":"gpt-4o-mini","messages":[],"tools":[{"type":"function","function":{"name":"device_list_bare","parameters":{"type":"object"}}}]}`)
	_, err := rt.RoundTrip(req)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(cap.gotBody, &got))
	tools := got["tools"].([]any)
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	params := fn["parameters"].(map[string]any)
	require.Contains(t, params, "properties", "normalizer must add properties on bare type=object")
	props, ok := params["properties"].(map[string]any)
	require.True(t, ok, "properties must be a JSON object (map)")
	assert.Empty(t, props, "added properties must be empty {}")
}

func TestPropertiesNormalizingTransport_LeavesExistingPropertiesAlone(t *testing.T) {
	cap := &captureTransport{}
	rt := &propertiesNormalizingTransport{base: cap}

	original := `{"model":"gpt-4o-mini","messages":[],"tools":[{"type":"function","function":{"name":"echo_message","parameters":{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}}}]}`
	req := newJSONRequest(t, original)
	_, err := rt.RoundTrip(req)
	require.NoError(t, err)

	// Body unchanged byte-for-byte semantics: re-parse and assert structure.
	var got map[string]any
	require.NoError(t, json.Unmarshal(cap.gotBody, &got))
	tools := got["tools"].([]any)
	params := tools[0].(map[string]any)["function"].(map[string]any)["parameters"].(map[string]any)
	props := params["properties"].(map[string]any)
	assert.Contains(t, props, "message", "existing properties must be preserved")
	assert.Contains(t, params["required"], "message")
}

func TestPropertiesNormalizingTransport_LeavesEmptyPropertiesMapAlone(t *testing.T) {
	cap := &captureTransport{}
	rt := &propertiesNormalizingTransport{base: cap}

	// Explicit empty properties map — OpenAI-compliant, must not be touched.
	original := `{"tools":[{"type":"function","function":{"name":"x","parameters":{"type":"object","properties":{}}}}]}`
	req := newJSONRequest(t, original)
	_, err := rt.RoundTrip(req)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(cap.gotBody, &got))
	tools := got["tools"].([]any)
	params := tools[0].(map[string]any)["function"].(map[string]any)["parameters"].(map[string]any)
	props := params["properties"].(map[string]any)
	assert.Empty(t, props)
}

func TestPropertiesNormalizingTransport_LeavesNonObjectSchemasAlone(t *testing.T) {
	cap := &captureTransport{}
	rt := &propertiesNormalizingTransport{base: cap}

	original := `{"tools":[{"type":"function","function":{"name":"int_arg","parameters":{"type":"integer"}}}]}`
	req := newJSONRequest(t, original)
	_, err := rt.RoundTrip(req)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(cap.gotBody, &got))
	params := got["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)["parameters"].(map[string]any)
	assert.NotContains(t, params, "properties", "non-object schemas must not gain a properties key")
}

func TestPropertiesNormalizingTransport_NoToolsKeyBypasses(t *testing.T) {
	cap := &captureTransport{}
	rt := &propertiesNormalizingTransport{base: cap}

	original := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`
	req := newJSONRequest(t, original)
	_, err := rt.RoundTrip(req)
	require.NoError(t, err)
	assert.Equal(t, original, string(cap.gotBody),
		"missing tools key must forward body byte-for-byte unchanged")
}

func TestPropertiesNormalizingTransport_EmptyToolsArrayBypasses(t *testing.T) {
	cap := &captureTransport{}
	rt := &propertiesNormalizingTransport{base: cap}

	original := `{"model":"gpt-4o-mini","messages":[],"tools":[]}`
	req := newJSONRequest(t, original)
	_, err := rt.RoundTrip(req)
	require.NoError(t, err)
	assert.Equal(t, original, string(cap.gotBody),
		"empty tools array must forward body byte-for-byte unchanged")
}

func TestPropertiesNormalizingTransport_MixedToolsOnlyMutatesBareOnes(t *testing.T) {
	cap := &captureTransport{}
	rt := &propertiesNormalizingTransport{base: cap}

	original := `{"tools":[
		{"type":"function","function":{"name":"bare","parameters":{"type":"object"}}},
		{"type":"function","function":{"name":"with_arg","parameters":{"type":"object","properties":{"x":{"type":"string"}}}}},
		{"type":"function","function":{"name":"int","parameters":{"type":"integer"}}}
	]}`
	req := newJSONRequest(t, original)
	_, err := rt.RoundTrip(req)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(cap.gotBody, &got))
	tools := got["tools"].([]any)

	bareParams := tools[0].(map[string]any)["function"].(map[string]any)["parameters"].(map[string]any)
	require.Contains(t, bareParams, "properties")
	assert.Empty(t, bareParams["properties"])

	withArgParams := tools[1].(map[string]any)["function"].(map[string]any)["parameters"].(map[string]any)
	assert.Contains(t, withArgParams["properties"].(map[string]any), "x")

	intParams := tools[2].(map[string]any)["function"].(map[string]any)["parameters"].(map[string]any)
	assert.NotContains(t, intParams, "properties")
}

func TestPropertiesNormalizingTransport_NonJSONBodyPassesThrough(t *testing.T) {
	cap := &captureTransport{}
	rt := &propertiesNormalizingTransport{base: cap}

	req, err := http.NewRequest(http.MethodPost, "https://example.test/v1/embeddings",
		bytes.NewBufferString("raw-binary"))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/octet-stream")

	_, err = rt.RoundTrip(req)
	require.NoError(t, err)
	assert.Equal(t, "raw-binary", string(cap.gotBody),
		"non-JSON content type must not be touched")
}

func TestPropertiesNormalizingTransport_NonPOSTPassesThrough(t *testing.T) {
	cap := &captureTransport{}
	rt := &propertiesNormalizingTransport{base: cap}

	req, err := http.NewRequest(http.MethodGet, "https://example.test/v1/models", nil)
	require.NoError(t, err)
	_, err = rt.RoundTrip(req)
	require.NoError(t, err)
	assert.Nil(t, cap.gotBody, "GET with nil body must forward unchanged")
}

// TestPropertiesNormalizingTransport_AgainstRealServer wraps a real httptest
// server to confirm the body the upstream receives includes the normalized
// properties field (covers GetBody retry semantics).
func TestPropertiesNormalizingTransport_AgainstRealServer(t *testing.T) {
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	client := &http.Client{Transport: &propertiesNormalizingTransport{base: http.DefaultTransport}}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions",
		strings.NewReader(`{"tools":[{"type":"function","function":{"name":"bare","parameters":{"type":"object"}}}]}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()

	var got map[string]any
	require.NoError(t, json.Unmarshal(captured, &got))
	params := got["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)["parameters"].(map[string]any)
	require.Contains(t, params, "properties",
		"merged body must arrive at upstream with normalized properties field")
}
