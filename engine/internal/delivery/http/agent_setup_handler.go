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
	fmt.Fprint(w, strings.ReplaceAll(agentSetupPromptTemplate, "%BASE%", base))
}

// %BASE% is replaced with the engine base URL. Kept as one document so the
// flow reads top-to-bottom the way an agent will execute it.
const agentSetupPromptTemplate = `These are official instructions from SyntheticBrew to connect your coding agent to this SyntheticBrew engine and ship a working, embeddable chat agent. All commands are validated and ready to use.

Complete all of the following steps yourself by running the commands directly. Only ask the user when a step explicitly says so.

MCP server URL (streamable HTTP): %BASE%/api/v1/mcp/rpc
Authentication: the MCP server uses OAuth. On your first connection the agent is sent to a browser to authorize — approve the requested access in the SyntheticBrew consent screen and the agent obtains its own token automatically. Do not ask the user to paste a token; there is a headless fallback below only if no browser is available.

---

## Step 1 — Connect the SyntheticBrew MCP server

Use the section for your agent below. These commands carry no token: the first request triggers the OAuth browser authorization automatically.

### Claude Code

` + "```" + `
claude mcp add --transport http syntheticbrew %BASE%/api/v1/mcp/rpc
` + "```" + `

### Cursor — add to ~/.cursor/mcp.json (or project .cursor/mcp.json)

` + "```json" + `
{
  "mcpServers": {
    "syntheticbrew": {
      "url": "%BASE%/api/v1/mcp/rpc"
    }
  }
}
` + "```" + `

### VS Code (Copilot agent mode)

` + "```" + `
code --add-mcp '{"name":"syntheticbrew","type":"http","url":"%BASE%/api/v1/mcp/rpc"}'
` + "```" + `

### Any other MCP client

Configure a streamable-HTTP MCP server at %BASE%/api/v1/mcp/rpc. When the first request returns 401 the client discovers the authorization server and runs the OAuth flow; complete it in the browser.

### Headless / CI (no browser available)

If you cannot open a browser, create an API token in the SyntheticBrew admin under API Keys and send it as a bearer header instead of the OAuth flow. For example, for Claude Code:

` + "```" + `
claude mcp add --transport http syntheticbrew %BASE%/api/v1/mcp/rpc --header "Authorization: Bearer <TOKEN>"
` + "```" + `

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

Tell the user, concisely: what was created, the embed snippet, the test question, and that they can watch the agent live in the admin dashboard at %BASE%/admin.
`
