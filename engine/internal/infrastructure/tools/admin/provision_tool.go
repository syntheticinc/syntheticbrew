package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/url"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// validateEndpoint normalizes a caller-supplied engine base URL. It returns the
// trimmed URL, or a non-empty user-facing error message if the value is not a
// well-formed absolute http(s) URL.
func validateEndpoint(raw string) (string, string) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Sprintf("Invalid endpoint %q. Must be an absolute http(s) URL (e.g. https://engine.example.com).", raw)
	}
	return trimmed, ""
}

// WidgetTokenMinter mints a chat-scoped API token for an embeddable widget.
// Consumer-side interface: the admin package declares it; the app wiring
// implements it by reusing the exact token-creation logic the REST token
// handler uses (same hashing/storage, chat scope). Returns the raw token,
// shown to the caller once.
type WidgetTokenMinter interface {
	MintChatToken(ctx context.Context, name string) (string, error)
}

// --- provision_agent ---

type provisionAgentTool struct {
	agentRepo  AgentRepository
	schemaRepo SchemaRepository
	reloader   func()
}

// NewProvisionAgentTool wires the one-shot agent provisioning tool. It composes
// existing repos to create a chat-enabled schema, the agent, and the membership
// (entry-agent) binding in a single call so external MCP clients do not have to
// orchestrate three separate admin calls.
func NewProvisionAgentTool(agentRepo AgentRepository, schemaRepo SchemaRepository, reloader func()) tool.InvokableTool {
	return &provisionAgentTool{agentRepo: agentRepo, schemaRepo: schemaRepo, reloader: reloader}
}

func (t *provisionAgentTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "provision_agent",
		Desc: strings.TrimSpace(`
Provisions a ready-to-use chat agent in one call: it creates a chat-enabled schema, the agent, and binds the agent as the schema's entry point so end users can immediately chat with it.

Write a strong system_prompt — it is the single biggest lever on agent quality. A good system_prompt states:
  - ROLE: who the agent is and the domain it serves (e.g. "You are a support assistant for the Acme checkout product").
  - SCOPE: what it should and should not handle; when to defer or escalate.
  - REFUSALS: topics or requests to decline, and how to decline politely.
  - TONE: voice and formatting expectations (concise, friendly, cites sources, etc.).

Recommended follow-ups after provisioning: attach a knowledge base (so the agent answers from your documents) and add tools (search, MCP servers) that let it act. If model_name is omitted the agent uses the deployment's default model when one is available (e.g. a hosted free plan) and can answer immediately; on a self-hosted deployment with no default, configure a model before it can answer.`),
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"name":          {Type: schema.String, Desc: "Unique agent name (lowercase letters, digits, hyphens; must start with a letter).", Required: true},
			"system_prompt": {Type: schema.String, Desc: "System prompt defining role, scope, refusals, and tone. This is the primary quality lever.", Required: true},
			"model_name":    {Type: schema.String, Desc: "Model name to bind. If empty, the agent uses the deployment's default model when one is available (e.g. a hosted free plan) and answers immediately; otherwise it must be configured with a model before it can answer.", Required: false},
			"tools":         {Type: schema.Array, Desc: "Optional array of builtin tool names to grant the agent.", Required: false},
			"schema_name":   {Type: schema.String, Desc: "Chat schema name. Defaults to the agent name.", Required: false},
		}),
	}, nil
}

type provisionAgentArgs struct {
	Name         string   `json:"name"`
	SystemPrompt string   `json:"system_prompt"`
	ModelName    string   `json:"model_name"`
	Tools        []string `json:"tools"`
	SchemaName   string   `json:"schema_name"`
}

type provisionAgentResult struct {
	SchemaName string   `json:"schema_name"`
	SchemaID   string   `json:"schema_id"`
	AgentName  string   `json:"agent_name"`
	AgentID    string   `json:"agent_id"`
	NextSteps  []string `json:"next_steps"`
}

func (t *provisionAgentTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	args, schemaName, errMsg := parseProvisionArgs(argsJSON)
	if errMsg != "" {
		return errMsg, nil
	}

	schemaRec, errMsg := t.createSchema(ctx, args.Name, schemaName)
	if errMsg != "" {
		return errMsg, nil
	}

	agentRec, errMsg := t.createAgent(ctx, args, schemaName)
	if errMsg != "" {
		return errMsg, nil
	}

	agentID, errMsg := t.bindEntryAgent(ctx, args.Name, schemaName, schemaRec, agentRec)
	if errMsg != "" {
		return errMsg, nil
	}

	t.reload()
	slog.InfoContext(ctx, "[ProvisionAgent] provisioned agent",
		"agent", args.Name, "schema", schemaName, "model_bound", args.ModelName != "")

	result := provisionAgentResult{
		SchemaName: schemaName,
		SchemaID:   schemaRec.ID,
		AgentName:  args.Name,
		AgentID:    agentID,
		NextSteps:  buildProvisionNextSteps(schemaName, args.ModelName),
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Sprintf("[ERROR] failed to serialize result: %v", err), nil
	}
	return string(data), nil
}

// parseProvisionArgs validates the raw JSON args; a non-empty errMsg is the
// user-facing rejection (tool-result error, not a protocol error).
func parseProvisionArgs(argsJSON string) (provisionAgentArgs, string, string) {
	var args provisionAgentArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return args, "", fmt.Sprintf("[ERROR] Invalid arguments: %v", err)
	}
	if args.Name == "" {
		return args, "", "[ERROR] name is required"
	}
	if len(args.Name) > 255 || !domain.AgentNameRe.MatchString(args.Name) {
		return args, "", fmt.Sprintf("Invalid agent name %q. Must match ^[a-z][a-z0-9-]* (lowercase letters, digits, hyphens; start with letter; max 255 chars).", args.Name)
	}
	if args.SystemPrompt == "" {
		return args, "", "[ERROR] system_prompt is required"
	}
	if msg := rejectManagementTools(args.Tools); msg != "" {
		return args, "", msg
	}
	schemaName := args.SchemaName
	if schemaName == "" {
		schemaName = args.Name
	}
	return args, schemaName, ""
}

// createSchema mirrors admin_create_schema.
func (t *provisionAgentTool) createSchema(ctx context.Context, agentName, schemaName string) (*SchemaRecord, string) {
	schemaRec := &SchemaRecord{Name: schemaName, Description: fmt.Sprintf("Chat schema for agent %q", agentName)}
	if err := t.schemaRepo.Create(ctx, schemaRec); err != nil {
		if isConflictErr(err) {
			return nil, fmt.Sprintf("Schema with name %q already exists. Choose a different schema_name.", schemaName)
		}
		return nil, fmt.Sprintf("[ERROR] Failed to create schema: %v", err)
	}
	return schemaRec, ""
}

// createAgent mirrors admin_create_agent defaults.
func (t *provisionAgentTool) createAgent(ctx context.Context, args provisionAgentArgs, schemaName string) (*AgentRecord, string) {
	agentRec := &AgentRecord{
		Name:          args.Name,
		SystemPrompt:  args.SystemPrompt,
		ModelName:     args.ModelName,
		Lifecycle:     "persistent",
		ToolExecution: "sequential",
		BuiltinTools:  args.Tools,
	}
	if err := t.agentRepo.Create(ctx, agentRec); err != nil {
		if isConflictErr(err) {
			return nil, fmt.Sprintf("Agent with name %q already exists. Schema %q was created — reuse it or pick a new name.", args.Name, schemaName)
		}
		return nil, fmt.Sprintf("[ERROR] Failed to create agent: %v", err)
	}
	return agentRec, ""
}

// bindEntryAgent sets the new agent as the schema entry point and enables chat
// (mirror admin_update_schema chat_enabled + entry_agent_id path). Returns the
// resolved agent ID.
func (t *provisionAgentTool) bindEntryAgent(ctx context.Context, agentName, schemaName string, schemaRec *SchemaRecord, agentRec *AgentRecord) (string, string) {
	agentID := agentRec.ID
	if agentID == "" {
		// Repo did not echo the new ID — resolve by name so the entry binding still lands.
		if resolved, err := t.agentRepo.GetByName(ctx, agentName); err == nil && resolved != nil {
			agentID = resolved.ID
		}
	}
	if agentID == "" {
		return "", ""
	}

	chatEnabled := true
	entryRef := agentID
	schemaUpdate := &SchemaRecord{
		Name:         schemaName,
		Description:  schemaRec.Description,
		EntryAgentID: &entryRef,
		ChatEnabled:  &chatEnabled,
	}
	if err := t.schemaRepo.Update(ctx, schemaRec.ID, schemaUpdate); err != nil {
		return "", fmt.Sprintf("[ERROR] Agent %q created but failed to enable chat on schema %q: %v", agentName, schemaName, err)
	}
	return agentID, ""
}

func (t *provisionAgentTool) reload() {
	if t.reloader != nil {
		t.reloader()
	}
}

func buildProvisionNextSteps(schemaName, modelName string) []string {
	steps := make([]string, 0, 3)
	if modelName == "" {
		steps = append(steps, "No model bound: this agent uses the deployment's default model when one is available (e.g. a hosted free plan) and can answer immediately. To pin a specific model, use admin_update_agent (model) or admin_set_default_model; on a self-hosted deployment with no default, binding a model is required before it can answer.")
	}
	steps = append(steps,
		"Attach a knowledge base so the agent answers from your documents.",
		fmt.Sprintf("Generate an embed snippet with get_embed_snippet (schema_name=%q) to drop the chat widget onto a site.", schemaName),
	)
	return steps
}

// --- get_embed_snippet ---

type getEmbedSnippetTool struct {
	schemaRepo SchemaRepository
	minter     WidgetTokenMinter
}

// NewGetEmbedSnippetTool wires the embed-snippet tool. It verifies the schema
// exists and is chat-enabled, mints a chat-scoped widget token via the minter,
// and returns the ready-to-paste <script> snippet.
func NewGetEmbedSnippetTool(schemaRepo SchemaRepository, minter WidgetTokenMinter) tool.InvokableTool {
	return &getEmbedSnippetTool{schemaRepo: schemaRepo, minter: minter}
}

func (t *getEmbedSnippetTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "get_embed_snippet",
		Desc: strings.TrimSpace(`
Generates a ready-to-paste HTML embed snippet for a chat schema: a <script> tag pointing at the engine's widget.js with a freshly minted chat-scoped API key. Paste it into any web page to add a chat widget backed by the schema's entry agent.

The schema must be chat-enabled (provision_agent enables chat automatically). The minted key is chat-only and is shown exactly once — store it securely.`),
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"schema_name": {Type: schema.String, Desc: "Name of the chat-enabled schema to embed.", Required: true},
			"endpoint":    {Type: schema.String, Desc: "Public engine base URL (e.g. https://engine.example.com). If omitted, the snippet uses a placeholder you must replace with your engine's public URL.", Required: false},
		}),
	}, nil
}

type getEmbedSnippetArgs struct {
	SchemaName string `json:"schema_name"`
	Endpoint   string `json:"endpoint"`
}

func (t *getEmbedSnippetTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args getEmbedSnippetArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.SchemaName == "" {
		return "[ERROR] schema_name is required", nil
	}

	schemaRec, err := t.findSchemaByName(ctx, args.SchemaName)
	if err != nil {
		return fmt.Sprintf("[ERROR] Failed to look up schema: %v", err), nil
	}
	if schemaRec == nil {
		return fmt.Sprintf("Schema not found: %s", args.SchemaName), nil
	}

	base := "https://YOUR-ENGINE-URL"
	if args.Endpoint != "" {
		validated, msg := validateEndpoint(args.Endpoint)
		if msg != "" {
			return msg, nil
		}
		base = validated
	}

	token, err := t.minter.MintChatToken(ctx, "widget-"+args.SchemaName)
	if err != nil {
		return fmt.Sprintf("[ERROR] Failed to mint widget token: %v", err), nil
	}

	// HTML-attribute-escape every interpolation defensively; the token is
	// server-generated and schema_name is regex-validated at creation, but the
	// snippet is HTML and must not depend on those invariants holding.
	snippet := fmt.Sprintf(`<script src="%s/widget.js"
        data-schema="%s"
        data-api-key="%s">
</script>`, html.EscapeString(base), html.EscapeString(args.SchemaName), html.EscapeString(token))

	var sb strings.Builder
	sb.WriteString(snippet)
	sb.WriteString("\n\n")
	sb.WriteString("WARNING: the data-api-key above is a chat-scoped key shown only once. Store it securely; it cannot be retrieved again (revoke and re-issue if lost).")
	if base == "https://YOUR-ENGINE-URL" {
		sb.WriteString("\n\nReplace https://YOUR-ENGINE-URL with your engine's public base URL before deploying.")
	}
	return sb.String(), nil
}

// findSchemaByName scans the tenant's schemas for a name match. SchemaRepository
// exposes GetByID / List but not GetByName, so we filter List — the schema set
// per tenant is small (config surface, not runtime data).
func (t *getEmbedSnippetTool) findSchemaByName(ctx context.Context, name string) (*SchemaRecord, error) {
	schemas, err := t.schemaRepo.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range schemas {
		if schemas[i].Name == name {
			return &schemas[i], nil
		}
	}
	return nil, nil
}

// isConflictErr reports whether a repo error is a uniqueness/duplicate conflict.
func isConflictErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "unique") ||
		strings.Contains(msg, "UNIQUE") ||
		strings.Contains(msg, "already exists")
}
