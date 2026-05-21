package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"gorm.io/gorm"

	deliveryhttp "github.com/syntheticinc/syntheticbrew/internal/delivery/http"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agentregistry"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// agentManagerHTTPAdapter bridges GORMAgentRepository + AgentRegistry to the
// http.AgentManager interface.
//
// registryMgr (when non-nil) owns the per-tenant registry cache; writes here
// must invalidate it so reads on the same tenant see fresh data.
type agentManagerHTTPAdapter struct {
	repo        *configrepo.GORMAgentRepository
	registry    *agentregistry.AgentRegistry
	registryMgr *agentregistry.Manager
	db          *gorm.DB
	schemaRepo  *configrepo.GORMSchemaRepository
	// kbRepo is used to apply knowledge_base_ids changes during agent
	// Create/Update/Patch (Bug 7). When nil, the knowledge_base_ids
	// field is silently dropped — this matches the legacy behaviour so
	// callers that don't wire kbRepo (none in production) keep working.
	kbRepo *configrepo.GORMKnowledgeBaseRepository
}

// invalidateRegistryForContext refreshes cached agent registries so that the
// next lookup sees the write we just committed.
//
// Ordering: we invalidate FIRST, reload SECOND. That way:
//   - multi-tenant mode: the per-tenant registry is dropped and will lazily
//     reload on the next request for that tenant;
//   - single-tenant mode: the eager singleton is reloaded immediately so that
//     the freshly-seeded agent is visible to the in-process agent pool.
func (a *agentManagerHTTPAdapter) invalidateRegistryForContext(ctx context.Context) {
	if a.registryMgr != nil {
		if tid := domain.TenantIDFromContext(ctx); tid != "" {
			a.registryMgr.InvalidateTenant(tid)
		} else {
			// CE / no-tenant path: InvalidateAll reloads the singleton.
			a.registryMgr.InvalidateAll()
		}
	}
	if a.registry != nil {
		if err := a.registry.Reload(ctx); err != nil {
			slog.ErrorContext(ctx, "failed to reload agent registry", "error", err)
		}
	}
}

func (a *agentManagerHTTPAdapter) ListAgents(ctx context.Context) ([]deliveryhttp.AgentInfo, error) {
	records, err := a.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}

	result := make([]deliveryhttp.AgentInfo, 0, len(records))
	for _, rec := range records {
		info := deliveryhttp.AgentInfo{
			ID:           rec.ID,
			Name:         rec.Name,
			ToolsCount:   len(rec.BuiltinTools) + len(rec.CustomTools),
			IsSystem:     rec.IsSystem,
		}
		if a.schemaRepo != nil {
			schemaNames, _ := a.schemaRepo.ListSchemasForAgent(ctx, rec.Name)
			info.UsedInSchemas = schemaNames
		}
		result = append(result, info)
	}
	return result, nil
}

// resolveAgentName translates an agent reference (UUID or name) into the
// canonical name expected by repo.GetByName. Mirrors resolveSchemaRef
// (http_adapters_extra.go) with explicit tenant scoping so cross-tenant
// UUID probes return NotFound instead of leaking existence.
func (a *agentManagerHTTPAdapter) resolveAgentName(ctx context.Context, ref string) (string, error) {
	if ref == "" || !isUUID(ref) {
		return ref, nil
	}
	tenantID := domain.TenantIDFromContext(ctx)
	if tenantID == "" {
		tenantID = domain.CETenantID
	}
	var name string
	err := a.db.WithContext(ctx).
		Raw("SELECT name FROM agents WHERE id = ? AND tenant_id = ? LIMIT 1", ref, tenantID).
		Scan(&name).Error
	if err != nil || name == "" {
		return "", pkgerrors.NotFound(fmt.Sprintf("agent not found: %s", ref))
	}
	return name, nil
}

func (a *agentManagerHTTPAdapter) GetAgent(ctx context.Context, name string) (*deliveryhttp.AgentDetail, error) {
	name, err := a.resolveAgentName(ctx, name)
	if err != nil {
		return nil, err
	}
	rec, err := a.repo.GetByName(ctx, name)
	if err != nil {
		return nil, pkgerrors.NotFound(fmt.Sprintf("agent not found: %s", name))
	}

	tools := make([]string, 0, len(rec.BuiltinTools)+len(rec.CustomTools))
	tools = append(tools, rec.BuiltinTools...)
	for _, ct := range rec.CustomTools {
		tools = append(tools, ct.Name)
	}

	detail := &deliveryhttp.AgentDetail{
		AgentInfo: deliveryhttp.AgentInfo{
			ID:           rec.ID,
			Name:         rec.Name,
			ToolsCount:   len(tools),
			IsSystem:     rec.IsSystem,
		},
		SystemPrompt:    rec.SystemPrompt,
		Tools:           tools,
		CanSpawn:        rec.CanSpawn,
		Lifecycle:       rec.Lifecycle,
		ToolExecution:   rec.ToolExecution,
		MaxSteps:        rec.MaxSteps,
		MaxContextSize:  rec.MaxContextSize,
		MaxTurnDuration: rec.MaxTurnDuration,
		Temperature:     rec.Temperature,
		TopP:            rec.TopP,
		MaxTokens:       rec.MaxTokens,
		StopSequences:   rec.StopSequences,
		ConfirmBefore:   rec.ConfirmBefore,
		MCPServers:      rec.MCPServers,
	}

	mcpNames, err := a.loadMCPServersForAgent(ctx, name)
	if err != nil {
		slog.WarnContext(ctx, "load mcp servers for agent", "agent", name, "error", err)
	} else {
		detail.MCPServers = mcpNames
	}

	// Resolve model ID for the response.
	detail.ModelID = a.resolveModelID(ctx, rec.ModelName)

	// Populate used_in_schemas (AC-ENT-03)
	if a.schemaRepo != nil {
		schemaNames, _ := a.schemaRepo.ListSchemasForAgent(ctx, name)
		detail.UsedInSchemas = schemaNames
	}

	return detail, nil
}

// resolveAgentModel resolves req.ModelID using (in order):
//  1. explicit ModelID — rejected when it points at a non-chat model;
//  2. by-name lookup via req.Model — 400 if name doesn't exist in tenant;
//  3. tenant's default chat model — 400 if no default and nothing was supplied.
//
// Closes the silent-default loophole that masked unbound agents until first
// chat (F16). Mutates req.ModelID in place.
func (a *agentManagerHTTPAdapter) resolveAgentModel(ctx context.Context, req *deliveryhttp.CreateAgentRequest) error {
	if req.ModelID != nil && *req.ModelID != "" {
		var llm models.LLMProviderModel
		if err := a.db.WithContext(ctx).Where("id = ?", *req.ModelID).First(&llm).Error; err == nil && llm.Kind == "embedding" {
			return pkgerrors.InvalidInput("model_id must reference a chat model, got kind=embedding")
		}
		return nil
	}

	tenantID := domain.TenantIDFromContext(ctx)
	if tenantID == "" {
		tenantID = domain.CETenantID
	}

	if req.Model != "" {
		var named models.LLMProviderModel
		err := a.db.WithContext(ctx).
			Where("tenant_id = ? AND name = ? AND kind = ?", tenantID, req.Model, "chat").
			First(&named).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return pkgerrors.InvalidInput(fmt.Sprintf("model not found: %q (must be the name of an existing chat model in this tenant)", req.Model))
		}
		if err != nil {
			return fmt.Errorf("resolve model by name: %w", err)
		}
		id := named.ID
		req.ModelID = &id
		return nil
	}

	var def models.LLMProviderModel
	err := a.db.WithContext(ctx).
		Where("tenant_id = ? AND is_default = ? AND kind = ?", tenantID, true, "chat").
		First(&def).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return pkgerrors.InvalidInput("model is required: tenant has no default chat model, pass model_id or model")
	}
	if err != nil {
		return fmt.Errorf("resolve default model: %w", err)
	}
	id := def.ID
	req.ModelID = &id
	slog.InfoContext(ctx, "agent inherits tenant default chat model", "agent", req.Name, "model", def.Name)
	return nil
}

func (a *agentManagerHTTPAdapter) CreateAgent(ctx context.Context, req deliveryhttp.CreateAgentRequest) (*deliveryhttp.AgentDetail, error) {
	if err := a.resolveAgentModel(ctx, &req); err != nil {
		return nil, err
	}

	record := a.toAgentRecord(req)
	if err := a.repo.Create(ctx, record); err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") || strings.Contains(err.Error(), "UNIQUE constraint") {
			return nil, pkgerrors.AlreadyExists(fmt.Sprintf("agent with name %q already exists", req.Name))
		}
		return nil, fmt.Errorf("create agent: %w", err)
	}

	// nil KnowledgeBaseIDs leaves links untouched; empty slice clears them.
	if req.KnowledgeBaseIDs != nil && a.kbRepo != nil {
		created, err := a.repo.GetByName(ctx, req.Name)
		if err != nil || created == nil {
			return nil, fmt.Errorf("load created agent for kb link: %w", err)
		}
		if err := a.kbRepo.ReplaceAgentKBs(ctx, created.ID, req.KnowledgeBaseIDs); err != nil {
			if errors.Is(err, configrepo.ErrAgentNotInTenant) {
				return nil, pkgerrors.NotFound(fmt.Sprintf("agent not found: %s", req.Name))
			}
			if errors.Is(err, configrepo.ErrKBsNotInTenant) {
				return nil, pkgerrors.NotFound("one or more knowledge bases not found")
			}
			return nil, fmt.Errorf("apply knowledge_base_ids: %w", err)
		}
	}

	a.invalidateRegistryForContext(ctx)

	return a.GetAgent(ctx, req.Name)
}

func (a *agentManagerHTTPAdapter) UpdateAgent(ctx context.Context, name string, req deliveryhttp.CreateAgentRequest) (*deliveryhttp.AgentDetail, error) {
	name, err := a.resolveAgentName(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := a.resolveAgentModel(ctx, &req); err != nil {
		return nil, err
	}

	record := a.toAgentRecord(req)

	// Preserve is_system and builtin tools from the existing record.
	// is_system is not settable via HTTP.
	// For system agents: if the request doesn't specify tools, preserve existing builtin tools
	// to prevent accidental tool erasure during model/prompt updates.
	if existing, err := a.repo.GetByName(ctx, name); err == nil && existing != nil {
		record.IsSystem = existing.IsSystem
		if existing.IsSystem && len(record.BuiltinTools) == 0 && len(existing.BuiltinTools) > 0 {
			record.BuiltinTools = existing.BuiltinTools
		}
		if !existing.IsSystem {
			for _, toolName := range record.BuiltinTools {
				if strings.HasPrefix(toolName, "admin_") {
					return nil, pkgerrors.InvalidInput("admin tools are reserved for system agents")
				}
			}
		}
	}

	if err := a.repo.Update(ctx, name, record); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, pkgerrors.NotFound(fmt.Sprintf("agent not found: %s", name))
		}
		return nil, fmt.Errorf("update agent: %w", err)
	}

	// Use the updated name (could have been renamed).
	lookupName := req.Name
	if lookupName == "" {
		lookupName = name
	}

	// nil KnowledgeBaseIDs leaves links untouched; empty slice clears them.
	if req.KnowledgeBaseIDs != nil && a.kbRepo != nil {
		updated, err := a.repo.GetByName(ctx, lookupName)
		if err != nil || updated == nil {
			return nil, fmt.Errorf("load updated agent for kb link: %w", err)
		}
		if err := a.kbRepo.ReplaceAgentKBs(ctx, updated.ID, req.KnowledgeBaseIDs); err != nil {
			if errors.Is(err, configrepo.ErrAgentNotInTenant) {
				return nil, pkgerrors.NotFound(fmt.Sprintf("agent not found: %s", lookupName))
			}
			if errors.Is(err, configrepo.ErrKBsNotInTenant) {
				return nil, pkgerrors.NotFound("one or more knowledge bases not found")
			}
			return nil, fmt.Errorf("apply knowledge_base_ids: %w", err)
		}
	}

	a.invalidateRegistryForContext(ctx)

	return a.GetAgent(ctx, lookupName)
}

// PatchAgent applies only the non-nil fields in req; unspecified fields are preserved.
func (a *agentManagerHTTPAdapter) PatchAgent(ctx context.Context, name string, req deliveryhttp.UpdateAgentRequest) (*deliveryhttp.AgentDetail, error) {
	name, err := a.resolveAgentName(ctx, name)
	if err != nil {
		return nil, err
	}
	existing, err := a.repo.GetByName(ctx, name)
	if err != nil || existing == nil {
		return nil, pkgerrors.NotFound(fmt.Sprintf("agent not found: %s", name))
	}

	// Resolve model_id if provided (accepts UUID or name).
	// Wave 5: model_id must reference a chat model, not an embedding model.
	if req.ModelID != nil && *req.ModelID != "" {
		var llm models.LLMProviderModel
		if isUUID(*req.ModelID) {
			if err := a.db.Where("id = ?", *req.ModelID).First(&llm).Error; err == nil {
				if llm.Kind == "embedding" {
					return nil, pkgerrors.InvalidInput(fmt.Sprintf("model_id must reference a chat model, got kind=embedding"))
				}
				existing.ModelID = req.ModelID
				existing.ModelName = llm.Name
			} else {
				return nil, pkgerrors.NotFound(fmt.Sprintf("model not found: %s", *req.ModelID))
			}
		} else {
			// Treat as name.
			if err := a.db.Where("name = ?", *req.ModelID).First(&llm).Error; err == nil {
				if llm.Kind == "embedding" {
					return nil, pkgerrors.InvalidInput(fmt.Sprintf("model_id must reference a chat model, got kind=embedding"))
				}
				id := llm.ID
				existing.ModelID = &id
				existing.ModelName = llm.Name
			} else {
				return nil, pkgerrors.NotFound(fmt.Sprintf("model not found: %s", *req.ModelID))
			}
		}
	}

	// Apply only non-nil fields.
	if req.SystemPrompt != nil {
		existing.SystemPrompt = *req.SystemPrompt
	}
	if req.Lifecycle != nil {
		existing.Lifecycle = *req.Lifecycle
	}
	if req.ToolExecution != nil {
		existing.ToolExecution = *req.ToolExecution
	}
	if req.MaxSteps != nil {
		existing.MaxSteps = *req.MaxSteps
	}
	if req.MaxContextSize != nil {
		existing.MaxContextSize = *req.MaxContextSize
	}
	if req.MaxTurnDuration != nil {
		existing.MaxTurnDuration = *req.MaxTurnDuration
	}
	if req.Temperature != nil {
		existing.Temperature = req.Temperature
	}
	if req.TopP != nil {
		existing.TopP = req.TopP
	}
	if req.MaxTokens != nil {
		existing.MaxTokens = req.MaxTokens
	}
	if req.StopSequences != nil {
		existing.StopSequences = *req.StopSequences
	}
	if req.ConfirmBefore != nil {
		existing.ConfirmBefore = *req.ConfirmBefore
	}
	if req.Tools != nil {
		existing.BuiltinTools = *req.Tools
	}
	if req.CanSpawn != nil {
		existing.CanSpawn = *req.CanSpawn
	}
	if req.MCPServers != nil {
		existing.MCPServers = *req.MCPServers
	}

	if err := a.repo.Update(ctx, name, existing); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, pkgerrors.NotFound(fmt.Sprintf("agent not found: %s", name))
		}
		return nil, fmt.Errorf("patch agent: %w", err)
	}

	// nil KnowledgeBaseIDs leaves links untouched; empty slice clears them.
	if req.KnowledgeBaseIDs != nil && a.kbRepo != nil {
		if err := a.kbRepo.ReplaceAgentKBs(ctx, existing.ID, *req.KnowledgeBaseIDs); err != nil {
			if errors.Is(err, configrepo.ErrAgentNotInTenant) {
				return nil, pkgerrors.NotFound(fmt.Sprintf("agent not found: %s", name))
			}
			if errors.Is(err, configrepo.ErrKBsNotInTenant) {
				return nil, pkgerrors.NotFound("one or more knowledge bases not found")
			}
			return nil, fmt.Errorf("apply knowledge_base_ids: %w", err)
		}
	}

	a.invalidateRegistryForContext(ctx)
	return a.GetAgent(ctx, name)
}

func (a *agentManagerHTTPAdapter) DeleteAgent(ctx context.Context, name string) error {
	name, err := a.resolveAgentName(ctx, name)
	if err != nil {
		return err
	}
	// System agents cannot be deleted via API.
	existing, err := a.repo.GetByName(ctx, name)
	if err == nil && existing != nil && existing.IsSystem {
		return pkgerrors.Forbidden(fmt.Sprintf("system agent %q cannot be deleted", name))
	}

	// Drop capabilities first to satisfy FK; triggers and agent_relations are
	// schema-owned and cascade with the schema, not the agent.
	if err := a.db.WithContext(ctx).
		Where("agent_id IN (SELECT id FROM agents WHERE name = ?)", name).
		Delete(&models.CapabilityModel{}).Error; err != nil {
		slog.WarnContext(ctx, "failed to cascade-delete capabilities", "agent", name, "error", err)
	}

	// Clear schemas.entry_agent_id that reference this agent so the FK does
	// not block deletion. The schema itself remains so the admin can reassign.
	if err := a.db.WithContext(ctx).Exec(
		"UPDATE schemas SET entry_agent_id = NULL WHERE entry_agent_id IN (SELECT id FROM agents WHERE name = ?)", name).Error; err != nil {
		slog.WarnContext(ctx, "failed to clear schema entry_agent references", "agent", name, "error", err)
	}

	// Drop any agent_relations that reference this agent (source or target).
	if err := a.db.WithContext(ctx).Exec(
		`DELETE FROM agent_relations
			WHERE source_agent_id IN (SELECT id FROM agents WHERE name = ?)
			   OR target_agent_id IN (SELECT id FROM agents WHERE name = ?)`, name, name).Error; err != nil {
		slog.WarnContext(ctx, "failed to cascade-delete agent relations", "agent", name, "error", err)
	}

	if err := a.repo.Delete(ctx, name); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return pkgerrors.NotFound(fmt.Sprintf("agent not found: %s", name))
		}
		return fmt.Errorf("delete agent: %w", err)
	}

	a.invalidateRegistryForContext(ctx)

	return nil
}

func (a *agentManagerHTTPAdapter) toAgentRecord(req deliveryhttp.CreateAgentRequest) *configrepo.AgentRecord {
	rec := &configrepo.AgentRecord{
		Name:            req.Name,
		SystemPrompt:    req.SystemPrompt,
		Lifecycle:       req.Lifecycle,
		ToolExecution:   req.ToolExecution,
		MaxSteps:        req.MaxSteps,
		MaxContextSize:  req.MaxContextSize,
		MaxTurnDuration: req.MaxTurnDuration,
		Temperature:     req.Temperature,
		TopP:            req.TopP,
		MaxTokens:       req.MaxTokens,
		StopSequences:   req.StopSequences,
		ConfirmBefore:   req.ConfirmBefore,
		BuiltinTools:    req.Tools,
		CanSpawn:        req.CanSpawn,
		MCPServers:      req.MCPServers,
	}

	// Resolve model: by ID or by name.
	// Note: kind validation is done before toAgentRecord is called (in Create/UpdateAgent).
	if req.ModelID != nil {
		rec.ModelID = req.ModelID
		var llm models.LLMProviderModel
		if err := a.db.Where("id = ?", *req.ModelID).First(&llm).Error; err == nil {
			rec.ModelName = llm.Name
		}
	} else if req.Model != "" {
		rec.ModelName = req.Model
	}

	// Apply defaults.
	if rec.Lifecycle == "" {
		rec.Lifecycle = "persistent"
	}
	if rec.ToolExecution == "" {
		rec.ToolExecution = "sequential"
	}
	if rec.MaxSteps == 0 {
		rec.MaxSteps = 50
	}
	if rec.MaxContextSize == 0 {
		rec.MaxContextSize = 16000
	}
	if rec.MaxTurnDuration == 0 {
		rec.MaxTurnDuration = 120
	}

	return rec
}

func (a *agentManagerHTTPAdapter) loadMCPServersForAgent(_ context.Context, name string) ([]string, error) {
	var agent models.AgentModel
	if err := a.db.Where("name = ?", name).First(&agent).Error; err != nil {
		return nil, err
	}

	var agentMCPs []models.AgentMCPServer
	if err := a.db.Preload("MCPServer").Where("agent_id = ?", agent.ID).Find(&agentMCPs).Error; err != nil {
		return nil, err
	}

	names := make([]string, 0, len(agentMCPs))
	for _, am := range agentMCPs {
		names = append(names, am.MCPServer.Name)
	}
	return names, nil
}

func (a *agentManagerHTTPAdapter) resolveModelID(_ context.Context, modelName string) *string {
	if modelName == "" {
		return nil
	}
	var llm models.LLMProviderModel
	if err := a.db.Where("name = ?", modelName).First(&llm).Error; err != nil {
		return nil
	}
	return &llm.ID
}
