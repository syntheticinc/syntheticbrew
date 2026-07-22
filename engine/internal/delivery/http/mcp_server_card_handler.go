package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// mcpServerCard is the static manifest served at
// /.well-known/mcp/server-card.json. It lets catalog scanners that cannot
// complete the OAuth flow discover the server declaratively, without weakening
// the authorization on the MCP endpoint itself.
type mcpServerCard struct {
	Name         string         `json:"name"`
	Title        string         `json:"title"`
	Description  string         `json:"description"`
	Version      string         `json:"version,omitempty"`
	Endpoint     string         `json:"endpoint"`
	Transport    string         `json:"transport"`
	Capabilities map[string]any `json:"capabilities"`
	Instructions string         `json:"instructions"`
}

// mcpServerCardDescription summarizes what the MCP surface does for humans and
// catalog scanners.
const mcpServerCardDescription = "MCP server for provisioning and managing AI chat agents: " +
	"create agents, ground them in knowledge bases, configure models and MCP tool servers, " +
	"and embed the resulting chat agent on any website."

// MCPServerCardHandler serves the anonymous, read-only server card. The MCP
// JSON-RPC endpoint itself stays fully authorized; this card discloses only
// what the server is, never per-tenant data.
type MCPServerCardHandler struct {
	card mcpServerCard
}

// NewMCPServerCardHandler builds the handler. version is the engine version;
// endpoint is the MCP JSON-RPC URL — absolute when the deployment's public base
// URL is known, otherwise the endpoint path.
func NewMCPServerCardHandler(version, endpoint string) *MCPServerCardHandler {
	return &MCPServerCardHandler{
		card: mcpServerCard{
			Name:         "syntheticbrew-engine",
			Title:        mcpServerTitle,
			Description:  mcpServerCardDescription,
			Version:      version,
			Endpoint:     endpoint,
			Transport:    "streamable-http",
			Capabilities: map[string]any{"tools": map[string]any{}},
			Instructions: mcpServerInstructions,
		},
	}
}

// ServeHTTP handles GET /.well-known/mcp/server-card.json.
func (h *MCPServerCardHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(h.card); err != nil {
		slog.ErrorContext(r.Context(), "encode mcp server card", "error", err)
	}
}
