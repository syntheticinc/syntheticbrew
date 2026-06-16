package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// usageReportingTransport injects the OpenRouter body flag `usage: {"include": true}`
// into outgoing chat-completion requests. OpenRouter omits
// `usage.prompt_tokens_details.cached_tokens` from its response unless this flag is
// set, so without it the engine reports cached=0 even when prompt caching is active.
// It is attached only for OpenRouter base URLs (see isOpenRouterBaseURL): the flag is
// an OpenRouter extension and real OpenAI / strict gateways reject unknown body keys.
//
// Only top-level keys are touched — the request's `messages` are forwarded as raw
// bytes, so the byte-stable prefix that explicit-cache providers depend on is
// preserved. If `usage` is already present (e.g. set by an operator via extra_body)
// it is left untouched, so operator configuration wins.
type usageReportingTransport struct {
	base http.RoundTripper
}

var includeUsage = json.RawMessage(`{"include":true}`)

func (t *usageReportingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body == nil || req.Method != http.MethodPost {
		return t.base.RoundTrip(req)
	}
	ct := req.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		return t.base.RoundTrip(req)
	}

	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("usage_reporting: read request body: %w", err)
	}
	if cerr := req.Body.Close(); cerr != nil {
		slog.WarnContext(req.Context(), "usage_reporting: close original body", "error", cerr)
	}

	out := injectIncludeUsage(req.Context(), raw)
	req.Body = io.NopCloser(bytes.NewReader(out))
	req.ContentLength = int64(len(out))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(out)), nil
	}
	return t.base.RoundTrip(req)
}

// injectIncludeUsage adds `usage: {"include": true}` to a serialized chat-completion
// body unless it is already present. Any non-object body or marshal failure forwards
// the original bytes unchanged — usage reporting is best-effort, never request-breaking.
func injectIncludeUsage(ctx context.Context, raw []byte) []byte {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return raw
	}
	if _, present := top["usage"]; present {
		return raw
	}
	top["usage"] = includeUsage
	merged, err := json.Marshal(top)
	if err != nil {
		slog.ErrorContext(ctx, "usage_reporting: marshal failed, forwarding original", "error", err)
		return raw
	}
	return merged
}

// isOpenRouterBaseURL reports whether a model's base URL points at OpenRouter, the
// one provider that gates detailed usage accounting (including cached_tokens) behind
// the `usage: {"include": true}` request flag.
func isOpenRouterBaseURL(baseURL string) bool {
	return strings.Contains(strings.ToLower(baseURL), "openrouter.ai")
}
