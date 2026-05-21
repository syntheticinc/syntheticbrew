package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

const (
	// sseEndpointTimeout is the max time to wait for the endpoint event after SSE connect.
	sseEndpointTimeout = 5 * time.Second

	// sseReconnectDelay is the delay before attempting to reconnect after SSE stream drop.
	sseReconnectDelay = 1 * time.Second

	// sseMaxReconnectAttempts is the max consecutive reconnect attempts before giving up.
	sseMaxReconnectAttempts = 5
)

// SSETransport connects to an MCP server via SSE (Server-Sent Events).
// Per MCP spec: GET /sse for server->client events, POST /message for client->server requests.
type SSETransport struct {
	baseURL        string
	sseClient      *http.Client // For SSE GET -- no timeout (long-lived stream)
	postClient     *http.Client // For POST /message -- with timeout
	messageURL     string       // discovered from SSE endpoint event
	forwardHeaders []string

	mu            sync.Mutex
	pending       map[interface{}]chan *Response
	cancel        context.CancelFunc
	closed        bool
	endpointReady chan struct{} // closed when endpoint event is received
	reconnectMu   sync.Mutex   // prevents concurrent reconnect attempts
}

// NewSSETransport creates a transport for MCP SSE servers.
// baseURL should be the SSE endpoint URL (e.g., "http://server:3001/sse").
func NewSSETransport(baseURL string, forwardHeaders ...[]string) *SSETransport {
	var fh []string
	if len(forwardHeaders) > 0 {
		fh = forwardHeaders[0]
	}
	return &SSETransport{
		baseURL:        baseURL,
		sseClient:      &http.Client{},                          // No timeout -- SSE stream is persistent
		postClient:     &http.Client{Timeout: 30 * time.Second}, // Timeout for POST requests
		pending:        make(map[interface{}]chan *Response),
		forwardHeaders: fh,
	}
}

func (t *SSETransport) Start(_ context.Context) error {
	// Use background context for SSE connection lifecycle -- it must outlive the
	// caller's context (e.g. connectCtx with 10s timeout). The SSE stream stays
	// open until Close() is called.
	sseCtx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel

	if err := t.connectSSE(sseCtx); err != nil {
		cancel()
		return err
	}

	// Wait for endpoint event with timeout (replaces time.Sleep(100ms))
	select {
	case <-t.endpointReady:
		return nil
	case <-time.After(sseEndpointTimeout):
		return fmt.Errorf("timeout waiting for SSE endpoint event after %s", sseEndpointTimeout)
	}
}

// connectSSE establishes a new SSE connection and starts reading events.
// Caller must hold no locks. The endpointReady channel is recreated.
func (t *SSETransport) connectSSE(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.baseURL, nil)
	if err != nil {
		return fmt.Errorf("create SSE request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := t.sseClient.Do(req)
	if err != nil {
		return fmt.Errorf("connect to SSE: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return fmt.Errorf("SSE server returned %d", resp.StatusCode)
	}

	// Reset endpoint state for the new connection
	t.mu.Lock()
	t.messageURL = ""
	t.endpointReady = make(chan struct{})
	t.mu.Unlock()

	go t.readSSE(ctx, resp.Body)

	return nil
}

func (t *SSETransport) Send(ctx context.Context, req *Request) (*Response, error) {
	resp, errBody, err := t.doSend(ctx, req)
	if err == nil {
		return resp, nil
	}

	// If error looks session-related (stale session ID), reconnect once and retry
	if !isSessionError(errBody) {
		return nil, err
	}

	slog.WarnContext(context.Background(), "SSE transport: session error on Send, attempting reconnect",
		"error", err, "body", string(errBody))

	if reconnErr := t.reconnect(); reconnErr != nil {
		return nil, fmt.Errorf("reconnect after session error: %w (original: %w)", reconnErr, err)
	}

	resp, _, retryErr := t.doSend(ctx, req)
	if retryErr != nil {
		return nil, fmt.Errorf("retry after reconnect: %w", retryErr)
	}
	return resp, nil
}

// doSend performs the actual HTTP POST and waits for a response.
// Returns the response body bytes on 4xx errors for inspection by the caller.
func (t *SSETransport) doSend(ctx context.Context, req *Request) (*Response, []byte, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, nil, fmt.Errorf("transport closed")
	}

	// Create response channel for this request ID
	ch := make(chan *Response, 1)
	t.pending[req.ID] = ch
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		delete(t.pending, req.ID)
		t.mu.Unlock()
	}()

	// POST request to message endpoint
	msgURL := t.getMessageURL()
	if msgURL == "" {
		return nil, nil, fmt.Errorf("message endpoint not discovered yet")
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, msgURL, bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	t.applyForwardHeaders(ctx, httpReq)

	httpResp, err := t.postClient.Do(httpReq)
	if err != nil {
		return nil, nil, fmt.Errorf("send message: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(httpResp.Body, 512))
		return nil, errBody, fmt.Errorf("message endpoint returned %d: %s", httpResp.StatusCode, string(errBody))
	}

	// Some MCP servers return the JSON-RPC response in the HTTP body
	// (not via SSE stream). Try to read it first.
	body, readErr := io.ReadAll(httpResp.Body)
	if readErr == nil && len(body) > 2 {
		var directResp Response
		if json.Unmarshal(body, &directResp) == nil && directResp.ID != nil {
			return &directResp, nil, nil
		}
	}

	// Otherwise wait for response via SSE stream
	select {
	case resp := <-ch:
		return resp, nil, nil
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-time.After(30 * time.Second):
		return nil, nil, fmt.Errorf("timeout waiting for SSE response")
	}
}

func (t *SSETransport) Notify(ctx context.Context, req *Request) {
	msgURL := t.getMessageURL()
	if msgURL == "" {
		return
	}

	data, err := json.Marshal(req)
	if err != nil {
		return
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, msgURL, bytes.NewReader(data))
	if err != nil {
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	t.applyForwardHeaders(ctx, httpReq)

	resp, err := t.postClient.Do(httpReq)
	if err != nil {
		return
	}
	resp.Body.Close()
}

func (t *SSETransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.closed = true
	if t.cancel != nil {
		t.cancel()
	}

	// Close all pending channels
	for id, ch := range t.pending {
		close(ch)
		delete(t.pending, id)
	}

	return nil
}

func (t *SSETransport) getMessageURL() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.messageURL
}

func (t *SSETransport) setMessageURL(url string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// If relative URL, resolve against base
	if strings.HasPrefix(url, "/") {
		// Extract base from SSE URL (scheme + host)
		base := t.baseURL
		if idx := strings.Index(base, "://"); idx != -1 {
			rest := base[idx+3:]
			if slashIdx := strings.Index(rest, "/"); slashIdx != -1 {
				base = base[:idx+3+slashIdx]
			}
		}
		url = base + url
	}

	t.messageURL = url
}

// readSSE processes the SSE stream from the server.
// When the stream drops (not due to context cancellation), it attempts to reconnect.
func (t *SSETransport) readSSE(ctx context.Context, body io.ReadCloser) {
	defer body.Close()

	scanner := bufio.NewScanner(body)
	var eventType string

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := scanner.Text()

		if line == "" {
			eventType = ""
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			t.handleSSEData(eventType, data)
		}
	}

	if err := scanner.Err(); err != nil {
		slog.WarnContext(context.Background(), "SSE transport: stream error", "error", err)
	}

	// Stream ended. If context is still active, the server dropped the connection.
	select {
	case <-ctx.Done():
		// Intentional shutdown -- do not reconnect
		return
	default:
	}

	slog.WarnContext(context.Background(), "SSE transport: stream dropped, attempting reconnect", "base_url", t.baseURL)
	if err := t.reconnect(); err != nil {
		slog.ErrorContext(context.Background(), "SSE transport: reconnect failed after stream drop", "error", err)
	}
}

// reconnect tears down the old SSE connection and establishes a new one.
// It is safe to call from multiple goroutines -- only one reconnect runs at a time.
func (t *SSETransport) reconnect() error {
	t.reconnectMu.Lock()
	defer t.reconnectMu.Unlock()

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return fmt.Errorf("transport closed")
	}
	t.mu.Unlock()

	// Cancel old SSE context to stop any lingering readSSE goroutine
	if t.cancel != nil {
		t.cancel()
	}

	var lastErr error
	for attempt := 1; attempt <= sseMaxReconnectAttempts; attempt++ {
		slog.InfoContext(context.Background(), "SSE transport: reconnect attempt", "attempt", attempt, "max", sseMaxReconnectAttempts)

		// Create a fresh context for the new connection
		sseCtx, cancel := context.WithCancel(context.Background())
		t.cancel = cancel

		if err := t.connectSSE(sseCtx); err != nil {
			cancel()
			lastErr = err
			slog.WarnContext(context.Background(), "SSE transport: reconnect attempt failed",
				"attempt", attempt, "error", err)
			time.Sleep(sseReconnectDelay * time.Duration(attempt))
			continue
		}

		// Wait for the new endpoint event
		t.mu.Lock()
		ready := t.endpointReady
		t.mu.Unlock()

		select {
		case <-ready:
			slog.InfoContext(context.Background(), "SSE transport: reconnected successfully", "attempt", attempt)
			return nil
		case <-time.After(sseEndpointTimeout):
			cancel()
			lastErr = fmt.Errorf("timeout waiting for endpoint event")
			slog.WarnContext(context.Background(), "SSE transport: reconnect endpoint timeout", "attempt", attempt)
			continue
		}
	}

	return fmt.Errorf("reconnect failed after %d attempts: %w", sseMaxReconnectAttempts, lastErr)
}

func (t *SSETransport) handleSSEData(eventType, data string) {
	switch eventType {
	case "endpoint":
		// Server announces its message endpoint
		t.setMessageURL(strings.TrimSpace(data))
		slog.InfoContext(context.Background(), "SSE transport: discovered message endpoint", "url", data)

		// Signal that endpoint is ready
		t.mu.Lock()
		select {
		case <-t.endpointReady:
			// Already closed -- nothing to do
		default:
			close(t.endpointReady)
		}
		t.mu.Unlock()

	case "message":
		// JSON-RPC response
		var resp Response
		if err := json.Unmarshal([]byte(data), &resp); err != nil {
			slog.WarnContext(context.Background(), "SSE transport: failed to parse response", "error", err)
			return
		}

		// Normalize response ID: JSON unmarshals numbers as float64,
		// but pending map keys are int64 from nextRequestID().
		normalizedID := normalizeID(resp.ID)

		t.mu.Lock()
		if ch, ok := t.pending[normalizedID]; ok {
			ch <- &resp
		}
		t.mu.Unlock()

	default:
		slog.DebugContext(context.Background(), "SSE transport: unknown event", "type", eventType, "data", data[:min(len(data), 100)])
	}
}

// isSessionError checks if an HTTP error response body indicates a stale session.
func isSessionError(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	lower := strings.ToLower(string(body))
	return strings.Contains(lower, "session")
}

// normalizeID converts JSON-unmarshalled float64 IDs back to int64 for map lookup.
// JSON numbers unmarshal as float64 in Go, but pending map keys are int64.
func normalizeID(id interface{}) interface{} {
	if f, ok := id.(float64); ok {
		return int64(f)
	}
	return id
}

// applyForwardHeaders copies configured headers from RequestContext to the HTTP request.
func (t *SSETransport) applyForwardHeaders(ctx context.Context, httpReq *http.Request) {
	if len(t.forwardHeaders) == 0 {
		return
	}
	rc := domain.GetRequestContext(ctx)
	if rc == nil {
		return
	}
	for _, headerName := range t.forwardHeaders {
		if val := rc.Get(headerName); val != "" {
			httpReq.Header.Set(headerName, val)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
