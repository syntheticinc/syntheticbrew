package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// propertiesNormalizingTransport injects `properties: {}` into outgoing tool
// schemas of `type: object` that lack the key — OpenAI rejects the bare form.
type propertiesNormalizingTransport struct {
	base http.RoundTripper
}

func (t *propertiesNormalizingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body == nil || req.Method != http.MethodPost {
		return t.base.RoundTrip(req)
	}
	ct := req.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		return t.base.RoundTrip(req)
	}

	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("properties_normalize: read request body: %w", err)
	}
	if cerr := req.Body.Close(); cerr != nil {
		slog.WarnContext(req.Context(), "properties_normalize: close original body", "error", cerr)
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		// Non-JSON body — restore and forward unchanged.
		req.Body = io.NopCloser(bytes.NewReader(raw))
		req.ContentLength = int64(len(raw))
		return t.base.RoundTrip(req)
	}

	mutated := normalizeToolProperties(payload)

	out := raw
	if mutated {
		merged, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			// Fall back to the original bytes rather than corrupt the request.
			slog.ErrorContext(req.Context(), "properties_normalize: marshal failed, forwarding original",
				"error", marshalErr)
			req.Body = io.NopCloser(bytes.NewReader(raw))
			req.ContentLength = int64(len(raw))
			return t.base.RoundTrip(req)
		}
		out = merged
	}
	req.Body = io.NopCloser(bytes.NewReader(out))
	req.ContentLength = int64(len(out))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(out)), nil
	}
	return t.base.RoundTrip(req)
}

// normalizeToolProperties inserts properties:{} for type=object schemas that
// don't already have the key. Returns true if any tool was mutated.
func normalizeToolProperties(payload map[string]any) bool {
	toolsRaw, ok := payload["tools"]
	if !ok {
		return false
	}
	tools, ok := toolsRaw.([]any)
	if !ok || len(tools) == 0 {
		return false
	}

	mutated := false
	for _, toolRaw := range tools {
		tool, ok := toolRaw.(map[string]any)
		if !ok {
			continue
		}
		fn, ok := tool["function"].(map[string]any)
		if !ok {
			continue
		}
		params, ok := fn["parameters"].(map[string]any)
		if !ok {
			continue
		}
		if params["type"] != "object" {
			continue
		}
		if _, present := params["properties"]; present {
			continue
		}
		params["properties"] = map[string]any{}
		mutated = true
	}
	return mutated
}
