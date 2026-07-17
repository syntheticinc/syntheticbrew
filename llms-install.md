# Installing the SyntheticBrew MCP server

SyntheticBrew is a **remote** MCP server (Streamable HTTP + OAuth 2.1). There is nothing to
install or run locally — connect your MCP client to the hosted endpoint and authenticate
through the browser. No API key to paste.

## Endpoint

- **URL:** `https://app.syntheticbrew.ai/api/v1/mcp/rpc`
- **Transport:** `http` (Streamable HTTP)
- **Auth:** OAuth 2.1 — the client opens a browser for sign-in on first use

## Client configuration

Add this to the MCP servers config (Cursor `~/.cursor/mcp.json`, Cline, VS Code, Claude Code, or any MCP client):

```json
{
  "mcpServers": {
    "syntheticbrew": {
      "type": "http",
      "url": "https://app.syntheticbrew.ai/api/v1/mcp/rpc"
    }
  }
}
```

On first use the client runs the OAuth handshake in a browser. Do not add an `Authorization`
header for the hosted endpoint — the OAuth flow supplies the token.

## What it does

Once connected, the server exposes tools to build and ship an embeddable AI chat agent from
your coding assistant: create agents, models, and knowledge bases, add documents, and get an
embed snippet. Example prompt:

> "Create a support agent grounded in these release notes and give me the embed snippet."

## Self-hosted engine

If you run SyntheticBrew yourself, point the URL at your own engine and pass a bearer token:

```json
{
  "mcpServers": {
    "syntheticbrew": {
      "type": "http",
      "url": "<ENGINE_URL>/api/v1/mcp/rpc",
      "headers": { "Authorization": "Bearer <token>" }
    }
  }
}
```
