package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capturingTransport records the body that actually reaches the wire.
type capturingTransport struct {
	body []byte
}

func (c *capturingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		c.body, _ = io.ReadAll(req.Body)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(nil)),
		Header:     make(http.Header),
	}, nil
}

func sendThroughUsageReporting(t *testing.T, body []byte, method, contentType string) []byte {
	t.Helper()
	cap := &capturingTransport{}
	tr := &usageReportingTransport{base: cap}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, "https://openrouter.ai/api/v1/chat/completions", rdr)
	require.NoError(t, err)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := tr.RoundTrip(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return cap.body
}

func TestUsageReporting_InjectsIncludeUsage(t *testing.T) {
	body := []byte(`{"model":"qwen/qwen3.7-plus","messages":[{"role":"user","content":"hi"}]}`)
	out := sendThroughUsageReporting(t, body, http.MethodPost, "application/json")

	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &top))
	require.Contains(t, top, "usage")
	assert.JSONEq(t, `{"include":true}`, string(top["usage"]))
}

func TestUsageReporting_PreservesMessagesBytesExactly(t *testing.T) {
	// The messages array must survive byte-for-byte so the explicit-cache prefix
	// stays stable — the transport only adds a top-level key.
	msgs := `[{"role":"system","content":[{"type":"text","text":"S","cache_control":{"type":"ephemeral"}}]},{"role":"user","content":"hi"}]`
	body := []byte(`{"model":"m","messages":` + msgs + `}`)
	out := sendThroughUsageReporting(t, body, http.MethodPost, "application/json")

	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &top))
	assert.JSONEq(t, msgs, string(top["messages"]))
	assert.Equal(t, msgs, string(top["messages"]), "messages raw bytes must be untouched")
}

func TestUsageReporting_IdempotentWhenUsagePresent(t *testing.T) {
	// Operator-supplied usage (e.g. via extra_body) must win — not be overwritten.
	body := []byte(`{"model":"m","messages":[],"usage":{"include":false}}`)
	out := sendThroughUsageReporting(t, body, http.MethodPost, "application/json")
	assert.Equal(t, string(body), string(out), "existing usage must be preserved unchanged")
}

func TestUsageReporting_PassthroughNonJSONBody(t *testing.T) {
	body := []byte(`not json at all`)
	out := sendThroughUsageReporting(t, body, http.MethodPost, "application/json")
	assert.Equal(t, string(body), string(out), "non-JSON body forwarded unchanged")
}

func TestUsageReporting_PassthroughNonJSONContentType(t *testing.T) {
	body := []byte(`{"messages":[]}`)
	out := sendThroughUsageReporting(t, body, http.MethodPost, "text/plain")
	assert.Equal(t, string(body), string(out), "non-JSON content-type untouched")
}

func TestUsageReporting_PassthroughNonPOST(t *testing.T) {
	body := []byte(`{"messages":[]}`)
	out := sendThroughUsageReporting(t, body, http.MethodGet, "application/json")
	assert.Equal(t, string(body), string(out), "non-POST untouched")
}

func TestUsageReporting_PassthroughNilBody(t *testing.T) {
	cap := &capturingTransport{}
	tr := &usageReportingTransport{base: cap}
	req, err := http.NewRequest(http.MethodPost, "https://openrouter.ai/", nil)
	require.NoError(t, err)
	resp, err := tr.RoundTrip(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Nil(t, cap.body)
}

func TestInjectIncludeUsage_NonObjectForwardedUnchanged(t *testing.T) {
	raw := []byte(`["not","an","object"]`)
	assert.Equal(t, string(raw), string(injectIncludeUsage(context.Background(), raw)))
}

func TestIsOpenRouterBaseURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://openrouter.ai/api/v1", true},
		{"https://OpenRouter.AI/api/v1", true},
		{"http://openrouter.ai", true},
		{"https://api.openai.com/v1", false},
		{"https://dashscope.aliyuncs.com/compatible-mode/v1", false},
		{"https://localhost:8000/v1", false},
		{"", false},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, isOpenRouterBaseURL(c.url), "url=%q", c.url)
	}
}
