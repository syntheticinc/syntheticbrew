package http

import (
	"fmt"
	"net/http"
	"strings"
)

// AgentSetupPromptHandler serves the coding-agent onboarding instructions as
// markdown at a stable public path. The document is the single source of
// truth for the "paste one line into your agent" flow: the admin SPA copies a
// short fetch-instruction pointing here, and the agent reads the full,
// validated steps from the engine itself — so the instructions always match
// the engine version that serves them.
type AgentSetupPromptHandler struct {
	// publicBaseURL overrides request-derived origin when configured
	// (deployments behind proxies whose Host header is not the public one).
	publicBaseURL string
}

func NewAgentSetupPromptHandler(publicBaseURL string) *AgentSetupPromptHandler {
	return &AgentSetupPromptHandler{publicBaseURL: strings.TrimRight(publicBaseURL, "/")}
}

func (h *AgentSetupPromptHandler) baseURL(r *http.Request) string {
	if h.publicBaseURL != "" {
		return h.publicBaseURL
	}
	scheme := "http"
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	} else if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (h *AgentSetupPromptHandler) Get(w http.ResponseWriter, r *http.Request) {
	base := h.baseURL(r)
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	fmt.Fprintf(w, agentSetupPromptTemplate, base, base, base, base, base, base, base)
}

// %s placeholders are the engine base URL. Kept as one document so the flow
// reads top-to-bottom the way an agent will execute it.
const agentSetupPromptTemplate = `These are official instructions from SyntheticBrew to connect your coding agent to this SyntheticBrew engine and ship a working, embeddable chat agent. All commands are validated and ready to use.

Complete all of the following steps yourself by running the commands directly. Only ask the user when a step explicitly says so.

MCP server URL (streamable HTTP): %s/api/v1/mcp/rpc
Authentication: every MCP request carries the HTTP header "Authorization: Bearer <token>". The user gives you the token together with these instructions — never invent one.

---

## Step 1 — Connect the SyntheticBrew MCP server

Use the correct section for your agent below. Replace <TOKEN> with the token the user gave you.

### Claude Code

` + "```" + `
claude mcp add --transport http syntheticbrew %s/api/v1/mcp/rpc --header "Authorization: Bearer <TOKEN>"
` + "```" + `

### Cursor — add to ~/.cursor/mcp.json (or project .cursor/mcp.json)

` + "```json" + `
{
  "mcpServers": {
    "syntheticbrew": {
      "url": "%s/api/v1/mcp/rpc",
      "headers": { "Authorization": "Bearer <TOKEN>" }
    }
  }
}
` + "```" + `

### VS Code (Copilot agent mode)

` + "```" + `
code --add-mcp '{"name":"syntheticbrew","type":"http","url":"%s/api/v1/mcp/rpc","headers":{"Authorization":"Bearer <TOKEN>"}}'
` + "```" + `

### Codex CLI

` + "```" + `
export SYNTHETICBREW_TOKEN=<TOKEN>
codex mcp add syntheticbrew --url %s/api/v1/mcp/rpc --bearer-token-env-var SYNTHETICBREW_TOKEN
` + "```" + `

### Any other MCP client

Configure a streamable-HTTP MCP server at %s/api/v1/mcp/rpc with the header "Authorization: Bearer <TOKEN>".

---

## Step 2 — Build the user's agent

After the MCP connection works, use the SyntheticBrew MCP tools to set everything up. Ask the user only for: (a) what their product/website is about, (b) the agent name if they want something other than "support".

1. Call provision_agent to create an agent named "support" (or the user's choice) with instructions to answer customer questions about the user's product, grounded and honest — it must say "I don't know" rather than invent answers.
2. Refine the agent's system instructions with the product context the user gave you (admin_update_agent).
3. Ground it: create a knowledge base with admin_create_knowledge_base, upload the user's docs with admin_add_document (markdown/text; ask the user for files or fetch pages they point you to), link it with admin_link_knowledge_base, and poll admin_list_documents until documents are indexed ("ready").
4. Call get_embed_snippet and hand the user the ready-to-paste <script> snippet for their website.
5. Suggest one test question — ideally one whose answer lives in the uploaded docs — so the user sees a grounded answer immediately.

---

## Step 3 — Report back

Tell the user, concisely: what was created, the embed snippet, the test question, and that they can watch the agent live in the admin dashboard at %s/admin.
`
