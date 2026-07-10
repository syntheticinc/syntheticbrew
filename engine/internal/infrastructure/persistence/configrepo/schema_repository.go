package configrepo

import (
	"context"
	"fmt"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"gorm.io/gorm"
)

// SchemaRecord is an intermediate struct for DB <-> domain mapping.
//
// V2: AgentNames is derived at read time from `agent_relations` (union of
// source and target agent names for the schema). There is no
// `schema_agents` join table — see docs/architecture/agent-first-runtime.md
// §2.1.
type SchemaRecord struct {
	ID              string
	Name            string
	Description     string
	IsSystem        bool
	AgentNames      []string // derived: distinct agents referenced by agent_relations of this schema
	EntryAgentID    *string  // FK to agents.id; may be nil
	ChatEnabled     bool
	ChatLastFiredAt *time.Time
	CreatedAt       time.Time
}

// GORMSchemaRepository implements schema CRUD using GORM.
type GORMSchemaRepository struct {
	db *gorm.DB
}

// NewGORMSchemaRepository creates a new GORMSchemaRepository.
func NewGORMSchemaRepository(db *gorm.DB) *GORMSchemaRepository {
	return &GORMSchemaRepository{db: db}
}

// List returns all schemas for the tenant with their derived agent membership.
func (r *GORMSchemaRepository) List(ctx context.Context) ([]SchemaRecord, error) {
	var schemas []models.SchemaModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Order("created_at ASC").
		Find(&schemas).Error; err != nil {
		return nil, fmt.Errorf("list schemas: %w", err)
	}

	records := make([]SchemaRecord, 0, len(schemas))
	for _, s := range schemas {
		agentNames, err := r.deriveAgentNames(ctx, s.ID)
		if err != nil {
			return nil, fmt.Errorf("derive agents for schema %q: %w", s.Name, err)
		}
		records = append(records, SchemaRecord{
			ID:              s.ID,
			Name:            s.Name,
			Description:     s.Description,
			IsSystem:        s.IsSystem,
			AgentNames:      agentNames,
			EntryAgentID:    s.EntryAgentID,
			ChatEnabled:     s.ChatEnabled,
			ChatLastFiredAt: s.ChatLastFiredAt,
			CreatedAt:       s.CreatedAt,
		})
	}
	return records, nil
}

// CountUserSchemas returns the number of user-created schemas for the tenant.
// Engine-managed system schemas (is_system = true, e.g. the builder-schema)
// are excluded so they do not consume the tenant's schema quota. Tenant-scoped.
func (r *GORMSchemaRepository) CountUserSchemas(ctx context.Context) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).
		Model(&models.SchemaModel{}).
		Scopes(tenantScope(ctx)).
		Where("is_system = ?", false).
		Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count user schemas: %w", err)
	}
	return count, nil
}

// GetByID returns a single schema by ID with derived agent membership (tenant-scoped).
func (r *GORMSchemaRepository) GetByID(ctx context.Context, id string) (*SchemaRecord, error) {
	var schema models.SchemaModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("id = ?", id).
		First(&schema).Error; err != nil {
		return nil, fmt.Errorf("get schema %s: %w", id, err)
	}

	agentNames, err := r.deriveAgentNames(ctx, schema.ID)
	if err != nil {
		return nil, fmt.Errorf("derive agents for schema %s: %w", id, err)
	}

	return &SchemaRecord{
		ID:              schema.ID,
		Name:            schema.Name,
		Description:     schema.Description,
		IsSystem:        schema.IsSystem,
		AgentNames:      agentNames,
		EntryAgentID:    schema.EntryAgentID,
		ChatEnabled:     schema.ChatEnabled,
		ChatLastFiredAt: schema.ChatLastFiredAt,
		CreatedAt:       schema.CreatedAt,
	}, nil
}

// GetSchemaIDByName resolves a schema name to its UUID within the caller's tenant.
//
// Used by the chat endpoint resolver when the URL `{id}` parameter is not a
// UUID — GitOps/declarative-config clients reference schemas by stable name
// because UUIDs are environment-specific (auto-generated on configApply,
// regenerated on DB reset). Returns gorm.ErrRecordNotFound when no row matches.
func (r *GORMSchemaRepository) GetSchemaIDByName(ctx context.Context, name string) (string, error) {
	var id string
	if err := r.db.WithContext(ctx).
		Raw("SELECT id FROM schemas WHERE name = ? AND tenant_id = ?", name, tenantIDFromCtx(ctx)).
		Scan(&id).Error; err != nil {
		return "", fmt.Errorf("get schema id by name %q: %w", name, err)
	}
	if id == "" {
		return "", gorm.ErrRecordNotFound
	}
	return id, nil
}

// GetModelByID returns the raw SchemaModel row (chat dispatcher path).
// Kept separate from GetByID so callers that need entry_agent_id + chat_enabled
// don't pay the cost of deriving AgentNames from agent_relations.
// Tenant-scoped.
func (r *GORMSchemaRepository) GetModelByID(ctx context.Context, id string) (*models.SchemaModel, error) {
	var schema models.SchemaModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("id = ?", id).
		First(&schema).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("get schema %s: %w", id, err)
	}
	return &schema, nil
}

// MarkChatFired stamps chat_last_fired_at on the schema — called from the
// chat dispatcher when a new session starts (replaces trigger.MarkFired).
// Tenant-scoped.
func (r *GORMSchemaRepository) MarkChatFired(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Model(&models.SchemaModel{}).
		Where("id = ?", id).
		Update("chat_last_fired_at", gorm.Expr("NOW()"))
	if result.Error != nil {
		return fmt.Errorf("mark schema chat fired: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("schema not found: %s", id)
	}
	return nil
}

// Create inserts a new schema, stamping tenant from context.
func (r *GORMSchemaRepository) Create(ctx context.Context, record *SchemaRecord) error {
	model := models.SchemaModel{
		TenantID:     tenantIDFromCtx(ctx),
		Name:         record.Name,
		Description:  record.Description,
		IsSystem:     record.IsSystem,
		EntryAgentID: record.EntryAgentID,
		ChatEnabled:  record.ChatEnabled,
	}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("create schema %q: %w", record.Name, err)
	}
	record.ID = model.ID
	return nil
}

// Update updates an existing schema by ID (tenant-scoped).
// Includes entry_agent_id so admins can re-point a schema's entry without a
// delete+recreate cycle. Nil EntryAgentID clears the column. chat_enabled is
// driven by the same DTO — admins toggle chat access from SchemaDetailPage.
func (r *GORMSchemaRepository) Update(ctx context.Context, id string, record *SchemaRecord) error {
	updates := map[string]interface{}{
		"name":           record.Name,
		"description":    record.Description,
		"entry_agent_id": record.EntryAgentID,
		"chat_enabled":   record.ChatEnabled,
	}
	result := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Model(&models.SchemaModel{}).
		Where("id = ?", id).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("update schema %s: %w", id, result.Error)
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// Delete removes a schema and all its agent_relations by ID (tenant-scoped).
// Triggers layer no longer exists in V2 — chat access is a schemas column.
func (r *GORMSchemaRepository) Delete(ctx context.Context, id string) error {
	tenantID := tenantIDFromCtx(ctx)
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Delete agent relations bound to this schema (membership cascade, tenant-scoped).
		if err := tx.
			Where("schema_id = ? AND tenant_id = ?", id, tenantID).
			Delete(&models.AgentRelationModel{}).Error; err != nil {
			return fmt.Errorf("delete schema agent relations: %w", err)
		}
		// Delete schema itself. Conditions are merged into one Where clause to
		// ensure tenant_id scoping is applied (avoids GORM zero-PK quirks).
		result := tx.
			Where("id = ? AND tenant_id = ?", id, tenantID).
			Delete(&models.SchemaModel{})
		if result.Error != nil {
			return fmt.Errorf("delete schema %s: %w", id, result.Error)
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

// ListAgents returns the derived list of agent names that participate in the
// given schema (V2: union of source/target agents in agent_relations).
func (r *GORMSchemaRepository) ListAgents(ctx context.Context, schemaID string) ([]string, error) {
	return r.deriveAgentNames(ctx, schemaID)
}

// ListSchemasForAgent returns schema names that reference a given agent.
//
// V2 derivation: schemas where the agent appears as source or target of any
// agent_relation. See docs/architecture/agent-first-runtime.md §2.1.
//
// Q.5: agent_relations uses source_agent_id/target_agent_id UUIDs. We first
// resolve agentName → agent.id, then query agent_relations by UUID.
// Tenant-scoped at every stage so we never surface another tenant's schema.
func (r *GORMSchemaRepository) ListSchemasForAgent(ctx context.Context, agentName string) ([]string, error) {
	tenantID := tenantIDFromCtx(ctx)

	var agentID string
	if err := r.db.WithContext(ctx).
		Raw("SELECT id FROM agents WHERE name = ? AND tenant_id = ?", agentName, tenantID).
		Scan(&agentID).Error; err != nil || agentID == "" {
		return nil, nil
	}

	var schemaIDs []string
	if err := r.db.WithContext(ctx).
		Raw(`SELECT DISTINCT schema_id FROM agent_relations
			WHERE (source_agent_id = ? OR target_agent_id = ?) AND tenant_id = ?`,
			agentID, agentID, tenantID).
		Scan(&schemaIDs).Error; err != nil {
		return nil, fmt.Errorf("list schema ids for agent %q: %w", agentName, err)
	}

	if len(schemaIDs) == 0 {
		return nil, nil
	}

	var schemas []models.SchemaModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("id IN ?", schemaIDs).
		Find(&schemas).Error; err != nil {
		return nil, fmt.Errorf("load schemas: %w", err)
	}

	names := make([]string, 0, len(schemas))
	for _, s := range schemas {
		names = append(names, s.Name)
	}
	return names, nil
}

// deriveAgentNames returns the distinct agent names participating in a schema.
// Membership is the union of:
//   - the schema's entry agent (schemas.entry_agent_id), if set
//   - all source/target agents of the schema's delegation relations
//
// A schema with an entry agent and no delegations is a valid single-agent
// state (template fork from Generic Assistant, or the system builder-schema).
// Tenant-scoped.
func (r *GORMSchemaRepository) deriveAgentNames(ctx context.Context, schemaID string) ([]string, error) {
	tenantID := tenantIDFromCtx(ctx)

	var names []string
	if err := r.db.WithContext(ctx).
		Raw(`SELECT DISTINCT a.name FROM (
				SELECT entry_agent_id AS agent_id FROM schemas
				WHERE id = ? AND tenant_id = ? AND entry_agent_id IS NOT NULL
				UNION
				SELECT source_agent_id AS agent_id FROM agent_relations
				WHERE schema_id = ? AND tenant_id = ?
				UNION
				SELECT target_agent_id AS agent_id FROM agent_relations
				WHERE schema_id = ? AND tenant_id = ?
			) members JOIN agents a ON a.id = members.agent_id AND a.tenant_id = ? ORDER BY a.name`,
			schemaID, tenantID, schemaID, tenantID, schemaID, tenantID, tenantID).
		Scan(&names).Error; err != nil {
		return nil, fmt.Errorf("derive agent names: %w", err)
	}
	if len(names) == 0 {
		return nil, nil
	}
	return names, nil
}
