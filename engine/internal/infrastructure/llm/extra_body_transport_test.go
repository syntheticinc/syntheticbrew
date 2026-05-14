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

// captureTransport records the request body forwarded by the wrapped
// transport. Used to assert what the downstream actually receives.
type captureTransport struct {
	gotBody []byte
}

func (c *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		_ = req.Body.Close()
		c.gotBody = b
	}
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(`{}`)),
		Header:     make(http.Header),
	}, nil
}

func newJSONRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "https://example.test/v1/chat/completions",
		bytes.NewBufferString(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestExtraBodyTransport_MergesProviderRoutingField(t *testing.T) {
	cap := &captureTransport{}
	rt := &extraBodyTransport{
		base: cap,
		extra: map[string]any{
			"provider": map[string]any{
				"order":           []string{"zai", "google"},
				"allow_fallbacks": false,
			},
		},
	}

	req := newJSONRequest(t, `{"model":"glm-4.7","messages":[{"role":"user","content":"hi"}]}`)
	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	_ = resp.Body.Close()

	var got map[string]any
	require.NoError(t, json.Unmarshal(cap.gotBody, &got))
	assert.Equal(t, "glm-4.7", got["model"], "engine-set model must survive merge")
	require.Contains(t, got, "provider", "extra_body provider field must be merged in")
	prov, ok := got["provider"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, false, prov["allow_fallbacks"])
}

func TestExtraBodyTransport_ReservedKeysCannotOverwriteEngineFields(t *testing.T) {
	cap := &captureTransport{}
	rt := &extraBodyTransport{
		base: cap,
		extra: map[string]any{
			"model":    "spoofed-model",
			"messages": []any{map[string]any{"role": "system", "content": "hijack"}},
			"tools":    []any{},
			"stream":   true,
			"provider": map[string]any{"order": []string{"zai"}},
		},
	}

	original := `{"model":"glm-4.7","messages":[{"role":"user","content":"hi"}],"stream":false}`
	req := newJSONRequest(t, original)
	_, err := rt.RoundTrip(req)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(cap.gotBody, &got))
	assert.Equal(t, "glm-4.7", got["model"], "model must NOT be overwritten by extra_body")
	assert.Equal(t, false, got["stream"], "stream must NOT be overwritten by extra_body")
	msgs, _ := got["messages"].([]any)
	assert.Len(t, msgs, 1, "messages must NOT be overwritten by extra_body")
	assert.Contains(t, got, "provider", "non-reserved key must still pass through")
}

func TestExtraBodyTransport_EmptyExtraBypasses(t *testing.T) {
	cap := &captureTransport{}
	rt := &extraBodyTransport{base: cap, extra: nil}

	original := `{"model":"glm-4.7"}`
	req := newJSONRequest(t, original)
	_, err := rt.RoundTrip(req)
	require.NoError(t, err)
	assert.Equal(t, original, string(cap.gotBody),
		"empty extra must forward body byte-for-byte unchanged")
}

func TestExtraBodyTransport_NonJSONBodyPassesThrough(t *testing.T) {
	cap := &captureTransport{}
	rt := &extraBodyTransport{base: cap, extra: map[string]any{"provider": "x"}}

	req, err := http.NewRequest(http.MethodPost, "https://example.test/v1/embeddings",
		bytes.NewBufferString("raw-binary"))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/octet-stream")

	_, err = rt.RoundTrip(req)
	require.NoError(t, err)
	assert.Equal(t, "raw-binary", string(cap.gotBody),
		"non-JSON body must not be touched")
}

func TestExtraBodyTransport_NonPOSTPassesThrough(t *testing.T) {
	cap := &captureTransport{}
	rt := &extraBodyTransport{base: cap, extra: map[string]any{"provider": "x"}}

	req, err := http.NewRequest(http.MethodGet, "https://example.test/v1/models", nil)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	_, err = rt.RoundTrip(req)
	require.NoError(t, err)
	assert.Nil(t, cap.gotBody, "GET with nil body forwards unchanged")
}

// TestExtraBodyTransport_AgainstRealServer is an end-to-end probe — wraps a
// real httptest server so we cover the GetBody retry-path that httputil might
// invoke. Verifies the merged body actually lands on the wire.
func TestExtraBodyTransport_AgainstRealServer(t *testing.T) {
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	client := &http.Client{Transport: &extraBodyTransport{
		base:  http.DefaultTransport,
		extra: map[string]any{"provider": map[string]any{"order": []string{"zai"}}},
	}}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions",
		bytes.NewBufferString(`{"model":"glm-4.7","messages":[]}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()

	var got map[string]any
	require.NoError(t, json.Unmarshal(captured, &got))
	assert.Contains(t, got, "provider", "merged body must arrive at upstream server")
	assert.Equal(t, "glm-4.7", got["model"])
}
