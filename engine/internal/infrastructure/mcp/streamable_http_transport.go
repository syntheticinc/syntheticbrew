package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// StreamableHTTPTransport connects to an MCP server via the Streamable HTTP transport
// (MCP spec 2025-03-26). Sends JSON-RPC via POST; server may respond with
// application/json (single response) or text/event-stream (SSE stream).
type StreamableHTTPTransport struct {
	url            string
	client         *http.Client
	forwardHeaders []string

	mu        sync.RWMutex
	sessionID string // Mcp-Session-Id from server
}

// NewStreamableHTTPTransport creates a transport for MCP Streamable HTTP servers.
func NewStreamableHTTPTransport(url string, forwardHeaders ...[]string) *StreamableHTTPTransport {
	var fh []string
	if len(forwardHeaders) > 0 {
		fh = forwardHeaders[0]
	}
	return &StreamableHTTPTransport{
		url:            url,
		client:         &http.Client{},
		forwardHeaders: fh,
	}
}

func (t *StreamableHTTPTransport) Start(_ context.Context) error {
	return nil // Stateless — initialization happens via normal Send (initialize request).
}

func (t *StreamableHTTPTransport) Send(ctx context.Context, req *Request) (*Response, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")

	t.mu.RLock()
	if t.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", t.sessionID)
	}
	t.mu.RUnlock()

	t.applyForwardHeaders(ctx, httpReq)

	httpResp, err := t.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer httpResp.Body.Close()

	// Store session ID if server provided one.
	if sid := httpResp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.mu.Lock()
		t.sessionID = sid
		t.mu.Unlock()
	}

	if httpResp.StatusCode >= 400 {
		body, _ := io.ReadAll(httpResp.Body)
		return nil, fmt.Errorf("server returned %d: %s", httpResp.StatusCode, string(body))
	}

	ct := httpResp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		return t.parseSSEResponse(httpResp.Body, req.ID)
	}

	// Default: parse as JSON.
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return &resp, nil
}

func (t *StreamableHTTPTransport) Notify(ctx context.Context, req *Request) {
	data, err := json.Marshal(req)
	if err != nil {
		return
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(data))
	if err != nil {
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")

	t.mu.RLock()
	if t.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", t.sessionID)
	}
	t.mu.RUnlock()

	t.applyForwardHeaders(ctx, httpReq)

	resp, err := t.client.Do(httpReq)
	if err != nil {
		return
	}
	resp.Body.Close()
}

func (t *StreamableHTTPTransport) Close() error {
	return nil
}

// parseSSEResponse reads an SSE stream from the response body and returns the
// first JSON-RPC response whose ID matches reqID.
func (t *StreamableHTTPTransport) parseSSEResponse(body io.Reader, reqID interface{}) (*Response, error) {
	scanner := bufio.NewScanner(body)
	var eventType string

	for scanner.Scan() {
		line := scanner.Text()

		// Blank line = end of event block.
		if line == "" {
			eventType = ""
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		// Accept "message" events and events without explicit type (default per SSE spec).
		if eventType != "" && eventType != "message" {
			continue
		}

		var resp Response
		if err := json.Unmarshal([]byte(data), &resp); err != nil {
			continue // skip malformed events
		}

		if normalizeID(resp.ID) == normalizeID(reqID) {
			return &resp, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read SSE stream: %w", err)
	}

	return nil, fmt.Errorf("no matching response in SSE stream for id %v", reqID)
}

// applyForwardHeaders copies configured headers from RequestContext to the HTTP request.
func (t *StreamableHTTPTransport) applyForwardHeaders(ctx context.Context, httpReq *http.Request) {
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
