package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
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
			name TEXT NOT NULL,
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
			name TEXT NOT NULL,
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
			name TEXT NOT NULL,
			model_id TEXT REFERENCES models(id),
			system_prompt TEXT NOT NULL,
			lifecycle VARCHAR(20) NOT NULL DEFAULT 'persistent',
			tool_execution VARCHAR(20) NOT NULL DEFAULT 'sequential',
			max_steps INTEGER NOT NULL DEFAULT 0,
			max_context_size INTEGER NOT NULL DEFAULT 16000,
			max_turn_duration INTEGER NOT NULL DEFAULT 120,
			max_step_duration INTEGER NOT NULL DEFAULT 0,
			temperature REAL,
			top_p REAL,
			max_tokens INTEGER,
			stop_sequences TEXT,
			confirm_before TEXT,
			is_system BOOLEAN NOT NULL DEFAULT 0,
			tenant_id TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
			created_at DATETIME,
			updated_at DATETIME,
			UNIQUE(tenant_id, name)
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
		`CREATE TABLE schemas (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			entry_agent_id TEXT,
			chat_enabled BOOLEAN NOT NULL DEFAULT 0,
			chat_last_fired_at DATETIME,
			is_system BOOLEAN NOT NULL DEFAULT 0,
			tenant_id TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
			created_at DATETIME,
			updated_at DATETIME,
			UNIQUE(tenant_id, name)
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
		Name:            "sales",
		ModelID:         &model.ID,
		SystemPrompt:    "You are a sales assistant.",
		Lifecycle:       "persistent",
		ToolExecution:   "sequential",
		MaxSteps:        30,
		MaxContextSize:  16000,
		MaxStepDuration: 45,
		ConfirmBefore:   &confirmBefore,
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

	// Agent relation (V2 replaces agent_spawn_targets) — relations are
	// schema-scoped, so seed the owning schema too.
	schema := models.SchemaModel{Name: "sales-flow", ChatEnabled: true, EntryAgentID: &agent.ID}
	require.NoError(t, db.Create(&schema).Error)
	require.NoError(t, db.Create(&models.AgentRelationModel{
		SchemaID:      schema.ID,
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
	assert.Empty(t, sales.CanSpawn, "per-agent can_spawn is no longer exported — delegation lives in the schemas section")
	assert.Equal(t, []string{"shop-api"}, sales.MCPServers)
	assert.Equal(t, []string{"delete_order", "refund"}, sales.ConfirmBefore)

	// Schemas — the delegation graph round-trips here
	require.Len(t, cfg.Schemas, 1)
	assert.Equal(t, "sales-flow", cfg.Schemas[0].Name)
	assert.Equal(t, "sales", cfg.Schemas[0].EntryAgent)
	require.Len(t, cfg.Schemas[0].Relations, 1)
	assert.Equal(t, relationYAML{From: "sales", To: "researcher"}, cfg.Schemas[0].Relations[0])

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
		assert.Equal(t, a1.MaxStepDuration, a2.MaxStepDuration, "max_step_duration must survive export→import for agent %q", a1.Name)
	}

	// The seeded "sales" agent carries a non-zero max_step_duration; assert the
	// concrete value (45) survives the full export→import→re-export round-trip.
	sales := findAgentYAML(cfg2.Agents.Items, "sales")
	require.NotNil(t, sales, "sales agent missing after round-trip")
	assert.Equal(t, 45, sales.MaxStepDuration, "max_step_duration value must round-trip verbatim")
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

// TestConfigImportExport_TenantIsolation pins the cloud-first fix: config
// export must only contain the calling tenant's rows, and a name-based import
// upsert must never reach across tenants — the pool of names is per-tenant.
func TestConfigImportExport_TenantIsolation(t *testing.T) {
	db := setupTestDB(t)
	adapter := &configImportExportHTTPAdapter{db: db}

	// Tenant B owns an agent and a model with names tenant A will also use.
	require.NoError(t, db.Create(&models.AgentModel{
		Name: "shared-agent", SystemPrompt: "tenant B secret prompt",
		Lifecycle: "persistent", ToolExecution: "sequential", TenantID: "tenant-b",
	}).Error)
	require.NoError(t, db.Create(&models.LLMProviderModel{
		Name: "shared-model", Type: "openai_compatible", ModelName: "b-model", TenantID: "tenant-b",
	}).Error)

	ctxA := domain.WithTenantID(context.Background(), "tenant-a")

	// Export as tenant A: none of tenant B's config may appear.
	data, err := adapter.ExportYAML(ctxA)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "tenant B secret prompt",
		"export must not leak another tenant's system prompt")
	assert.NotContains(t, string(data), "shared-agent",
		"export must not leak another tenant's agent names")
	assert.NotContains(t, string(data), "shared-model",
		"export must not leak another tenant's model names")

	// Import as tenant A with tenant B's names: B stays untouched, A gets its own rows.
	yamlDoc := `
agents:
  - name: shared-agent
    system_prompt: tenant A prompt
models:
  - name: shared-model
    type: openai_compatible
    model_name: a-model
`
	require.NoError(t, adapter.ImportYAML(ctxA, []byte(yamlDoc)))

	var bAgent models.AgentModel
	require.NoError(t, db.Where("tenant_id = ? AND name = ?", "tenant-b", "shared-agent").First(&bAgent).Error)
	assert.Equal(t, "tenant B secret prompt", bAgent.SystemPrompt,
		"import as tenant A must NOT modify tenant B's same-named agent")

	var aAgent models.AgentModel
	require.NoError(t, db.Where("tenant_id = ? AND name = ?", "tenant-a", "shared-agent").First(&aAgent).Error,
		"import must create the agent under the calling tenant")
	assert.Equal(t, "tenant A prompt", aAgent.SystemPrompt)

	var bModel models.LLMProviderModel
	require.NoError(t, db.Where("tenant_id = ? AND name = ?", "tenant-b", "shared-model").First(&bModel).Error)
	assert.Equal(t, "b-model", bModel.ModelName,
		"import as tenant A must NOT modify tenant B's same-named model")
}

// TestConfigImportExport_SchemaRelationsRoundTrip pins the schemas section:
// the delegation graph (agent_relations) + entry agent must survive
// export → import into an empty instance. Before this section existed,
// export wrote a derived per-agent can_spawn that import silently ignored —
// a restored workspace lost every delegation arrow.
func TestConfigImportExport_SchemaRelationsRoundTrip(t *testing.T) {
	srcDB := setupTestDB(t)
	src := &configImportExportHTTPAdapter{db: srcDB}

	// Source: coordinator → two children inside one schema.
	mkAgent := func(db *gorm.DB, name string) models.AgentModel {
		ag := models.AgentModel{Name: name, SystemPrompt: "p-" + name,
			Lifecycle: "persistent", ToolExecution: "sequential"}
		require.NoError(t, db.Create(&ag).Error)
		return ag
	}
	coord := mkAgent(srcDB, "coordinator")
	childA := mkAgent(srcDB, "child-a")
	childB := mkAgent(srcDB, "child-b")

	schema := models.SchemaModel{Name: "support", Description: "support flow",
		ChatEnabled: true, EntryAgentID: &coord.ID}
	require.NoError(t, srcDB.Create(&schema).Error)
	for _, target := range []models.AgentModel{childA, childB} {
		require.NoError(t, srcDB.Create(&models.AgentRelationModel{
			SchemaID: schema.ID, SourceAgentID: coord.ID, TargetAgentID: target.ID,
		}).Error)
	}
	// System schema must NOT round-trip.
	require.NoError(t, srcDB.Create(&models.SchemaModel{Name: "builder-schema", IsSystem: true}).Error)

	data, err := src.ExportYAML(context.Background())
	require.NoError(t, err)
	assert.Contains(t, string(data), "schemas:")
	assert.Contains(t, string(data), "entry_agent: coordinator")
	assert.NotContains(t, string(data), "builder-schema", "system schemas must not be exported")
	assert.NotContains(t, string(data), "can_spawn", "per-agent can_spawn must no longer be exported")

	// Fresh instance: import restores agents, schema, entry agent and arrows.
	dstDB := setupTestDB(t)
	dst := &configImportExportHTTPAdapter{db: dstDB}
	require.NoError(t, dst.ImportYAML(context.Background(), data))

	var restored models.SchemaModel
	require.NoError(t, dstDB.Where("name = ?", "support").First(&restored).Error)
	assert.True(t, restored.ChatEnabled)
	require.NotNil(t, restored.EntryAgentID, "entry agent must be restored")

	var entry models.AgentModel
	require.NoError(t, dstDB.Where("id = ?", *restored.EntryAgentID).First(&entry).Error)
	assert.Equal(t, "coordinator", entry.Name)

	var rels []models.AgentRelationModel
	require.NoError(t, dstDB.Where("schema_id = ?", restored.ID).
		Preload("SourceAgent").Preload("TargetAgent").Find(&rels).Error)
	require.Len(t, rels, 2, "both delegation arrows must be restored")
	targets := map[string]bool{}
	for _, r := range rels {
		assert.Equal(t, "coordinator", r.SourceAgent.Name)
		// jsonb column: Postgres rejects the empty string, so the import must
		// write a valid JSON document (sqlite would silently accept "").
		assert.Equal(t, "{}", r.Config, "relation Config must be valid JSON for the jsonb column")
		targets[r.TargetAgent.Name] = true
	}
	assert.True(t, targets["child-a"] && targets["child-b"], "arrows must point at both children")
}

// TestConfigImport_UnknownRelationAgentFails pins fail-loud: an arrow whose
// endpoint does not resolve must abort the import instead of silently
// dropping the arrow.
func TestConfigImport_UnknownRelationAgentFails(t *testing.T) {
	db := setupTestDB(t)
	adapter := &configImportExportHTTPAdapter{db: db}

	yamlDoc := `
agents:
  - name: coordinator
    system_prompt: p
schemas:
  - name: support
    relations:
      - from: coordinator
        to: ghost-agent
`
	err := adapter.ImportYAML(context.Background(), []byte(yamlDoc))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ghost-agent")
}

// fakeSchemaGuard records OnSchemaCreate calls and can reject to simulate a
// tier limit, standing in for the plugin quota seam.
type fakeSchemaGuard struct {
	calls    []int // n per call
	rejectAt int   // reject when cumulative new-schema count exceeds this (0 = never)
	total    int
}

func (g *fakeSchemaGuard) OnSchemaCreate(_ context.Context, _ string, n int) error {
	g.calls = append(g.calls, n)
	g.total += n
	if g.rejectAt > 0 && g.total > g.rejectAt {
		return fmt.Errorf("schema quota exceeded")
	}
	return nil
}

// TestConfigImport_SchemaQuotaGuard pins the ee#12 invariant on the import
// path: creating schemas via /config/import must consult the quota seam for
// the number of NEW schemas, and a rejection aborts the whole import.
func TestConfigImport_SchemaQuotaGuard(t *testing.T) {
	t.Run("gates new schemas and blocks over-limit", func(t *testing.T) {
		db := setupTestDB(t)
		guard := &fakeSchemaGuard{rejectAt: 2} // Free tier: 2 schemas
		adapter := &configImportExportHTTPAdapter{db: db, schemaGuard: guard}

		yamlDoc := `
agents:
  - name: a1
    system_prompt: p
schemas:
  - name: s1
  - name: s2
  - name: s3
`
		err := adapter.ImportYAML(context.Background(), []byte(yamlDoc))
		require.Error(t, err, "importing 3 schemas on a 2-schema tier must be rejected")
		assert.Contains(t, err.Error(), "quota")

		// Rollback: no schema rows created.
		var count int64
		require.NoError(t, db.Model(&models.SchemaModel{}).Count(&count).Error)
		assert.Zero(t, count, "a rejected quota must roll back all schema inserts")
		require.Len(t, guard.calls, 1, "guard consulted once for the create batch")
		assert.Equal(t, 3, guard.calls[0], "guard must be asked to admit all 3 new schemas")
	})

	t.Run("re-import of existing schemas does not re-charge quota", func(t *testing.T) {
		db := setupTestDB(t)
		guard := &fakeSchemaGuard{rejectAt: 2}
		adapter := &configImportExportHTTPAdapter{db: db, schemaGuard: guard}

		// Seed one existing schema; agent for FK-free import.
		require.NoError(t, db.Create(&models.AgentModel{Name: "a1", SystemPrompt: "p",
			Lifecycle: "persistent", ToolExecution: "sequential"}).Error)
		require.NoError(t, db.Create(&models.SchemaModel{Name: "s1"}).Error)

		yamlDoc := `
agents:
  - name: a1
    system_prompt: p
schemas:
  - name: s1
  - name: s2
`
		// 1 existing (s1) + 1 new (s2) = 1 create → under the limit.
		require.NoError(t, adapter.ImportYAML(context.Background(), []byte(yamlDoc)))
		require.Len(t, guard.calls, 1)
		assert.Equal(t, 1, guard.calls[0], "only the new schema (s2) counts against quota")
	})
}
