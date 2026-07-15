---
name: syntheticbrew-docs-mcp
description: Answer questions about SyntheticBrew Engine (configuration, API, deployment, agents, tools, RAG, knowledge graphs) by querying the public docs-search MCP server. No signup or credentials required.
license: CC-BY-4.0
---

# Searching SyntheticBrew documentation via MCP

SyntheticBrew operates a public, unauthenticated MCP server that searches the official product documentation.

## Steps

1. Connect to the MCP server at `https://mcp.syntheticbrew.ai/sse` (SSE transport, no authentication).
2. Call the `search_docs` tool with `{ "query": "<natural language question>" }`.
3. Use the returned passages to answer; cite the doc URLs they reference.

Server card: https://syntheticbrew.ai/.well-known/mcp/server-card.json

## Fallbacks without MCP

- Curated link index: https://syntheticbrew.ai/llms.txt
- Documentation site: https://syntheticbrew.ai/docs/
- Any page on https://syntheticbrew.ai/ returns a markdown version when requested with `Accept: text/markdown`.
