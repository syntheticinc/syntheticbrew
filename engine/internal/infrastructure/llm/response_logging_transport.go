package llm

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
)

// responseLoggingTransport logs raw upstream 4xx/5xx response bodies — the
// eino-ext driver otherwise collapses them into an opaque error string.
type responseLoggingTransport struct {
	base http.RoundTripper
}

const responseLoggingBodyCap = 16 * 1024

func (t *responseLoggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	if resp == nil || resp.StatusCode < 400 || resp.Body == nil {
		return resp, nil
	}

	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, responseLoggingBodyCap+1))
	if cerr := resp.Body.Close(); cerr != nil {
		slog.WarnContext(req.Context(), "llm provider response: close original body", "error", cerr)
	}
	if readErr != nil {
		resp.Body = io.NopCloser(bytes.NewReader(nil))
		return resp, nil
	}

	truncated := false
	if len(raw) > responseLoggingBodyCap {
		raw = raw[:responseLoggingBodyCap]
		truncated = true
	}

	slog.ErrorContext(req.Context(), "llm provider error response",
		"status", resp.StatusCode,
		"url", req.URL.Redacted(),
		"body", string(raw),
		"truncated", truncated,
	)

	resp.Body = io.NopCloser(bytes.NewReader(raw))
	resp.ContentLength = int64(len(raw))
	return resp, nil
}
