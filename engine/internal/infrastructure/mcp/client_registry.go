package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/persistence/models"
	"github.com/syntheticinc/bytebrew/engine/pkg/plugin"
)

// ClientRegistry manages connected MCP clients for a single tenant.
//
// One ClientRegistry instance is owned by exactly one tenant — broadcast
// operations (CloseAll, Load) act on that tenant's clients only. Multi-tenant
// orchestration is the responsibility of Manager.
//
// Implements tools.MCPClientProvider via GetMCPTools.
type ClientRegistry struct {
	mu      sync.RWMutex
	clients map[string]*Client
}

// NewClientRegistry creates a new empty ClientRegistry.
func NewClientRegistry() *ClientRegistry {
	return &ClientRegistry{clients: make(map[string]*Client)}
}

// Register adds a connected client to the registry.
func (r *ClientRegistry) Register(name string, client *Client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients[name] = client
}

// client returns the registered client for the given name. Internal accessor
// reserved for the MCP package (Manager, Refresher) so they can operate on the
// underlying *Client without exposing it through the public Eino tool surface.
func (r *ClientRegistry) client(name string) (*Client, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.clients[name]
	return c, ok
}

// GetMCPTools returns Eino-compatible tools for the named MCP server.
// Returns nil, nil if the server is not registered or not connected.
func (r *ClientRegistry) GetMCPTools(name string) ([]tool.InvokableTool, error) {
	r.mu.RLock()
	client, ok := r.clients[name]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("mcp server %q not registered", name)
	}
	if !client.IsConnected() {
		return nil, nil
	}

	mcpTools := client.ListTools()
	result := make([]tool.InvokableTool, 0, len(mcpTools))
	for _, mt := range mcpTools {
		result = append(result, AdaptMCPTool(client, mt))
	}
	return result, nil
}

// CloseAll closes every client registered with this ClientRegistry.
//
// Scope: this tenant only. Multi-tenant shutdown is orchestrated by
// Manager.Shutdown which fans out per-tenant CloseAll calls.
func (r *ClientRegistry) CloseAll() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for name, client := range r.clients {
		_ = client.Close()
		delete(r.clients, name)
	}
}

// Names returns all registered server names.
func (r *ClientRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.clients))
	for name := range r.clients {
		names = append(names, name)
	}
	return names
}

// Load dials every server in the supplied slice and registers the successful
// clients. Failures are logged and skipped so a single broken endpoint does
// not abort an entire tenant's MCP surface. Honors the supplied transport
// policy (multi-tenant deployments restrict stdio via the injected policy).
//
// Idempotent in the sense that callers may invoke CloseAll first to recycle
// state — Load itself does not implicitly reset the registry.
func (r *ClientRegistry) Load(ctx context.Context, servers []models.MCPServerModel, policy plugin.TransportPolicy) error {
	for _, srv := range servers {
		client, err := dialServer(ctx, srv, policy)
		if err != nil {
			// dialServer already logs the underlying reason at warn level.
			continue
		}
		if client == nil {
			// Skipped (e.g. policy block or unknown type) — already logged.
			continue
		}
		r.Register(srv.Name, client)
	}
	return nil
}

// Disconnect closes the registered client for the given name and removes it
// from the registry. Per-server, idempotent — no-op (returns nil) when the
// server is not registered. Sibling clients are untouched.
func (r *ClientRegistry) Disconnect(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.clients[name]
	if !ok {
		return nil
	}
	_ = existing.Close()
	delete(r.clients, name)
	return nil
}

// Reconnect dials a fresh client for srv.Name and only on success swaps out
// any existing one. Per-server, idempotent. Sibling clients are untouched.
//
// Dial-first-then-swap: if the dial fails the previously-registered client
// stays live so callers continue to see its tools rather than a missing
// entry. Brief overlap of old + new TCP connections during the swap is
// acceptable for MCP (low frequency, low connection count).
func (r *ClientRegistry) Reconnect(ctx context.Context, srv models.MCPServerModel, policy plugin.TransportPolicy) error {
	fresh, err := dialServer(ctx, srv, policy)
	if err != nil {
		return fmt.Errorf("reconnect mcp server %q: %w", srv.Name, err)
	}
	if fresh == nil {
		// Skipped (policy / unknown type) — surface as error so callers know.
		return fmt.Errorf("reconnect mcp server %q: skipped (transport not allowed or unknown type %q)", srv.Name, srv.Type)
	}

	r.mu.Lock()
	if existing, ok := r.clients[srv.Name]; ok {
		_ = existing.Close()
	}
	r.clients[srv.Name] = fresh
	r.mu.Unlock()
	return nil
}

// dialServer materialises a Transport for the given DB row and connects an
// MCP Client. Returns (nil, nil) when the server is intentionally skipped
// (policy block, unknown type) — callers continue to the next server.
//
// Lives next to ClientRegistry instead of in the service/mcp package so that
// Reconnect can share the exact same construction logic without an import cycle.
func dialServer(ctx context.Context, srv models.MCPServerModel, policy plugin.TransportPolicy) (*Client, error) {
	var forwardHeaders []string
	if srv.ForwardHeaders != nil && *srv.ForwardHeaders != "" {
		if err := json.Unmarshal([]byte(*srv.ForwardHeaders), &forwardHeaders); err != nil {
			slog.WarnContext(ctx, "mcp connector: failed to parse forward_headers", "name", srv.Name, "error", err)
			return nil, nil
		}
	}

	var transport Transport
	switch srv.Type {
	case "stdio":
		if err := policy.IsAllowed("stdio"); err != nil {
			slog.WarnContext(ctx, "MCP stdio transport blocked by policy", "name", srv.Name, "reason", err.Error())
			return nil, nil
		}
		var args []string
		if srv.Args != nil && *srv.Args != "" {
			if err := json.Unmarshal([]byte(*srv.Args), &args); err != nil {
				slog.WarnContext(ctx, "mcp connector: failed to parse args", "name", srv.Name, "error", err)
				return nil, nil
			}
		}
		transport = NewStdioTransport(srv.Command, args, nil, forwardHeaders)
	case "http":
		transport = NewHTTPTransport(srv.URL, forwardHeaders)
	case "sse":
		transport = NewSSETransport(srv.URL, forwardHeaders)
	case "streamable-http":
		transport = NewStreamableHTTPTransport(srv.URL, forwardHeaders)
	default:
		slog.WarnContext(ctx, "unknown MCP server type, skipping", "name", srv.Name, "type", srv.Type)
		return nil, nil
	}

	client := NewClient(srv.Name, transport)
	connectCtx, connectCancel := context.WithTimeout(ctx, 10*time.Second)
	defer connectCancel()
	if err := client.Connect(connectCtx); err != nil {
		slog.WarnContext(ctx, "MCP server unavailable, skipping", "name", srv.Name, "error", err)
		return nil, err
	}

	tools := client.ListTools()
	slog.InfoContext(ctx, "MCP server connected", "name", srv.Name, "tools", len(tools))
	return client, nil
}
