package configrepo

import (
	"context"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"gorm.io/gorm"
)

// GORMMCPServerRepository implements MCP server CRUD using GORM.
type GORMMCPServerRepository struct {
	db *gorm.DB
}

// NewGORMMCPServerRepository creates a new GORMMCPServerRepository.
func NewGORMMCPServerRepository(db *gorm.DB) *GORMMCPServerRepository {
	return &GORMMCPServerRepository{db: db}
}

// List returns all MCP server models for the tenant resolved from ctx.
//
// V2 Commit Group C (§5.6): runtime status (connection state, tools_count)
// is no longer persisted — callers that need status must ping the live MCP
// client registry instead.
//
// HTTP callers continue to use this method (tenant flows in via context).
// Background callers without a request ctx must use ListForTenant — passing
// an explicit tenantID instead of relying on the CE sentinel fallback.
func (r *GORMMCPServerRepository) List(ctx context.Context) ([]models.MCPServerModel, error) {
	return r.ListForTenant(ctx, tenantIDFromCtx(ctx))
}

// ListForTenant returns all MCP server models for the supplied tenantID.
// Explicit tenant scoping for background callers (Manager.Init, lazy load,
// reconnect) so the tenant identity is never inferred from ambient context.
func (r *GORMMCPServerRepository) ListForTenant(ctx context.Context, tenantID string) ([]models.MCPServerModel, error) {
	var servers []models.MCPServerModel
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("name").
		Find(&servers).Error; err != nil {
		return nil, fmt.Errorf("list mcp servers for tenant %s: %w", tenantID, err)
	}
	return servers, nil
}

// GetByName returns a single MCP server model by name within the supplied
// tenant. Used by Manager.ReconnectServer (этап 1) for per-server reconnect
// after CRUD without taking a fresh full-tenant List.
func (r *GORMMCPServerRepository) GetByName(ctx context.Context, tenantID, name string) (*models.MCPServerModel, error) {
	var server models.MCPServerModel
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND name = ?", tenantID, name).
		First(&server).Error; err != nil {
		return nil, fmt.Errorf("get mcp server %s for tenant %s: %w", name, tenantID, err)
	}
	return &server, nil
}

// GetByID returns a single MCP server model by ID (tenant-scoped).
func (r *GORMMCPServerRepository) GetByID(ctx context.Context, id string) (*models.MCPServerModel, error) {
	var server models.MCPServerModel
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("id = ?", id).
		First(&server).Error; err != nil {
		return nil, fmt.Errorf("get mcp server %s: %w", id, err)
	}
	return &server, nil
}

// Create inserts a new MCP server model, stamping tenant from context.
func (r *GORMMCPServerRepository) Create(ctx context.Context, model *models.MCPServerModel) error {
	model.TenantID = tenantIDFromCtx(ctx)
	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		return fmt.Errorf("create mcp server: %w", err)
	}
	return nil
}

// Update updates an MCP server model by ID (tenant-scoped).
// Select("*") ensures zero-value fields (e.g. cleared ForwardHeaders) are written.
func (r *GORMMCPServerRepository) Update(ctx context.Context, id string, model *models.MCPServerModel) error {
	result := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Model(&models.MCPServerModel{}).
		Where("id = ?", id).
		Select("*").Omit("id", "tenant_id", "created_at", "updated_at").
		Updates(model)
	if result.Error != nil {
		return fmt.Errorf("update mcp server: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("mcp server not found: %s", id)
	}
	return nil
}

// Delete removes an MCP server model by ID (tenant-scoped).
func (r *GORMMCPServerRepository) Delete(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Delete(&models.MCPServerModel{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("delete mcp server: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("mcp server not found: %s", id)
	}
	return nil
}

// GetAgentNamesByServerIDs returns a map of MCP server ID -> []agent names
// by querying the agent_mcp_servers join table (tenant-scoped). Loads all servers in one query.
func (r *GORMMCPServerRepository) GetAgentNamesByServerIDs(ctx context.Context, serverIDs []string) (map[string][]string, error) {
	if len(serverIDs) == 0 {
		return make(map[string][]string), nil
	}

	var joins []models.AgentMCPServer
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Preload("Agent").
		Where("mcp_server_id IN ?", serverIDs).
		Find(&joins).Error; err != nil {
		return nil, fmt.Errorf("load agent names for mcp servers: %w", err)
	}

	result := make(map[string][]string, len(serverIDs))
	for _, j := range joins {
		result[j.MCPServerID] = append(result[j.MCPServerID], j.Agent.Name)
	}
	return result, nil
}

// GetAgentNamesForServer returns agent names assigned to a single MCP server (tenant-scoped).
func (r *GORMMCPServerRepository) GetAgentNamesForServer(ctx context.Context, serverID string) ([]string, error) {
	m, err := r.GetAgentNamesByServerIDs(ctx, []string{serverID})
	if err != nil {
		return nil, err
	}
	return m[serverID], nil
}
