package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/kgapply"
)

// configImportExportHTTPAdapter bridges GORM DB to the http.ConfigImportExporter interface.
// YAML types used by this adapter are defined in config_yaml_types.go.
//
// Knowledge Graphs sections (engine 1.3.0+) round-trip via:
//   - kgApply (apply usecase)         — for import: atomic schema + entity persistence
//   - kgBundleRepo / kgSchemaRepo / kgEntityRepo — for export: direct DB reads
// Tenant scope is taken from ctx (or CETenantID fallback) so single-tenant CE
// continues to work unchanged.
type configImportExportHTTPAdapter struct {
	db           *gorm.DB
	kgApply      *kgapply.Usecase
	kgBundleRepo *configrepo.GORMKGBundleRepository
	kgSchemaRepo *configrepo.GORMKGSchemaRepository
	kgEntityRepo *configrepo.GORMKGEntityRepository
}

// SetKnowledgeGraphs wires KG dependencies into the adapter after the
// adapter has been constructed (KG repos + usecase are built later in
// routes_register.go to share with the dedicated /knowledge-graphs handlers).
// Pointer mutation: any deliveryhttp.ConfigHandler that already captured
// this adapter sees the wired KG dependencies on its next call.
func (a *configImportExportHTTPAdapter) SetKnowledgeGraphs(
	apply *kgapply.Usecase,
	bundles *configrepo.GORMKGBundleRepository,
	schemas *configrepo.GORMKGSchemaRepository,
	entities *configrepo.GORMKGEntityRepository,
) {
	a.kgApply = apply
	a.kgBundleRepo = bundles
	a.kgSchemaRepo = schemas
	a.kgEntityRepo = entities
}

// ExportYAML reads all config from DB and marshals to YAML.
func (a *configImportExportHTTPAdapter) ExportYAML(ctx context.Context) ([]byte, error) {
	cfg, err := a.buildExportConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("build export config: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal yaml: %w", err)
	}

	header := fmt.Sprintf("# SyntheticBrew Engine Configuration\n# Exported: %s\n\n", time.Now().UTC().Format(time.RFC3339))
	return append([]byte(header), data...), nil
}

func (a *configImportExportHTTPAdapter) buildExportConfig(ctx context.Context) (*configYAML, error) {
	var cfg configYAML

	agentsYAML, err := a.exportAgents(ctx)
	if err != nil {
		return nil, fmt.Errorf("export agents: %w", err)
	}
	cfg.Agents.Items = agentsYAML

	modelsYAML, err := a.exportModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("export models: %w", err)
	}
	cfg.Models.Items = modelsYAML

	mcpYAML, err := a.exportMCPServers(ctx)
	if err != nil {
		return nil, fmt.Errorf("export mcp servers: %w", err)
	}
	cfg.MCPServers.Items = mcpYAML

	kgYAML, err := a.exportKnowledgeGraphs(ctx)
	if err != nil {
		return nil, fmt.Errorf("export knowledge_graphs: %w", err)
	}
	cfg.KnowledgeGraphs = kgYAML

	return &cfg, nil
}

// exportKnowledgeGraphs reads all bundles for the calling tenant and returns
// the YAML representation. Bundles, schemas, and entities are pulled in
// separate queries; the assembled structure round-trips through ImportYAML
// without information loss (apart from server-generated timestamps).
func (a *configImportExportHTTPAdapter) exportKnowledgeGraphs(ctx context.Context) ([]knowledgeGraphYAML, error) {
	if a.kgBundleRepo == nil {
		return nil, nil
	}
	tenantID := domain.TenantIDFromContext(ctx)
	if tenantID == "" {
		tenantID = domain.CETenantID
	}

	bundles, err := a.kgBundleRepo.List(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list bundles: %w", err)
	}
	out := make([]knowledgeGraphYAML, 0, len(bundles))
	for _, b := range bundles {
		schemas, err := a.kgSchemaRepo.ListByBundle(ctx, tenantID, b.BundleName)
		if err != nil {
			return nil, fmt.Errorf("list schemas for %s: %w", b.BundleName, err)
		}
		schemaYAMLs := make([]knowledgeGraphSchemaYAML, 0, len(schemas))
		entityGroups := make([]knowledgeGraphEntitiesYAML, 0, len(schemas))
		for _, s := range schemas {
			var schemaMap map[string]interface{}
			if err := json.Unmarshal(s.SchemaJSON, &schemaMap); err != nil {
				return nil, fmt.Errorf("decode schema %s/%s: %w", b.BundleName, s.EntityType, err)
			}
			schemaYAMLs = append(schemaYAMLs, knowledgeGraphSchemaYAML{
				EntityType:      s.EntityType,
				Schema:          schemaMap,
				ExposeTools:     s.ExposeTools,
				ToolDescription: s.ToolDescription,
			})

			// Pull up to 10K entities (per-bundle limit from the plan).
			items, _, err := a.kgEntityRepo.ListEntities(ctx, configrepo.ListEntitiesQuery{
				TenantID:   tenantID,
				BundleName: b.BundleName,
				EntityType: s.EntityType,
				Limit:      500,
				Offset:     0,
			})
			if err != nil {
				return nil, fmt.Errorf("list entities for %s/%s: %w", b.BundleName, s.EntityType, err)
			}
			itemsYAML := make([]map[string]interface{}, 0, len(items))
			for _, e := range items {
				var data map[string]interface{}
				if err := json.Unmarshal(e.Data, &data); err != nil {
					return nil, fmt.Errorf("decode entity %s/%s/%s: %w", b.BundleName, s.EntityType, e.EntityID, err)
				}
				itemsYAML = append(itemsYAML, data)
			}
			entityGroups = append(entityGroups, knowledgeGraphEntitiesYAML{
				EntityType: s.EntityType,
				Items:      itemsYAML,
			})
		}
		out = append(out, knowledgeGraphYAML{
			BundleName: b.BundleName,
			Version:    b.Version,
			Schemas:    schemaYAMLs,
			Entities:   entityGroups,
		})
	}
	return out, nil
}

func (a *configImportExportHTTPAdapter) exportAgents(_ context.Context) ([]agentYAML, error) {
	var agents []models.AgentModel
	if err := a.db.Preload("Model").Preload("Tools", func(db *gorm.DB) *gorm.DB {
		return db.Order("sort_order ASC")
	}).Find(&agents).Error; err != nil {
		return nil, fmt.Errorf("query agents: %w", err)
	}

	// Load MCP server associations separately (comment in model explains why).
	var agentMCPs []models.AgentMCPServer
	if err := a.db.Preload("MCPServer").Find(&agentMCPs).Error; err != nil {
		return nil, fmt.Errorf("query agent mcp servers: %w", err)
	}
	mcpByAgent := make(map[string][]string)
	for _, am := range agentMCPs {
		mcpByAgent[am.AgentID] = append(mcpByAgent[am.AgentID], am.MCPServer.Name)
	}

	// Load CanSpawn from agent_relations (V2 replaces agent_spawn_targets).
	var rels []models.AgentRelationModel
	if err := a.db.Preload("SourceAgent").Preload("TargetAgent").Find(&rels).Error; err != nil {
		return nil, fmt.Errorf("query agent relations: %w", err)
	}
	spawnByAgent := make(map[string][]string)
	for _, rel := range rels {
		spawnByAgent[rel.SourceAgent.Name] = append(spawnByAgent[rel.SourceAgent.Name], rel.TargetAgent.Name)
	}

	result := make([]agentYAML, 0, len(agents))
	for _, ag := range agents {
		ay := agentYAML{
			Name:            ag.Name,
			SystemPrompt:    ag.SystemPrompt,
			Lifecycle:       ag.Lifecycle,
			ToolExecution:   ag.ToolExecution,
			MaxSteps:        ag.MaxSteps,
			MaxContextSize:  ag.MaxContextSize,
			MaxTurnDuration: ag.MaxTurnDuration,
			MaxStepDuration: ag.MaxStepDuration,
			Temperature:     ag.Temperature,
			TopP:            ag.TopP,
			MaxTokens:       ag.MaxTokens,
			MCPServers:      mcpByAgent[ag.ID],
		}

		if ag.Model != nil {
			ay.ModelName = ag.Model.Name
		}

		if ag.ConfirmBefore != nil && *ag.ConfirmBefore != "" {
			_ = json.Unmarshal([]byte(*ag.ConfirmBefore), &ay.ConfirmBefore)
		}
		if ag.StopSequences != nil && *ag.StopSequences != "" {
			_ = json.Unmarshal([]byte(*ag.StopSequences), &ay.StopSequences)
		}

		for _, t := range ag.Tools {
			ay.Tools = append(ay.Tools, t.ToolName)
		}

		ay.CanSpawn = spawnByAgent[ag.Name]

		result = append(result, ay)
	}
	return result, nil
}

func (a *configImportExportHTTPAdapter) exportModels(_ context.Context) ([]modelYAML, error) {
	var llms []models.LLMProviderModel
	if err := a.db.Find(&llms).Error; err != nil {
		return nil, fmt.Errorf("query models: %w", err)
	}

	result := make([]modelYAML, 0, len(llms))
	for _, m := range llms {
		result = append(result, modelYAML{
			Name:      m.Name,
			Type:      m.Type,
			BaseURL:   m.BaseURL,
			ModelName: m.ModelName,
			ExtraBody: m.GetConfig().ExtraBody,
			// API key intentionally not exported.
		})
	}
	return result, nil
}

func (a *configImportExportHTTPAdapter) exportMCPServers(_ context.Context) ([]mcpServerYAML, error) {
	var servers []models.MCPServerModel
	if err := a.db.Find(&servers).Error; err != nil {
		return nil, fmt.Errorf("query mcp servers: %w", err)
	}

	result := make([]mcpServerYAML, 0, len(servers))
	for _, s := range servers {
		my := mcpServerYAML{
			Name:    s.Name,
			Type:    s.Type,
			Command: s.Command,
			URL:     s.URL,
		}
		if s.Args != nil && *s.Args != "" {
			var args []string
			if err := json.Unmarshal([]byte(*s.Args), &args); err == nil {
				my.Args = args
			}
		}
		if s.EnvVars != nil && *s.EnvVars != "" {
			var envVars map[string]string
			if err := json.Unmarshal([]byte(*s.EnvVars), &envVars); err == nil {
				// Mask env var values for security.
				masked := make(map[string]string, len(envVars))
				for k := range envVars {
					masked[k] = fmt.Sprintf("${%s}", k)
				}
				my.EnvVars = masked
			}
		}
		if s.ForwardHeaders != nil && *s.ForwardHeaders != "" {
			var fh []string
			if err := json.Unmarshal([]byte(*s.ForwardHeaders), &fh); err == nil {
				my.ForwardHeaders = fh
			}
		}
		result = append(result, my)
	}
	return result, nil
}

// ImportYAML parses YAML config and writes to DB in a transaction.
func (a *configImportExportHTTPAdapter) ImportYAML(ctx context.Context, yamlData []byte) error {
	var cfg configYAML
	if err := yaml.Unmarshal(yamlData, &cfg); err != nil {
		return fmt.Errorf("parse yaml: %w", err)
	}

	if err := a.db.Transaction(func(tx *gorm.DB) error {
		if err := a.importModels(tx, cfg.Models.Items); err != nil {
			return fmt.Errorf("import models: %w", err)
		}

		if err := a.importMCPServers(tx, cfg.MCPServers.Items); err != nil {
			return fmt.Errorf("import mcp servers: %w", err)
		}

		if err := a.importAgents(tx, cfg.Agents.Items); err != nil {
			return fmt.Errorf("import agents: %w", err)
		}

		slog.InfoContext(ctx, "config imported",
			"agents", len(cfg.Agents.Items),
			"models", len(cfg.Models.Items),
			"mcp_servers", len(cfg.MCPServers.Items),
			"knowledge_graphs", len(cfg.KnowledgeGraphs),
		)
		return nil
	}); err != nil {
		return err
	}

	// Knowledge Graphs apply runs OUTSIDE the agents/models/mcp transaction
	// because kgapply requires its own transaction-aware repository chain.
	// Best-effort sequential: a failing bundle stops the loop but does not
	// undo the already-committed agents/models/mcp inserts.
	if err := a.importKnowledgeGraphs(ctx, cfg.KnowledgeGraphs); err != nil {
		return fmt.Errorf("import knowledge_graphs: %w", err)
	}
	return nil
}

// importKnowledgeGraphs delegates each bundle to the apply usecase, which
// runs its own atomic transaction with cross-ref validation + tool collision
// detection. Failing apply rejects the bundle but does NOT undo previously
// imported sections (agents/models/mcp) since they were committed earlier —
// callers should treat /config/import as best-effort sequential for KG.
//
// (Original design called for a single outer transaction, but KG apply
// requires its own transaction-aware repositories, so we keep KG apply
// stand-alone and document the partial-success semantic.)
func (a *configImportExportHTTPAdapter) importKnowledgeGraphs(ctx context.Context, items []knowledgeGraphYAML) error {
	if a.kgApply == nil || len(items) == 0 {
		return nil
	}
	for _, item := range items {
		input := kgapply.Input{
			BundleName: item.BundleName,
			Version:    item.Version,
			Schemas:    make([]kgapply.SchemaInput, 0, len(item.Schemas)),
			Entities:   make([]kgapply.EntitySetInput, 0, len(item.Entities)),
		}
		if input.Version == "" {
			input.Version = "1.0.0"
		}
		for _, s := range item.Schemas {
			schemaJSON, err := json.Marshal(s.Schema)
			if err != nil {
				return fmt.Errorf("marshal schema %s/%s: %w", item.BundleName, s.EntityType, err)
			}
			input.Schemas = append(input.Schemas, kgapply.SchemaInput{
				EntityType:      s.EntityType,
				SchemaJSON:      schemaJSON,
				ExposeTools:     s.ExposeTools,
				ToolDescription: s.ToolDescription,
			})
		}
		for _, e := range item.Entities {
			input.Entities = append(input.Entities, kgapply.EntitySetInput{
				EntityType: e.EntityType,
				Items:      e.Items,
			})
		}
		if _, err := a.kgApply.Execute(ctx, input); err != nil {
			return fmt.Errorf("apply bundle %s: %w", item.BundleName, err)
		}
	}
	return nil
}

func (a *configImportExportHTTPAdapter) importModels(tx *gorm.DB, items []modelYAML) error {
	for _, m := range items {
		var existing models.LLMProviderModel
		err := tx.Where("name = ?", m.Name).First(&existing).Error
		if err == nil {
			// Update existing (preserve API key). Carry ExtraBody through
			// alongside the rest of the fields so YAML stays the source of
			// truth — empty extra_body in YAML clears any previously-set value.
			existing.Type = m.resolvedType()
			existing.BaseURL = m.BaseURL
			existing.ModelName = m.ModelName
			cfg := existing.GetConfig()
			cfg.ExtraBody = m.ExtraBody
			existing.SetConfig(cfg)
			if err := tx.Save(&existing).Error; err != nil {
				return fmt.Errorf("update model %q: %w", m.Name, err)
			}
			continue
		}

		newModel := models.LLMProviderModel{
			Name:      m.Name,
			Type:      m.resolvedType(),
			BaseURL:   m.BaseURL,
			ModelName: m.ModelName,
		}
		if len(m.ExtraBody) > 0 {
			newModel.SetConfig(models.ModelConfig{ExtraBody: m.ExtraBody})
		}
		if err := tx.Create(&newModel).Error; err != nil {
			return fmt.Errorf("create model %q: %w", m.Name, err)
		}
	}
	return nil
}

func (a *configImportExportHTTPAdapter) importMCPServers(tx *gorm.DB, items []mcpServerYAML) error {
	for _, s := range items {
		var existing models.MCPServerModel
		err := tx.Where("name = ?", s.Name).First(&existing).Error

		var argsJSON *string
		if len(s.Args) > 0 {
			data, _ := json.Marshal(s.Args)
			s := string(data)
			argsJSON = &s
		}

		var envJSON *string
		if len(s.EnvVars) > 0 {
			// Filter out placeholder values like "${VAR_NAME}".
			clean := make(map[string]string)
			for k, v := range s.EnvVars {
				if !isEnvPlaceholder(v) {
					clean[k] = v
				}
			}
			if len(clean) > 0 {
				data, _ := json.Marshal(clean)
				s := string(data)
				envJSON = &s
			}
		}

		var forwardHeadersJSON *string
		if len(s.ForwardHeaders) > 0 {
			data, _ := json.Marshal(s.ForwardHeaders)
			s := string(data)
			forwardHeadersJSON = &s
		}

		if err == nil {
			existing.Type = s.Type
			existing.Command = s.Command
			existing.URL = s.URL
			if argsJSON != nil {
				existing.Args = argsJSON
			}
			if envJSON != nil {
				existing.EnvVars = envJSON
			}
			if forwardHeadersJSON != nil {
				existing.ForwardHeaders = forwardHeadersJSON
			}
			if err := tx.Save(&existing).Error; err != nil {
				return fmt.Errorf("update mcp server %q: %w", s.Name, err)
			}
			continue
		}

		newServer := models.MCPServerModel{
			Name:           s.Name,
			Type:           s.Type,
			Command:        s.Command,
			Args:           argsJSON,
			URL:            s.URL,
			EnvVars:        envJSON,
			ForwardHeaders: forwardHeadersJSON,
		}
		if err := tx.Create(&newServer).Error; err != nil {
			return fmt.Errorf("create mcp server %q: %w", s.Name, err)
		}
	}
	return nil
}

func applyAgentImportDefaults(ag *agentYAML) {
	if ag.Lifecycle == "" {
		ag.Lifecycle = "persistent"
	}
	if ag.ToolExecution == "" {
		ag.ToolExecution = "sequential"
	}
	if ag.MaxSteps == 0 {
		ag.MaxSteps = 50
	}
	if ag.MaxContextSize == 0 {
		ag.MaxContextSize = 16000
	}
	if ag.MaxTurnDuration == 0 {
		ag.MaxTurnDuration = 120
	}
}

func (a *configImportExportHTTPAdapter) importAgents(tx *gorm.DB, items []agentYAML) error {
	// Pass 1: create/update all agent records (without spawn targets that reference other agents).
	agentIDs := make(map[string]string, len(items))
	for _, ag := range items {
		applyAgentImportDefaults(&ag)
		if err := validateMaxStepDuration(ag.MaxStepDuration); err != nil {
			return fmt.Errorf("agent %q: %w", ag.Name, err)
		}
		var modelID *string
		if ag.ModelName != "" {
			var llm models.LLMProviderModel
			if err := tx.Where("name = ?", ag.ModelName).First(&llm).Error; err != nil {
				return fmt.Errorf("model %q referenced by agent %q not found: %w", ag.ModelName, ag.Name, err)
			}
			modelID = &llm.ID
		}

		var existing models.AgentModel
		err := tx.Where("name = ?", ag.Name).First(&existing).Error
		if err == nil {
			existing.SystemPrompt = ag.SystemPrompt
			existing.ModelID = modelID
			existing.Lifecycle = ag.Lifecycle
			existing.ToolExecution = ag.ToolExecution
			existing.MaxSteps = ag.MaxSteps
			existing.MaxContextSize = ag.MaxContextSize
			existing.MaxTurnDuration = ag.MaxTurnDuration
			existing.MaxStepDuration = ag.MaxStepDuration
			existing.Temperature = ag.Temperature
			existing.TopP = ag.TopP
			existing.MaxTokens = ag.MaxTokens
			if len(ag.StopSequences) > 0 {
				if ssJSON, err := json.Marshal(ag.StopSequences); err == nil {
					s := string(ssJSON)
					existing.StopSequences = &s
				}
			} else {
				existing.StopSequences = nil
			}
			if len(ag.ConfirmBefore) > 0 {
				if cbJSON, err := json.Marshal(ag.ConfirmBefore); err == nil {
					s := string(cbJSON)
					existing.ConfirmBefore = &s
				}
			} else {
				existing.ConfirmBefore = nil
			}
			if err := tx.Save(&existing).Error; err != nil {
				return fmt.Errorf("update agent %q: %w", ag.Name, err)
			}
			agentIDs[ag.Name] = existing.ID
			continue
		}

		newAgent := models.AgentModel{
			Name:            ag.Name,
			SystemPrompt:    ag.SystemPrompt,
			ModelID:         modelID,
			Lifecycle:       ag.Lifecycle,
			ToolExecution:   ag.ToolExecution,
			MaxSteps:        ag.MaxSteps,
			MaxContextSize:  ag.MaxContextSize,
			MaxTurnDuration: ag.MaxTurnDuration,
			MaxStepDuration: ag.MaxStepDuration,
			Temperature:     ag.Temperature,
			TopP:            ag.TopP,
			MaxTokens:       ag.MaxTokens,
			StopSequences: func() *string {
				if len(ag.StopSequences) == 0 {
					return nil
				}
				d, _ := json.Marshal(ag.StopSequences)
				s := string(d)
				return &s
			}(),
			ConfirmBefore: func() *string {
				if len(ag.ConfirmBefore) == 0 {
					return nil
				}
				d, _ := json.Marshal(ag.ConfirmBefore)
				s := string(d)
				return &s
			}(),
		}
		if err := tx.Create(&newAgent).Error; err != nil {
			return fmt.Errorf("create agent %q: %w", ag.Name, err)
		}
		agentIDs[ag.Name] = newAgent.ID
	}

	// Pass 2: sync relations (tools, spawn targets, MCP servers).
	for _, ag := range items {
		agentID := agentIDs[ag.Name]
		if err := a.syncAgentRelations(tx, agentID, ag); err != nil {
			return fmt.Errorf("sync relations for agent %q: %w", ag.Name, err)
		}
	}

	return nil
}

func (a *configImportExportHTTPAdapter) syncAgentRelations(tx *gorm.DB, agentID string, ag agentYAML) error {
	// Tools: delete old, insert new.
	if err := tx.Where("agent_id = ?", agentID).Delete(&models.AgentToolModel{}).Error; err != nil {
		return fmt.Errorf("delete old tools: %w", err)
	}
	for i, toolName := range ag.Tools {
		tool := models.AgentToolModel{
			AgentID:   agentID,
			ToolType:  models.ToolTypeBuiltin,
			ToolName:  toolName,
			SortOrder: i,
		}
		if err := tx.Create(&tool).Error; err != nil {
			return fmt.Errorf("create tool %q: %w", toolName, err)
		}
	}

	// CanSpawn is now derived from agent_relations (V2). Config import does not
	// create agent_relations — those are managed via the schema/canvas UI.

	// MCP servers: delete old, insert new.
	if err := tx.Where("agent_id = ?", agentID).Delete(&models.AgentMCPServer{}).Error; err != nil {
		return fmt.Errorf("delete old mcp server links: %w", err)
	}
	for _, mcpName := range ag.MCPServers {
		var mcp models.MCPServerModel
		if err := tx.Where("name = ?", mcpName).First(&mcp).Error; err != nil {
			return fmt.Errorf("mcp server %q not found: %w", mcpName, err)
		}
		link := models.AgentMCPServer{
			AgentID:     agentID,
			MCPServerID: mcp.ID,
		}
		if err := tx.Create(&link).Error; err != nil {
			return fmt.Errorf("link mcp server %q: %w", mcpName, err)
		}
	}

	return nil
}

// splitCSV splits a comma-separated string into a slice, trimming whitespace.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// isEnvPlaceholder checks if a value is an env var placeholder like "${VAR_NAME}".
func isEnvPlaceholder(v string) bool {
	return strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}")
}

