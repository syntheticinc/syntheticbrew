package llm

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureLogHandler is a slog.Handler that captures records into an in-memory
// buffer for assertion. Records are written as one JSON object per line.
type captureLogHandler struct {
	buf *bytes.Buffer
	inner slog.Handler
}

func newCaptureLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), buf
}

// stubTransport returns a canned response, optionally with the given body.
type stubTransport struct {
	status int
	body   string
	gotReq *http.Request
}

func (s *stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.gotReq = req
	return &http.Response{
		StatusCode: s.status,
		Body:       io.NopCloser(strings.NewReader(s.body)),
		Header:     make(http.Header),
	}, nil
}

func TestResponseLoggingTransport_LogsBodyOn4xx(t *testing.T) {
	prevLogger := slog.Default()
	logger, buf := newCaptureLogger()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	openRouterError := `{"error":{"message":"Provider returned error","metadata":{"raw":"{\"error\":{\"message\":\"Invalid 'tools[0].function.name': string does not match pattern...\"}}"}}}`
	rt := &responseLoggingTransport{
		base: &stubTransport{status: 400, body: openRouterError},
	}

	req, err := http.NewRequest(http.MethodPost, "https://openrouter.ai/api/v1/chat/completions", nil)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Body must be restored — downstream still gets the raw bytes.
	gotBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, openRouterError, string(gotBody),
		"response body must be restored after logging so downstream parsers still see it")

	// Log must contain the raw body field, the status, and the URL.
	logOutput := buf.String()
	assert.Contains(t, logOutput, "llm provider error response")
	assert.Contains(t, logOutput, `"status":400`)
	assert.Contains(t, logOutput, "openrouter.ai")
	assert.Contains(t, logOutput, "Invalid 'tools[0].function.name'",
		"raw error.metadata.raw content must appear in the log so operators can diagnose")
}

func TestResponseLoggingTransport_LogsBodyOn5xx(t *testing.T) {
	prevLogger := slog.Default()
	logger, buf := newCaptureLogger()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	rt := &responseLoggingTransport{
		base: &stubTransport{status: 503, body: `{"error":"upstream unavailable"}`},
	}
	req, _ := http.NewRequest(http.MethodPost, "https://api.example.test/v1/chat", nil)

	_, err := rt.RoundTrip(req)
	require.NoError(t, err)

	logOutput := buf.String()
	assert.Contains(t, logOutput, `"status":503`)
	assert.Contains(t, logOutput, "upstream unavailable")
}

func TestResponseLoggingTransport_DoesNotLogOn2xx(t *testing.T) {
	prevLogger := slog.Default()
	logger, buf := newCaptureLogger()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	rt := &responseLoggingTransport{
		base: &stubTransport{status: 200, body: `{"choices":[{"message":{"content":"hi"}}]}`},
	}
	req, _ := http.NewRequest(http.MethodPost, "https://api.example.test/v1/chat", nil)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)

	logOutput := buf.String()
	assert.NotContains(t, logOutput, "llm provider error response",
		"successful responses must NOT trigger error logging")

	// Body still readable.
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "hi")
}

func TestResponseLoggingTransport_TruncatesLargeBodies(t *testing.T) {
	prevLogger := slog.Default()
	logger, buf := newCaptureLogger()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	// 20 KiB of 'X' — bigger than the 16 KiB cap.
	bigBody := strings.Repeat("X", 20*1024)
	rt := &responseLoggingTransport{
		base: &stubTransport{status: 502, body: bigBody},
	}
	req, _ := http.NewRequest(http.MethodPost, "https://api.example.test/v1/chat", nil)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)

	logOutput := buf.String()
	assert.Contains(t, logOutput, `"truncated":true`,
		"oversized bodies must be flagged as truncated in the log")

	// Restored body should match the capped size.
	got, _ := io.ReadAll(resp.Body)
	assert.Equal(t, responseLoggingBodyCap, len(got),
		"restored body must be capped at responseLoggingBodyCap")
}

func TestResponseLoggingTransport_PropagatesTransportError(t *testing.T) {
	rt := &responseLoggingTransport{
		base: errorTransport{},
	}
	req, _ := http.NewRequest(http.MethodPost, "https://api.example.test/v1/chat", nil)

	_, err := rt.RoundTrip(req)
	require.Error(t, err, "transport-level errors must propagate unchanged")
}

type errorTransport struct{}

func (errorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, io.ErrUnexpectedEOF
}

// TestResponseLoggingTransport_AgainstRealServer wraps a real httptest server
// to confirm the transport works end-to-end through net/http machinery, not
// just stub roundtrippers.
func TestResponseLoggingTransport_AgainstRealServer(t *testing.T) {
	prevLogger := slog.Default()
	logger, buf := newCaptureLogger()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	const upstreamBody = `{"error":{"message":"Provider returned error","metadata":{"raw":"diag"}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(upstreamBody))
	}))
	defer srv.Close()

	client := &http.Client{Transport: &responseLoggingTransport{base: http.DefaultTransport}}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", strings.NewReader(`{}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, upstreamBody, string(body),
		"end-to-end: downstream must read the full upstream body even after logging")
	assert.Contains(t, buf.String(), "diag",
		"end-to-end: log must contain the upstream body content")
}
