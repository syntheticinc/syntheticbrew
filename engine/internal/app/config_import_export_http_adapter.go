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

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// configImportExportHTTPAdapter bridges GORM DB to the http.ConfigImportExporter interface.
// YAML types used by this adapter are defined in config_yaml_types.go.
type configImportExportHTTPAdapter struct {
	db *gorm.DB
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

	return &cfg, nil
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

	return a.db.Transaction(func(tx *gorm.DB) error {
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
		)
		return nil
	})
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

