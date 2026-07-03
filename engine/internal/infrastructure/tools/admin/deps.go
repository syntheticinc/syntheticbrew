package admin

import (
	"context"

	"github.com/syntheticinc/syntheticbrew/internal/service/mcp"
)

// NOTE: Admin tools operate without tenant scoping (CE = single-tenant by design).
// Cloud deployments MUST NOT expose admin tools to non-admin agents.

// AdminToolDependencies holds repositories and callbacks for admin tools.
// Captured in closure at registration time via RegisterAdminTools.
type AdminToolDependencies struct {
	AgentRepo         AgentRepository
	SchemaRepo        SchemaRepository
	MCPServerRepo     MCPServerRepository
	ModelRepo         ModelRepository
	AgentRelationRepo AgentRelationRepository
	SessionRepo       SessionRepository
	CapabilityRepo    CapabilityRepository
	Reloader          func()              // AgentRegistry reload callback
	TransportPolicy   mcp.TransportPolicy // MCP transport restriction policy
	// WidgetTokenMinter mints chat-scoped widget tokens for get_embed_snippet.
	// Nil disables the get_embed_snippet tool at registration time.
	WidgetTokenMinter WidgetTokenMinter
}

// Consumer-side interfaces (defined here, implemented by GORM repo adapters):

// AgentRepository provides agent CRUD for admin tools.
type AgentRepository interface {
	List(ctx context.Context) ([]AgentRecord, error)
	GetByName(ctx context.Context, name string) (*AgentRecord, error)
	Create(ctx context.Context, record *AgentRecord) error
	Update(ctx context.Context, name string, record *AgentRecord) error
	Delete(ctx context.Context, name string) error
}

// AgentUUIDResolver returns an agent's UUID given either its name or UUID.
// Consumer-side contract used by schema/attachment tools that accept either
// form of agent reference from the LLM. Value "" means "preserve existing".
//
// Implementations should return the UUID unchanged when it is already a
// canonical UUID (cheap string check), and resolve by name otherwise.
type AgentUUIDResolver interface {
	ResolveAgentID(ctx context.Context, ref string) (string, error)
}

// SchemaRepository provides schema CRUD for admin tools.
//
// V2: schema membership is derived from `agent_relations` (see
// docs/architecture/agent-first-runtime.md §2.1) — there is no separate
// AddAgent / RemoveAgent surface. Adding an agent to a schema is done by
// creating a delegation relation through AgentRelationRepository.
type SchemaRepository interface {
	List(ctx context.Context) ([]SchemaRecord, error)
	GetByID(ctx context.Context, id string) (*SchemaRecord, error)
	Create(ctx context.Context, record *SchemaRecord) error
	Update(ctx context.Context, id string, record *SchemaRecord) error
	Delete(ctx context.Context, id string) error
}

// MCPServerRepository provides MCP server CRUD for admin tools.
type MCPServerRepository interface {
	List(ctx context.Context) ([]MCPServerRecord, error)
	GetByID(ctx context.Context, id string) (*MCPServerRecord, error)
	Create(ctx context.Context, record *MCPServerRecord) error
	Update(ctx context.Context, id string, record *MCPServerRecord) error
	Delete(ctx context.Context, id string) error
}

// ModelRepository provides model CRUD for admin tools.
//
// GetDefault / SetDefault operate on the tenant's default chat model (kind
// is hard-coded to 'chat' inside the implementation — admin tools do not
// expose embedding defaults today). The partial unique index keyed on
// (tenant_id, kind='chat') guarantees at most one chat default per tenant
// at the DB layer.
type ModelRepository interface {
	List(ctx context.Context) ([]ModelRecord, error)
	GetByID(ctx context.Context, id string) (*ModelRecord, error)
	Create(ctx context.Context, record *ModelRecord) error
	Update(ctx context.Context, id string, record *ModelRecord) error
	Delete(ctx context.Context, id string) error
	GetDefault(ctx context.Context) (*ModelRecord, error)
	SetDefault(ctx context.Context, id string) error
}

// AgentRelationRepository provides agent-relation CRUD for admin tools.
type AgentRelationRepository interface {
	List(ctx context.Context, schemaID string) ([]AgentRelationRecord, error)
	Create(ctx context.Context, record *AgentRelationRecord) error
	Delete(ctx context.Context, id string) error
}

// SessionRepository provides read-only session access for admin tools.
type SessionRepository interface {
	List(ctx context.Context) ([]SessionRecord, error)
	GetByID(ctx context.Context, id string) (*SessionRecord, error)
}

// CapabilityRepository provides capability CRUD for admin tools.
type CapabilityRepository interface {
	ListByAgent(ctx context.Context, agentName string) ([]CapabilityRecord, error)
	Create(ctx context.Context, record *CapabilityRecord) error
	Update(ctx context.Context, id string, record *CapabilityRecord) error
	Delete(ctx context.Context, id string) error
}

// AgentRecord mirrors configrepo.AgentRecord fields needed by admin tools.
type AgentRecord struct {
	ID            string
	Name          string
	SystemPrompt  string
	ModelName     string
	Lifecycle     string
	ToolExecution string
	MaxSteps      int
	BuiltinTools  []string
	MCPServers    []string
	CanSpawn      []string
	IsSystem      bool
}

// SchemaRecord represents a schema for admin tools.
//
// EntryAgentID / ChatEnabled are optional write-side overrides plumbed through
// to SchemaRepository.Update so admin_update_schema can flip chat access and
// re-point the entry agent without going through the REST layer. Nil pointers
// mean "preserve existing value" — zero values would incorrectly clear the
// fields.
type SchemaRecord struct {
	ID           string
	Name         string
	Description  string
	AgentNames   []string
	EntryAgentID *string
	ChatEnabled  *bool
}

// MCPServerRecord represents an MCP server for admin tools.
type MCPServerRecord struct {
	ID      string
	Name    string
	Type    string
	Command string
	URL     string
	Args    []string
	EnvVars map[string]string
	// Enabled reflects the row's `enabled` column. False means the server
	// stays configured but is not injected into agents at runtime.
	Enabled bool
}

// ModelRecord represents an LLM model configuration for admin tools.
type ModelRecord struct {
	ID        string
	Name      string
	Type      string
	BaseURL   string
	ModelName string
	APIKey    string // write-only, masked on read
	// IsDefault mirrors models.is_default. Readers use it to highlight the
	// default chat model in UI / tool output; writers can set it true on
	// create to explicitly promote the new row to default (otherwise the
	// HTTP adapter auto-promotes the first chat model per tenant).
	IsDefault bool
}

// AgentRelationRecord represents a delegation relation between agents in a
// schema. V2 has a single implicit DELEGATION type — no per-row Type field
// (see docs/architecture/agent-first-runtime.md §3.1).
type AgentRelationRecord struct {
	ID        string
	SchemaID  string
	FromAgent string
	ToAgent   string
	Label     string
}

// SessionRecord represents a session for admin tools.
// Q.5: AgentName dropped — session belongs to schema.
type SessionRecord struct {
	ID        string
	UserID    string
	StartedAt string
	Status    string
}

// CapabilityRecord represents an agent capability for admin tools.
type CapabilityRecord struct {
	ID        string
	AgentName string
	Type      string
	Config    map[string]interface{}
	Enabled   bool
}
