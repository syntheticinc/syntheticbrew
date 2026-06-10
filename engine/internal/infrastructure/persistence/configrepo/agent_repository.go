package configrepo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"gorm.io/gorm"
)

// AgentRecord is an intermediate struct for DB <-> domain mapping.
// Contains all agent config from DB (agent + tools + MCP).
// CanSpawn is derived from agent_relations (V2).
type AgentRecord struct {
	ID              string
	Name            string
	ModelID         *string
	ModelName       string
	SystemPrompt    string
	Lifecycle       string
	ToolExecution   string
	MaxSteps        int
	MaxContextSize  int
	MaxTurnDuration int
	MaxStepDuration int
	Temperature     *float64
	TopP            *float64
	MaxTokens       *int
	StopSequences   []string
	ConfirmBefore   []string
	BuiltinTools    []string
	CustomTools     []CustomToolRecord
	MCPServers      []string
	CanSpawn        []string
	IsSystem        bool
}

// CustomToolRecord holds a custom tool name and its JSON config.
type CustomToolRecord struct {
	Name   string
	Config string
}

// GORMAgentRepository implements AgentReader and AgentWriter using GORM.
type GORMAgentRepository struct {
	db *gorm.DB
}

// NewGORMAgentRepository creates a new GORMAgentRepository.
func NewGORMAgentRepository(db *gorm.DB) *GORMAgentRepository {
	return &GORMAgentRepository{db: db}
}

// List returns all agent records for the tenant with all associations preloaded.
func (r *GORMAgentRepository) List(ctx context.Context) ([]AgentRecord, error) {
	var agents []models.AgentModel
	err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Preload("Tools").
		Preload("Model").
		Find(&agents).Error
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}

	// Load MCP server names for all agents in one query
	mcpByAgent, err := r.loadAllAgentMCPServers(ctx)
	if err != nil {
		return nil, fmt.Errorf("load mcp servers: %w", err)
	}

	// Load CanSpawn from agent_relations for all agents
	spawnByAgent, err := r.loadAllCanSpawn(ctx)
	if err != nil {
		return nil, fmt.Errorf("load can_spawn: %w", err)
	}

	records := make([]AgentRecord, 0, len(agents))
	for _, a := range agents {
		rec, err := toAgentRecord(a)
		if err != nil {
			return nil, fmt.Errorf("convert agent %q: %w", a.Name, err)
		}
		rec.MCPServers = mcpByAgent[a.ID]
		rec.CanSpawn = spawnByAgent[a.Name]
		records = append(records, rec)
	}
	return records, nil
}

// GetByName returns a single agent record by name, scoped to the tenant.
func (r *GORMAgentRepository) GetByName(ctx context.Context, name string) (*AgentRecord, error) {
	var agent models.AgentModel
	err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Preload("Tools").
		Preload("Model").
		Where("name = ?", name).
		First(&agent).Error
	if err != nil {
		return nil, fmt.Errorf("get agent %q: %w", name, err)
	}

	rec, err := toAgentRecord(agent)
	if err != nil {
		return nil, fmt.Errorf("convert agent %q: %w", name, err)
	}

	// Load MCP server names separately (GORM many2many infers wrong column names)
	mcpNames, err := r.loadMCPServersForAgent(ctx, agent.ID)
	if err != nil {
		return nil, fmt.Errorf("load mcp servers for agent %q: %w", name, err)
	}
	rec.MCPServers = mcpNames

	// Load CanSpawn from agent_relations
	rec.CanSpawn = r.loadCanSpawnForAgent(ctx, agent.Name)

	return &rec, nil
}

// Count returns the number of agents in the database for the current tenant.
func (r *GORMAgentRepository) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Model(&models.AgentModel{}).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count agents: %w", err)
	}
	return count, nil
}

// Create inserts a new agent with all associations, stamped with tenant from context.
func (r *GORMAgentRepository) Create(ctx context.Context, record *AgentRecord) error {
	tenantID := tenantIDFromCtx(ctx)

	agent, err := r.toAgentModel(ctx, record)
	if err != nil {
		return fmt.Errorf("build agent model: %w", err)
	}
	agent.TenantID = tenantID
	// Propagate tenant into owned child rows (tools) before the cascade insert.
	for i := range agent.Tools {
		agent.Tools[i].TenantID = tenantID
	}

	if err := r.db.WithContext(ctx).Create(&agent).Error; err != nil {
		return fmt.Errorf("create agent %q: %w", record.Name, err)
	}

	if err := r.createMCPAssociations(ctx, agent.ID, record.MCPServers); err != nil {
		return fmt.Errorf("create mcp associations: %w", err)
	}

	return nil
}

// Update replaces the agent record identified by name (tenant-scoped lookup).
func (r *GORMAgentRepository) Update(ctx context.Context, name string, record *AgentRecord) error {
	tenantID := tenantIDFromCtx(ctx)

	var existing models.AgentModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("name = ?", name).
		First(&existing).Error; err != nil {
		return fmt.Errorf("find agent %q: %w", name, err)
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Delete old associations (tenant-scoped).
		if err := tx.
			Where("agent_id = ? AND tenant_id = ?", existing.ID, tenantID).
			Delete(&models.AgentToolModel{}).Error; err != nil {
			return fmt.Errorf("delete old tools: %w", err)
		}
		if err := tx.Exec(
			"DELETE FROM agent_mcp_servers WHERE agent_id = ? AND tenant_id = ?",
			existing.ID, tenantID,
		).Error; err != nil {
			return fmt.Errorf("delete old mcp associations: %w", err)
		}

		// Build updated model
		agent, err := r.toAgentModelWithTx(tx, record)
		if err != nil {
			return fmt.Errorf("build agent model: %w", err)
		}

		// Update scalar fields
		updates := map[string]interface{}{
			"name":              agent.Name,
			"model_id":          agent.ModelID,
			"system_prompt":     agent.SystemPrompt,
			"lifecycle":         agent.Lifecycle,
			"tool_execution":    agent.ToolExecution,
			"max_steps":         agent.MaxSteps,
			"max_context_size":  agent.MaxContextSize,
			"max_turn_duration": agent.MaxTurnDuration,
			"max_step_duration": agent.MaxStepDuration,
			"temperature":       agent.Temperature,
			"top_p":             agent.TopP,
			"max_tokens":        agent.MaxTokens,
			"stop_sequences":    agent.StopSequences,
			"confirm_before":    agent.ConfirmBefore,
			"is_system":         agent.IsSystem,
		}
		if err := tx.Model(&models.AgentModel{}).
			Where("id = ? AND tenant_id = ?", existing.ID, tenantID).
			Updates(updates).Error; err != nil {
			return fmt.Errorf("update agent %q: %w", name, err)
		}

		// Recreate associations with existing ID + tenant.
		for i := range agent.Tools {
			agent.Tools[i].AgentID = existing.ID
			agent.Tools[i].TenantID = tenantID
		}
		if len(agent.Tools) > 0 {
			if err := tx.Create(&agent.Tools).Error; err != nil {
				return fmt.Errorf("create tools: %w", err)
			}
		}

		if err := r.createMCPAssociationsWithTx(tx, existing.ID, record.MCPServers); err != nil {
			return fmt.Errorf("create mcp associations: %w", err)
		}

		return nil
	})
}

// Delete removes an agent and all its associations by name (tenant-scoped).
func (r *GORMAgentRepository) Delete(ctx context.Context, name string) error {
	tenantID := tenantIDFromCtx(ctx)

	var agent models.AgentModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("name = ?", name).
		First(&agent).Error; err != nil {
		return fmt.Errorf("find agent %q: %w", name, err)
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.
			Where("agent_id = ? AND tenant_id = ?", agent.ID, tenantID).
			Delete(&models.AgentToolModel{}).Error; err != nil {
			return fmt.Errorf("delete tools: %w", err)
		}
		if err := tx.Exec(
			"DELETE FROM agent_mcp_servers WHERE agent_id = ? AND tenant_id = ?",
			agent.ID, tenantID,
		).Error; err != nil {
			return fmt.Errorf("delete mcp associations: %w", err)
		}
		if err := tx.
			Where("tenant_id = ?", tenantID).
			Delete(&agent).Error; err != nil {
			return fmt.Errorf("delete agent %q: %w", name, err)
		}
		return nil
	})
}

// toAgentRecord converts AgentModel to AgentRecord.
func toAgentRecord(a models.AgentModel) (AgentRecord, error) {
	rec := AgentRecord{
		ID:              a.ID,
		Name:            a.Name,
		SystemPrompt:    a.SystemPrompt,
		Lifecycle:       a.Lifecycle,
		ToolExecution:   a.ToolExecution,
		MaxSteps:        a.MaxSteps,
		MaxContextSize:  a.MaxContextSize,
		MaxTurnDuration: a.MaxTurnDuration,
		MaxStepDuration: a.MaxStepDuration,
		Temperature:     a.Temperature,
		TopP:            a.TopP,
		MaxTokens:       a.MaxTokens,
		IsSystem:        a.IsSystem,
	}

	// StopSequences: JSON array -> []string
	if a.StopSequences != nil && *a.StopSequences != "" {
		if err := json.Unmarshal([]byte(*a.StopSequences), &rec.StopSequences); err != nil {
			return AgentRecord{}, fmt.Errorf("parse stop_sequences: %w", err)
		}
	}

	// Model ID and name
	rec.ModelID = a.ModelID
	if a.Model != nil {
		rec.ModelName = a.Model.Name
	}

	// ConfirmBefore: JSON array -> []string
	if a.ConfirmBefore != nil && *a.ConfirmBefore != "" {
		if err := json.Unmarshal([]byte(*a.ConfirmBefore), &rec.ConfirmBefore); err != nil {
			return AgentRecord{}, fmt.Errorf("parse confirm_before: %w", err)
		}
	}

	// Tools: split by type
	for _, t := range a.Tools {
		switch t.ToolType {
		case "builtin":
			rec.BuiltinTools = append(rec.BuiltinTools, t.ToolName)
		case "custom":
			config := ""
			if t.Config != nil {
				config = *t.Config
			}
			rec.CustomTools = append(rec.CustomTools, CustomToolRecord{
				Name:   t.ToolName,
				Config: config,
			})
		}
	}

	// CanSpawn + MCP servers: loaded separately via agent_relations / agent_mcp_servers queries.

	return rec, nil
}

// toAgentModel converts AgentRecord to AgentModel (for Create).
func (r *GORMAgentRepository) toAgentModel(ctx context.Context, rec *AgentRecord) (models.AgentModel, error) {
	return r.toAgentModelWithDB(r.db.WithContext(ctx), rec)
}

// toAgentModelWithTx converts AgentRecord to AgentModel using a transaction.
func (r *GORMAgentRepository) toAgentModelWithTx(tx *gorm.DB, rec *AgentRecord) (models.AgentModel, error) {
	return r.toAgentModelWithDB(tx, rec)
}

func (r *GORMAgentRepository) toAgentModelWithDB(db *gorm.DB, rec *AgentRecord) (models.AgentModel, error) {
	agent := models.AgentModel{
		Name:            rec.Name,
		SystemPrompt:    rec.SystemPrompt,
		Lifecycle:       rec.Lifecycle,
		ToolExecution:   rec.ToolExecution,
		MaxSteps:        rec.MaxSteps,
		MaxContextSize:  rec.MaxContextSize,
		MaxTurnDuration: rec.MaxTurnDuration,
		MaxStepDuration: rec.MaxStepDuration,
		Temperature:     rec.Temperature,
		TopP:            rec.TopP,
		MaxTokens:       rec.MaxTokens,
		IsSystem:        rec.IsSystem,
	}

	// StopSequences: []string -> JSON string
	if len(rec.StopSequences) > 0 {
		data, err := json.Marshal(rec.StopSequences)
		if err != nil {
			return models.AgentModel{}, fmt.Errorf("marshal stop_sequences: %w", err)
		}
		s := string(data)
		agent.StopSequences = &s
	}

	// Resolve model name -> ID. MUST be tenant-scoped: model names are
	// unique within a tenant (idx_models_tenant_name), but the same name
	// across tenants is legal — every fresh signup goes through the
	// onboarding wizard and lands a `glm-default` (or similar) into its
	// own row. Without the tenant filter the global FIRST match wins, so
	// backfillTenantAgentsToDefault binds the new tenant's agent to a
	// stranger tenant's model UUID — a cross-tenant leak.
	if rec.ModelName != "" {
		var model models.LLMProviderModel
		if err := db.Scopes(tenantScope(db.Statement.Context)).
			Where("name = ?", rec.ModelName).
			First(&model).Error; err != nil {
			return models.AgentModel{}, fmt.Errorf("resolve model %q: %w", rec.ModelName, err)
		}
		agent.ModelID = &model.ID
	}

	// ConfirmBefore: []string -> JSON string
	if len(rec.ConfirmBefore) > 0 {
		data, err := json.Marshal(rec.ConfirmBefore)
		if err != nil {
			return models.AgentModel{}, fmt.Errorf("marshal confirm_before: %w", err)
		}
		s := string(data)
		agent.ConfirmBefore = &s
	}

	// Builtin tools
	for i, name := range rec.BuiltinTools {
		agent.Tools = append(agent.Tools, models.AgentToolModel{
			ToolType:  "builtin",
			ToolName:  name,
			SortOrder: i,
		})
	}

	// Custom tools
	for i, ct := range rec.CustomTools {
		var cfgPtr *string
		if ct.Config != "" {
			s := ct.Config
			cfgPtr = &s
		}
		agent.Tools = append(agent.Tools, models.AgentToolModel{
			ToolType:  "custom",
			ToolName:  ct.Name,
			Config:    cfgPtr,
			SortOrder: len(rec.BuiltinTools) + i,
		})
	}

	return agent, nil
}

// loadAllCanSpawn loads CanSpawn (delegation targets) from agent_relations for
// all agents in the current tenant. We scope the join by tenant on both ends
// so that a tenant never sees names belonging to another tenant.
func (r *GORMAgentRepository) loadAllCanSpawn(ctx context.Context) (map[string][]string, error) {
	var rels []models.AgentRelationModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Preload("SourceAgent").
		Preload("TargetAgent").
		Find(&rels).Error; err != nil {
		return nil, fmt.Errorf("load agent relations: %w", err)
	}

	result := make(map[string][]string)
	for _, rel := range rels {
		result[rel.SourceAgent.Name] = append(result[rel.SourceAgent.Name], rel.TargetAgent.Name)
	}
	return result, nil
}

// loadCanSpawnForAgent loads CanSpawn targets for a single agent from agent_relations.
// Q.5: resolves agent name → id, then queries by source_agent_id. Tenant-scoped.
func (r *GORMAgentRepository) loadCanSpawnForAgent(ctx context.Context, agentName string) []string {
	var agent models.AgentModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("name = ?", agentName).
		First(&agent).Error; err != nil {
		return nil
	}
	var rels []models.AgentRelationModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Preload("TargetAgent").
		Where("source_agent_id = ?", agent.ID).
		Find(&rels).Error; err != nil {
		return nil
	}

	names := make([]string, 0, len(rels))
	for _, rel := range rels {
		names = append(names, rel.TargetAgent.Name)
	}
	return names
}

// createMCPAssociations links agent to MCP servers via join table (tenant-stamped).
func (r *GORMAgentRepository) createMCPAssociations(ctx context.Context, agentID string, serverNames []string) error {
	return r.createMCPAssociationsWithTxCtx(ctx, r.db.WithContext(ctx), agentID, serverNames)
}

func (r *GORMAgentRepository) createMCPAssociationsWithTx(tx *gorm.DB, agentID string, serverNames []string) error {
	// Inside an explicit transaction the caller already has the ctx-backed tx,
	// so the tx's Statement.Context carries the tenant. Pass that through to
	// the ctx-aware helper so tenant stamping stays consistent.
	return r.createMCPAssociationsWithTxCtx(tx.Statement.Context, tx, agentID, serverNames)
}

func (r *GORMAgentRepository) createMCPAssociationsWithTxCtx(ctx context.Context, tx *gorm.DB, agentID string, serverNames []string) error {
	if len(serverNames) == 0 {
		return nil
	}

	tenantID := tenantIDFromCtx(ctx)

	var servers []models.MCPServerModel
	if err := tx.
		Where("name IN ? AND tenant_id = ?", serverNames, tenantID).
		Find(&servers).Error; err != nil {
		return fmt.Errorf("resolve mcp servers: %w", err)
	}

	for _, s := range servers {
		if err := tx.Exec(
			"INSERT INTO agent_mcp_servers (agent_id, mcp_server_id, tenant_id) VALUES (?, ?, ?)",
			agentID, s.ID, tenantID,
		).Error; err != nil {
			return fmt.Errorf("link mcp server %q: %w", s.Name, err)
		}
	}
	return nil
}

// loadAllAgentMCPServers loads MCP server names for all agents in a single query (tenant-scoped).
func (r *GORMAgentRepository) loadAllAgentMCPServers(ctx context.Context) (map[string][]string, error) {
	var joins []models.AgentMCPServer
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Preload("MCPServer").
		Find(&joins).Error; err != nil {
		return nil, fmt.Errorf("load agent mcp servers: %w", err)
	}

	result := make(map[string][]string)
	for _, j := range joins {
		result[j.AgentID] = append(result[j.AgentID], j.MCPServer.Name)
	}
	return result, nil
}

// loadMCPServersForAgent loads MCP server names for a single agent (tenant-scoped).
func (r *GORMAgentRepository) loadMCPServersForAgent(ctx context.Context, agentID string) ([]string, error) {
	var joins []models.AgentMCPServer
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Preload("MCPServer").
		Where("agent_id = ?", agentID).
		Find(&joins).Error; err != nil {
		return nil, fmt.Errorf("load mcp servers: %w", err)
	}

	names := make([]string, 0, len(joins))
	for _, j := range joins {
		names = append(names, j.MCPServer.Name)
	}
	return names, nil
}
