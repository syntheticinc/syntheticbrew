package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	// Register callback to auto-generate UUIDs for empty string IDs (SQLite has no gen_random_uuid()).
	db.Callback().Create().Before("gorm:create").Register("test:uuid", func(tx *gorm.DB) {
		if tx.Statement.Schema == nil {
			return
		}
		for _, field := range tx.Statement.Schema.PrimaryFields {
			if field.DBName == "id" {
				val, isZero := field.ValueOf(tx.Statement.Context, tx.Statement.ReflectValue)
				if isZero || val == nil || val == "" {
					_ = field.Set(tx.Statement.Context, tx.Statement.ReflectValue, uuid.New().String())
				}
			}
		}
	})

	// Manual CREATE TABLE because GORM tags use PostgreSQL-specific uuid/gen_random_uuid().
	for _, ddl := range []string{
		`CREATE TABLE models (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			type VARCHAR(30) NOT NULL,
			kind VARCHAR(20) NOT NULL DEFAULT 'chat',
			is_default BOOLEAN NOT NULL DEFAULT 0,
			base_url VARCHAR(500),
			model_name VARCHAR(255) NOT NULL,
			api_key_encrypted VARCHAR(1000),
			api_version VARCHAR(30) DEFAULT '',
			config TEXT NOT NULL DEFAULT '{}',
			tenant_id TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
			created_at DATETIME,
			updated_at DATETIME
		)`,
		// V2 Commit Group C (§5.5): `is_well_known` and `catalog_name` are
		// gone (catalog lives in `mcp_catalog`, install is a copy with no FK).
		`CREATE TABLE mcp_servers (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			type VARCHAR(20) NOT NULL,
			command VARCHAR(500),
			args TEXT,
			url VARCHAR(500),
			env_vars TEXT,
			forward_headers TEXT,
			auth_type VARCHAR(30) NOT NULL DEFAULT 'none',
			auth_key_env VARCHAR(255),
			auth_token_env VARCHAR(255),
			auth_client_id VARCHAR(255),
			enabled BOOLEAN NOT NULL DEFAULT 1,
			tenant_id TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
			catalog_refresh_interval_seconds INTEGER,
			created_at DATETIME,
			updated_at DATETIME
		)`,
		`CREATE TABLE agents (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			model_id TEXT REFERENCES models(id),
			system_prompt TEXT NOT NULL,
			lifecycle VARCHAR(20) NOT NULL DEFAULT 'persistent',
			tool_execution VARCHAR(20) NOT NULL DEFAULT 'sequential',
			max_steps INTEGER NOT NULL DEFAULT 0,
			max_context_size INTEGER NOT NULL DEFAULT 16000,
			max_turn_duration INTEGER NOT NULL DEFAULT 120,
			temperature REAL,
			top_p REAL,
			max_tokens INTEGER,
			stop_sequences TEXT,
			confirm_before TEXT,
			is_system BOOLEAN NOT NULL DEFAULT 0,
			tenant_id TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
			created_at DATETIME,
			updated_at DATETIME
		)`,
		`CREATE TABLE agent_tools (
			id TEXT PRIMARY KEY,
			agent_id TEXT NOT NULL REFERENCES agents(id),
			tool_type VARCHAR(20) NOT NULL,
			tool_name VARCHAR(255) NOT NULL,
			config TEXT,
			sort_order INTEGER NOT NULL DEFAULT 0,
			tenant_id TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
			UNIQUE(agent_id, tool_type, tool_name)
		)`,
		`CREATE TABLE agent_relations (
			id TEXT PRIMARY KEY,
			schema_id TEXT NOT NULL,
			source_agent_id TEXT NOT NULL,
			target_agent_id TEXT NOT NULL,
			config TEXT,
			tenant_id TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
			created_at DATETIME,
			updated_at DATETIME
		)`,
		`CREATE TABLE agent_mcp_servers (
			agent_id TEXT NOT NULL REFERENCES agents(id),
			mcp_server_id TEXT NOT NULL REFERENCES mcp_servers(id),
			tenant_id TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
			PRIMARY KEY (agent_id, mcp_server_id)
		)`,
	} {
		require.NoError(t, db.Exec(ddl).Error)
	}
	return db
}

func seedTestData(t *testing.T, db *gorm.DB) {
	t.Helper()

	// Model
	model := models.LLMProviderModel{
		Name:            "gpt-4o",
		Type:            "openai_compatible",
		BaseURL:         "https://api.openai.com/v1",
		ModelName:       "gpt-4o",
		APIKeyEncrypted: "encrypted-secret",
	}
	require.NoError(t, db.Create(&model).Error)

	// MCP Server
	envJSON, _ := json.Marshal(map[string]string{"API_KEY": "secret123"})
	argsJSON, _ := json.Marshal([]string{"--port", "3000"})
	envJSONStr := string(envJSON)
	argsJSONStr := string(argsJSON)
	mcpServer := models.MCPServerModel{
		Name:    "shop-api",
		Type:    "http",
		URL:     "http://shop-api:3000/mcp",
		Args:    &argsJSONStr,
		EnvVars: &envJSONStr,
	}
	require.NoError(t, db.Create(&mcpServer).Error)

	// Agent
	confirmBefore := `["delete_order","refund"]`
	agent := models.AgentModel{
		Name:           "sales",
		ModelID:        &model.ID,
		SystemPrompt:   "You are a sales assistant.",
		Lifecycle:      "persistent",
		ToolExecution:  "sequential",
		MaxSteps:       30,
		MaxContextSize: 16000,
		ConfirmBefore:  &confirmBefore,
	}
	require.NoError(t, db.Create(&agent).Error)

	// Agent tools
	require.NoError(t, db.Create(&models.AgentToolModel{
		AgentID: agent.ID, ToolType: "builtin", ToolName: "web_search", SortOrder: 0,
	}).Error)
	require.NoError(t, db.Create(&models.AgentToolModel{
		AgentID: agent.ID, ToolType: "builtin", ToolName: "show_structured_output", SortOrder: 1,
	}).Error)

	// Agent MCP server link
	require.NoError(t, db.Create(&models.AgentMCPServer{
		AgentID: agent.ID, MCPServerID: mcpServer.ID,
	}).Error)

	// Second agent (spawn target)
	researcher := models.AgentModel{
		Name:           "researcher",
		SystemPrompt:   "You research things.",
		Lifecycle:      "spawn",
		ToolExecution:  "parallel",
		MaxSteps:       20,
		MaxContextSize: 8000,
	}
	require.NoError(t, db.Create(&researcher).Error)

	// Agent relation (V2 replaces agent_spawn_targets)
	require.NoError(t, db.Create(&models.AgentRelationModel{
		SchemaID:      "00000000-0000-0000-0000-000000000001",
		SourceAgentID: agent.ID, TargetAgentID: researcher.ID,
	}).Error)
}

func TestExportYAML(t *testing.T) {
	db := setupTestDB(t)
	seedTestData(t, db)
	adapter := &configImportExportHTTPAdapter{db: db}

	data, err := adapter.ExportYAML(context.Background())
	require.NoError(t, err)

	output := string(data)

	// Header present
	assert.True(t, strings.HasPrefix(output, "# SyntheticBrew Engine Configuration"))

	// Parse YAML part (skip header comments)
	var cfg configYAML
	require.NoError(t, yaml.Unmarshal(data, &cfg))

	// Agents
	require.Len(t, cfg.Agents.Items, 2)
	sales := findAgentYAML(cfg.Agents.Items, "sales")
	require.NotNil(t, sales)
	assert.Equal(t, "You are a sales assistant.", sales.SystemPrompt)
	assert.Equal(t, "gpt-4o", sales.ModelName)
	assert.Equal(t, "persistent", sales.Lifecycle)
	assert.Equal(t, 30, sales.MaxSteps)
	assert.Equal(t, []string{"web_search", "show_structured_output"}, sales.Tools)
	assert.Equal(t, []string{"researcher"}, sales.CanSpawn)
	assert.Equal(t, []string{"shop-api"}, sales.MCPServers)
	assert.Equal(t, []string{"delete_order", "refund"}, sales.ConfirmBefore)

	// Models — API key must NOT be present
	require.Len(t, cfg.Models.Items, 1)
	assert.Equal(t, "gpt-4o", cfg.Models.Items[0].Name)
	assert.Equal(t, "openai_compatible", cfg.Models.Items[0].Type)
	assert.NotContains(t, output, "encrypted-secret")

	// MCP Servers — env vars must be masked
	require.Len(t, cfg.MCPServers.Items, 1)
	assert.Equal(t, "shop-api", cfg.MCPServers.Items[0].Name)
	assert.Equal(t, "${API_KEY}", cfg.MCPServers.Items[0].EnvVars["API_KEY"])
}

func TestImportYAML(t *testing.T) {
	db := setupTestDB(t)
	adapter := &configImportExportHTTPAdapter{db: db}

	yamlData := `
agents:
  - name: "support"
    system_prompt: "You help customers."
    model_name: "claude-3"
    lifecycle: "persistent"
    tool_execution: "sequential"
    max_steps: 25
    max_context_size: 12000
    tools:
      - web_search
    can_spawn: []
    mcp_servers: []

models:
  - name: "claude-3"
    type: "anthropic"
    base_url: "https://api.anthropic.com"
    model_name: "claude-3-opus-20240229"

mcp_servers:
  - name: "crm"
    type: "http"
    url: "http://crm:8080/mcp"

triggers:
  - title: "Hourly check"
    type: "cron"
    agent_name: "support"
    schedule: "0 * * * *"
    description: "Check tickets"
    enabled: true
`
	err := adapter.ImportYAML(context.Background(), []byte(yamlData))
	require.NoError(t, err)

	// Verify models in DB
	var llms []models.LLMProviderModel
	require.NoError(t, db.Find(&llms).Error)
	require.Len(t, llms, 1)
	assert.Equal(t, "claude-3", llms[0].Name)

	// Verify MCP servers
	var mcps []models.MCPServerModel
	require.NoError(t, db.Find(&mcps).Error)
	require.Len(t, mcps, 1)
	assert.Equal(t, "crm", mcps[0].Name)

	// Verify agents
	var agents []models.AgentModel
	require.NoError(t, db.Preload("Model").Preload("Tools").Find(&agents).Error)
	require.Len(t, agents, 1)
	assert.Equal(t, "support", agents[0].Name)
	assert.Equal(t, "claude-3", agents[0].Model.Name)
	require.Len(t, agents[0].Tools, 1)
	assert.Equal(t, "web_search", agents[0].Tools[0].ToolName)
}

func TestImportYAML_UpdateExisting(t *testing.T) {
	db := setupTestDB(t)
	adapter := &configImportExportHTTPAdapter{db: db}

	// First import
	yamlData := `
models:
  - name: "gpt-4o"
    type: "openai_compatible"
    base_url: "https://api.openai.com/v1"
    model_name: "gpt-4o"
agents:
  - name: "bot"
    system_prompt: "V1 prompt"
    model_name: "gpt-4o"
    lifecycle: "persistent"
    tool_execution: "sequential"
    max_steps: 10
    max_context_size: 8000
    tools:
      - web_search
`
	require.NoError(t, adapter.ImportYAML(context.Background(), []byte(yamlData)))

	// Second import with updated values
	yamlData2 := `
models:
  - name: "gpt-4o"
    type: "openai_compatible"
    base_url: "https://api.openai.com/v2"
    model_name: "gpt-4o-2024-08-06"
agents:
  - name: "bot"
    system_prompt: "V2 prompt"
    model_name: "gpt-4o"
    lifecycle: "persistent"
    tool_execution: "parallel"
    max_steps: 50
    max_context_size: 32000
    tools:
      - show_structured_output
      - web_search
`
	require.NoError(t, adapter.ImportYAML(context.Background(), []byte(yamlData2)))

	// Should still have 1 model (updated)
	var llms []models.LLMProviderModel
	require.NoError(t, db.Find(&llms).Error)
	require.Len(t, llms, 1)
	assert.Equal(t, "https://api.openai.com/v2", llms[0].BaseURL)
	assert.Equal(t, "gpt-4o-2024-08-06", llms[0].ModelName)

	// Should still have 1 agent (updated)
	var agents []models.AgentModel
	require.NoError(t, db.Preload("Tools").Find(&agents).Error)
	require.Len(t, agents, 1)
	assert.Equal(t, "V2 prompt", agents[0].SystemPrompt)
	assert.Equal(t, "parallel", agents[0].ToolExecution)
	assert.Equal(t, 50, agents[0].MaxSteps)
	require.Len(t, agents[0].Tools, 2)
}

func TestImportYAML_InvalidYAML(t *testing.T) {
	db := setupTestDB(t)
	adapter := &configImportExportHTTPAdapter{db: db}

	err := adapter.ImportYAML(context.Background(), []byte("{{invalid"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse yaml")
}

func TestImportYAML_MissingModelReference(t *testing.T) {
	db := setupTestDB(t)
	adapter := &configImportExportHTTPAdapter{db: db}

	yamlData := `
agents:
  - name: "bot"
    system_prompt: "Test"
    model_name: "nonexistent"
    lifecycle: "persistent"
    tool_execution: "sequential"
    max_steps: 10
    max_context_size: 8000
`
	err := adapter.ImportYAML(context.Background(), []byte(yamlData))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent")
}

func TestExportImportRoundTrip(t *testing.T) {
	db := setupTestDB(t)
	seedTestData(t, db)
	adapter := &configImportExportHTTPAdapter{db: db}

	// Export
	exported, err := adapter.ExportYAML(context.Background())
	require.NoError(t, err)

	// Import into fresh DB
	db2 := setupTestDB(t)
	adapter2 := &configImportExportHTTPAdapter{db: db2}
	require.NoError(t, adapter2.ImportYAML(context.Background(), exported))

	// Re-export from second DB
	exported2, err := adapter2.ExportYAML(context.Background())
	require.NoError(t, err)

	// Parse both and compare structure
	var cfg1, cfg2 configYAML
	require.NoError(t, yaml.Unmarshal(exported, &cfg1))
	require.NoError(t, yaml.Unmarshal(exported2, &cfg2))

	assert.Equal(t, len(cfg1.Agents.Items), len(cfg2.Agents.Items))
	assert.Equal(t, len(cfg1.Models.Items), len(cfg2.Models.Items))
	assert.Equal(t, len(cfg1.MCPServers.Items), len(cfg2.MCPServers.Items))

	// Agent names match
	for _, a1 := range cfg1.Agents.Items {
		a2 := findAgentYAML(cfg2.Agents.Items, a1.Name)
		require.NotNil(t, a2, "agent %q missing after round-trip", a1.Name)
		assert.Equal(t, a1.SystemPrompt, a2.SystemPrompt)
		assert.Equal(t, a1.Lifecycle, a2.Lifecycle)
	}
}

func TestExportYAML_EmptyDB(t *testing.T) {
	db := setupTestDB(t)
	adapter := &configImportExportHTTPAdapter{db: db}

	data, err := adapter.ExportYAML(context.Background())
	require.NoError(t, err)

	var cfg configYAML
	require.NoError(t, yaml.Unmarshal(data, &cfg))
	assert.Empty(t, cfg.Agents.Items)
	assert.Empty(t, cfg.Models.Items)
	assert.Empty(t, cfg.MCPServers.Items)
}

func TestSplitCSV(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty", "", nil},
		{"single", "web_search", []string{"web_search"}},
		{"multiple", "a,b,c", []string{"a", "b", "c"}},
		{"with spaces", " a , b , c ", []string{"a", "b", "c"}},
		{"trailing comma", "a,b,", []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitCSV(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsEnvPlaceholder(t *testing.T) {
	assert.True(t, isEnvPlaceholder("${API_KEY}"))
	assert.False(t, isEnvPlaceholder("real-value"))
	assert.False(t, isEnvPlaceholder("${partial"))
}

func TestImportYAML_DefaultsApplied(t *testing.T) {
	db := setupTestDB(t)
	adapter := &configImportExportHTTPAdapter{db: db}

	// Minimal YAML — no lifecycle, tool_execution, max_steps, max_context_size.
	yamlData := `
agents:
  - name: "minimal"
    system_prompt: "Hello"
`
	require.NoError(t, adapter.ImportYAML(context.Background(), []byte(yamlData)))

	var agents []models.AgentModel
	require.NoError(t, db.Find(&agents).Error)
	require.Len(t, agents, 1)

	assert.Equal(t, "persistent", agents[0].Lifecycle, "lifecycle should default to persistent")
	assert.Equal(t, "sequential", agents[0].ToolExecution, "tool_execution should default to sequential")
	assert.Equal(t, 50, agents[0].MaxSteps, "max_steps should default to 50")
	assert.Equal(t, 16000, agents[0].MaxContextSize, "max_context_size should default to 16000")
}

func TestImportYAML_DefaultsNotOverrideExplicit(t *testing.T) {
	db := setupTestDB(t)
	adapter := &configImportExportHTTPAdapter{db: db}

	yamlData := `
agents:
  - name: "custom"
    system_prompt: "Custom"
    lifecycle: "spawn"
    tool_execution: "parallel"
    max_steps: 100
    max_context_size: 32000
`
	require.NoError(t, adapter.ImportYAML(context.Background(), []byte(yamlData)))

	var agents []models.AgentModel
	require.NoError(t, db.Find(&agents).Error)
	require.Len(t, agents, 1)

	assert.Equal(t, "spawn", agents[0].Lifecycle)
	assert.Equal(t, "parallel", agents[0].ToolExecution)
	assert.Equal(t, 100, agents[0].MaxSteps)
	assert.Equal(t, 32000, agents[0].MaxContextSize)
}

func findAgentYAML(agents []agentYAML, name string) *agentYAML {
	for i := range agents {
		if agents[i].Name == name {
			return &agents[i]
		}
	}
	return nil
}
