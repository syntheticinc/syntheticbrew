package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/authprim"
	deliveryhttp "github.com/syntheticinc/syntheticbrew/internal/delivery/http"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agentregistry"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/audit"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/mcp"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	mcpcatalog "github.com/syntheticinc/syntheticbrew/internal/service/mcp"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// agentCounterHTTPAdapter bridges AgentRegistry to the http.AgentCounter interface.
type agentCounterHTTPAdapter struct {
	registry *agentregistry.AgentRegistry
}

func (a *agentCounterHTTPAdapter) Count() int {
	if a.registry == nil {
		return 0
	}
	return a.registry.Count()
}

// auditHTTPAdapter bridges audit.Logger to the http.AuditLogger interface.
type auditHTTPAdapter struct {
	logger *audit.Logger
}

func (a *auditHTTPAdapter) Log(ctx context.Context, entry deliveryhttp.AuditEntry) error {
	return a.logger.Log(ctx, audit.Entry{
		Timestamp: entry.Timestamp,
		ActorType: entry.ActorType,
		ActorID:   entry.ActorID,
		Action:    entry.Action,
		Resource:  entry.Resource,
		Details:   entry.Details,
		SessionID: entry.SessionID,
	})
}

// agentListerHTTPAdapter bridges AgentRegistry to the http.AgentLister interface.
type agentListerHTTPAdapter struct {
	registry *agentregistry.AgentRegistry
}

func (a *agentListerHTTPAdapter) ListAgents(_ context.Context) ([]deliveryhttp.AgentInfo, error) {
	agents := a.registry.GetAll()
	result := make([]deliveryhttp.AgentInfo, 0, len(agents))
	for _, agent := range agents {
		result = append(result, deliveryhttp.AgentInfo{
			ID:           agent.Record.ID,
			Name:         agent.Record.Name,
			ToolsCount:   len(agent.Record.BuiltinTools) + len(agent.Record.CustomTools),
		})
	}
	return result, nil
}

func (a *agentListerHTTPAdapter) GetAgent(_ context.Context, name string) (*deliveryhttp.AgentDetail, error) {
	agent, err := a.registry.Get(name)
	if err != nil {
		return nil, pkgerrors.NotFound(fmt.Sprintf("agent not found: %s", name))
	}
	rec := agent.Record
	tools := make([]string, 0, len(rec.BuiltinTools)+len(rec.CustomTools))
	tools = append(tools, rec.BuiltinTools...)
	for _, ct := range rec.CustomTools {
		tools = append(tools, ct.Name)
	}
	return &deliveryhttp.AgentDetail{
		AgentInfo: deliveryhttp.AgentInfo{
			ID:           rec.ID,
			Name:         rec.Name,
			ToolsCount:   len(tools),
			IsSystem:     rec.IsSystem,
		},
		ModelID:         rec.ModelID,
		SystemPrompt:    rec.SystemPrompt,
		Tools:           tools,
		CanSpawn:        rec.CanSpawn,
		Lifecycle:       rec.Lifecycle,
		ToolExecution:   rec.ToolExecution,
		MaxSteps:        rec.MaxSteps,
		MaxContextSize:  rec.MaxContextSize,
		MaxTurnDuration: rec.MaxTurnDuration,
		MaxStepDuration: rec.MaxStepDuration,
		Temperature:     rec.Temperature,
		TopP:            rec.TopP,
		MaxTokens:       rec.MaxTokens,
		StopSequences:   rec.StopSequences,
		ConfirmBefore:   rec.ConfirmBefore,
		MCPServers:      rec.MCPServers,
	}, nil
}

// tokenRepoHTTPAdapter bridges GORMAPITokenRepository to the http.TokenRepository interface.
type tokenRepoHTTPAdapter struct {
	repo *configrepo.GORMAPITokenRepository
}

func (a *tokenRepoHTTPAdapter) Create(ctx context.Context, userSub, name, tokenHash string, scopesMask int) (string, error) {
	return a.repo.Create(ctx, userSub, name, tokenHash, scopesMask)
}

func (a *tokenRepoHTTPAdapter) List(ctx context.Context) ([]deliveryhttp.TokenInfo, error) {
	tokens, err := a.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]deliveryhttp.TokenInfo, 0, len(tokens))
	for _, t := range tokens {
		result = append(result, deliveryhttp.TokenInfo{
			ID:         t.ID,
			Name:       t.Name,
			ScopesMask: t.ScopesMask,
			CreatedAt:  t.CreatedAt,
			LastUsedAt: t.LastUsedAt,
		})
	}
	return result, nil
}

func (a *tokenRepoHTTPAdapter) Delete(ctx context.Context, id string) error {
	return a.repo.Delete(ctx, id)
}

func (a *tokenRepoHTTPAdapter) VerifyToken(ctx context.Context, tokenHash string) (deliveryhttp.APITokenInfo, error) {
	v, err := a.repo.VerifyToken(ctx, tokenHash)
	if err != nil {
		return deliveryhttp.APITokenInfo{}, err
	}
	return deliveryhttp.APITokenInfo{
		Name:       v.Name,
		ScopesMask: v.ScopesMask,
		TenantID:   v.TenantID,
	}, nil
}

// widgetTokenMinterAdapter mints chat-scoped API tokens for embed snippets.
// It reuses the exact token-creation primitives the REST token handler uses
// (authprim.Generate + authprim.Hash + the same repo Create), pinned to the
// chat scope so a widget key can only drive chat and nothing else. Tenant and
// actor are stamped from the request context by the repo/authprim layer.
type widgetTokenMinterAdapter struct {
	repo *configrepo.GORMAPITokenRepository
}

func (a *widgetTokenMinterAdapter) MintChatToken(ctx context.Context, name string) (string, error) {
	raw, err := authprim.Generate()
	if err != nil {
		return "", fmt.Errorf("generate widget token: %w", err)
	}
	hash := authprim.Hash(raw)
	userSub := domain.UserSubFromContext(ctx)
	if _, err := a.repo.Create(ctx, userSub, name, hash, deliveryhttp.ScopeChat); err != nil {
		return "", fmt.Errorf("store widget token: %w", err)
	}
	return raw, nil
}

// mcpToolAuditorAdapter appends a per-tool-call audit record for the MCP server
// endpoint. It reuses the shared audit.Logger; tenant/actor are stamped from
// context inside Log.
type mcpToolAuditorAdapter struct {
	logger *audit.Logger
}

func (a *mcpToolAuditorAdapter) RecordToolCall(ctx context.Context, toolName string, isError bool, durationMs int64) {
	if a.logger == nil {
		return
	}
	actorType, _ := ctx.Value(deliveryhttp.ContextKeyActorType).(string)
	actorID, _ := ctx.Value(deliveryhttp.ContextKeyActorID).(string)
	err := a.logger.Log(ctx, audit.Entry{
		ActorType: actorType,
		ActorID:   actorID,
		Action:    "mcp.tool.call",
		Resource:  toolName,
		Details: map[string]interface{}{
			"tool":        toolName,
			"is_error":    isError,
			"duration_ms": durationMs,
		},
	})
	if err != nil {
		slog.ErrorContext(ctx, "mcp server: append tool-call audit failed", "tool", toolName, "error", err)
	}
}

// configReloaderHTTPAdapter bridges AgentRegistry and MCP reconnection to the http.ConfigReloader interface.
type configReloaderHTTPAdapter struct {
	registry        *agentregistry.AgentRegistry
	mcpManager      *mcp.Manager
	db              *gorm.DB
	transportPolicy mcpcatalog.TransportPolicy
}

func (a *configReloaderHTTPAdapter) Reload(ctx context.Context) error {
	if err := a.registry.Reload(ctx); err != nil {
		return err
	}

	a.reconnectMCPServers(ctx)
	return nil
}

func (a *configReloaderHTTPAdapter) reconnectMCPServers(ctx context.Context) {
	if a.mcpManager == nil || a.db == nil {
		return
	}

	tenantID := domain.TenantIDFromContext(ctx)
	if tenantID == "" {
		tenantID = domain.CETenantID
	}

	if err := a.mcpManager.ReconnectTenant(ctx, tenantID); err != nil {
		slog.ErrorContext(ctx, "failed to reconnect MCP servers for tenant", "tenant_id", tenantID, "error", err)
		return
	}

	// Forward-headers cache is refreshed by Manager.ReconnectTenant inside
	// the same code path — no extra step needed here.
	slog.InfoContext(ctx, "MCP servers reconnected after config reload", "tenant_id", tenantID)
}

func (a *configReloaderHTTPAdapter) AgentsCount() int {
	if a.registry == nil {
		return 0
	}
	return a.registry.Count()
}

// mcpProviderAdapter wraps *mcp.Manager so AgentToolResolver sees a tenant-aware
// MCPClientProvider. The adapter routes the per-request ctx (tenant_id baked
// in by middleware) to the matching ClientRegistry; CE single-tenant mode
// always returns the singleton.
type mcpProviderAdapter struct {
	manager *mcp.Manager
}

func newMCPProviderAdapter(m *mcp.Manager) *mcpProviderAdapter {
	return &mcpProviderAdapter{manager: m}
}

func (a *mcpProviderAdapter) GetMCPTools(ctx context.Context, name string) ([]tool.InvokableTool, error) {
	registry, err := a.manager.GetForContext(ctx)
	if err != nil {
		return nil, err
	}
	return registry.GetMCPTools(name)
}

// collectForwardHeaders moved to mcp.CollectForwardHeaders so the Manager
// can refresh per-tenant forward-header lists without an import cycle.

// configImportExportHTTPAdapter and its YAML types live in config_import_export_http_adapter.go.

// auditServiceHTTPAdapter bridges GORMAuditRepository to the http.AuditService interface.
type auditServiceHTTPAdapter struct {
	repo *configrepo.GORMAuditRepository
}

func (a *auditServiceHTTPAdapter) ListAuditLogs(ctx context.Context, actorType, action, resource string, from, to *time.Time, page, perPage int) ([]deliveryhttp.AuditResponse, int64, error) {
	filters := configrepo.AuditFilters{
		ActorType: actorType,
		Action:    action,
		Resource:  resource,
		From:      from,
		To:        to,
	}

	logs, total, err := a.repo.List(ctx, filters, page, perPage)
	if err != nil {
		return nil, 0, err
	}

	result := make([]deliveryhttp.AuditResponse, 0, len(logs))
	for _, l := range logs {
		actorID := ""
		if l.ActorSub != nil {
			actorID = *l.ActorSub
		}
		result = append(result, deliveryhttp.AuditResponse{
			ID:        l.ID,
			Timestamp: l.OccurredAt.Format(time.RFC3339),
			ActorType: l.ActorType,
			ActorID:   actorID,
			Action:    l.Action,
			Resource:  l.Resource,
			Details:   l.Details,
		})
	}
	return result, total, nil
}

// toolCallLogHTTPAdapter bridges ToolCallEventRepository to the http.ToolCallEventQuerier interface.
type toolCallLogHTTPAdapter struct {
	repo *configrepo.ToolCallEventRepository
}

func (a *toolCallLogHTTPAdapter) QueryToolCalls(ctx context.Context, filters deliveryhttp.ToolCallFilters, page, perPage int) ([]deliveryhttp.ToolCallEntry, int64, error) {
	repoFilters := configrepo.ToolCallFilters{
		SessionID: filters.SessionID,
		AgentName: filters.AgentName,
		ToolName:  filters.ToolName,
		Status:    filters.Status,
		UserID:    filters.UserID,
		From:      filters.From,
		To:        filters.To,
	}

	entries, total, err := a.repo.QueryToolCalls(ctx, repoFilters, page, perPage)
	if err != nil {
		return nil, 0, err
	}

	result := make([]deliveryhttp.ToolCallEntry, 0, len(entries))
	for _, e := range entries {
		result = append(result, deliveryhttp.ToolCallEntry{
			ID:         e.ID,
			SessionID:  e.SessionID,
			AgentName:  e.AgentName,
			ToolName:   e.ToolName,
			Input:      e.Input,
			Output:     e.Output,
			Status:     e.Status,
			DurationMs: e.DurationMs,
			UserID:     e.UserID,
			CreatedAt:  e.CreatedAt,
		})
	}
	return result, total, nil
}

// agentSchemaIDResolver resolves the primary schema UUID for an agent.
// BUG-007: memory/knowledge tools need schema_id (UUID) to scope data.
type agentSchemaIDResolver struct {
	db *gorm.DB
}

func (r *agentSchemaIDResolver) ResolveSchemaID(ctx context.Context, agentName string) (string, error) {
	// Q.5: agent_relations uses agent UUIDs. Resolve name → id first,
	// then find the schema where this agent participates.
	//
	// A single-agent schema has no relations, so also treat an entry_agent_id
	// match on schemas as membership — otherwise memory/knowledge tools stay
	// disabled for solo entry agents.
	var agentID string
	if err := r.db.WithContext(ctx).Raw(
		"SELECT id FROM agents WHERE name = ? LIMIT 1", agentName).Scan(&agentID).Error; err != nil || agentID == "" {
		return "", fmt.Errorf("no schema for agent %q", agentName)
	}
	var schemaID string
	if err := r.db.WithContext(ctx).Raw(
		`SELECT schema_id FROM agent_relations
			WHERE source_agent_id = ? OR target_agent_id = ?
			LIMIT 1`, agentID, agentID).Scan(&schemaID).Error; err == nil && schemaID != "" {
		return schemaID, nil
	}
	if err := r.db.WithContext(ctx).Raw(
		"SELECT id FROM schemas WHERE entry_agent_id = ? LIMIT 1", agentID).Scan(&schemaID).Error; err != nil || schemaID == "" {
		return "", fmt.Errorf("no schema for agent %q", agentName)
	}
	return schemaID, nil
}

// taskServiceHTTPAdapter and its helpers live in task_http_adapter.go.

// knowledgeStatsHTTPAdapter bridges GORMKnowledgeRepository to the http.KnowledgeStats interface.
// Resolves agent name → linked KB IDs, then aggregates stats across KBs.
type knowledgeStatsHTTPAdapter struct {
	repo   *configrepo.GORMKnowledgeRepository
	kbRepo *configrepo.GORMKnowledgeBaseRepository
}

func (a *knowledgeStatsHTTPAdapter) GetStats(ctx context.Context, agentName string) (int, int, *time.Time, error) {
	if a.kbRepo == nil {
		return 0, 0, nil, nil
	}
	kbIDs, err := a.kbRepo.ListKBsByAgentName(ctx, agentName)
	if err != nil || len(kbIDs) == 0 {
		return 0, 0, nil, nil
	}
	return a.repo.GetStatsByKBs(ctx, kbIDs)
}

// chatServiceHTTPAdapter and chatTriggerCheckerAdapter live in chat_http_adapter.go.
