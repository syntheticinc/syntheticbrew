package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// StdioTransport connects to an MCP server via stdio (subprocess).
type StdioTransport struct {
	command        string
	args           []string
	env            []string
	forwardHeaders []string
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	stdout         *bufio.Reader
	mu             sync.Mutex
}

// NewStdioTransport creates a transport that communicates via subprocess stdin/stdout.
func NewStdioTransport(command string, args []string, env []string, forwardHeaders ...[]string) *StdioTransport {
	var fh []string
	if len(forwardHeaders) > 0 {
		fh = forwardHeaders[0]
	}
	return &StdioTransport{command: command, args: args, env: env, forwardHeaders: fh}
}

func (t *StdioTransport) Start(_ context.Context) error {
	// Use background context for the subprocess lifetime — the process must survive
	// beyond the initial connection handshake. Shutdown is handled via Close().
	t.cmd = exec.Command(t.command, t.args...)
	if len(t.env) > 0 {
		t.cmd.Env = append(t.cmd.Environ(), t.env...)
	}

	var err error
	t.stdin, err = t.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := t.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	t.stdout = bufio.NewReader(stdout)

	if err := t.cmd.Start(); err != nil {
		return fmt.Errorf("start process: %w", err)
	}
	return nil
}

func (t *StdioTransport) Send(ctx context.Context, req *Request) (*Response, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.injectContext(ctx, req)

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	if _, err := t.stdin.Write(append(data, '\n')); err != nil {
		return nil, fmt.Errorf("write to stdin: %w", err)
	}

	line, err := t.stdout.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read from stdout: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return &resp, nil
}

func (t *StdioTransport) Notify(ctx context.Context, req *Request) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.injectContext(ctx, req)

	data, _ := json.Marshal(req)
	_, _ = t.stdin.Write(append(data, '\n'))
}

func (t *StdioTransport) Close() error {
	if t.stdin != nil {
		_ = t.stdin.Close()
	}
	if t.cmd != nil && t.cmd.Process != nil {
		return t.cmd.Process.Kill()
	}
	return nil
}

// injectContext adds a _context field to JSON-RPC params with forwarded headers.
// For stdio transport, HTTP headers cannot be sent directly, so they are embedded
// in the JSON-RPC params object as a convention.
func (t *StdioTransport) injectContext(ctx context.Context, req *Request) {
	if len(t.forwardHeaders) == 0 {
		return
	}
	rc := domain.GetRequestContext(ctx)
	if rc == nil {
		return
	}

	contextMap := make(map[string]string)
	for _, h := range t.forwardHeaders {
		if val := rc.Get(h); val != "" {
			contextMap[h] = val
		}
	}
	if len(contextMap) == 0 {
		return
	}

	// Ensure params is a map so we can add _context
	params, ok := req.Params.(map[string]interface{})
	if !ok {
		return
	}
	params["_context"] = contextMap
	req.Params = params
}
