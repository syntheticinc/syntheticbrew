package configrepo

import (
	"context"
	"errors"
	"fmt"

	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/persistence/models"
	"gorm.io/gorm"
)

// Sentinel errors for ReplaceAgentKBs/LinkAgent/UnlinkAgent tenant-isolation
// checks. Callers use errors.Is to map these to 404 at the HTTP layer without
// relying on string matching.
var (
	// ErrAgentNotInTenant means the referenced agent does not exist in the
	// tenant resolved from the context. The authoritative fix at the caller
	// is to surface a 404 — never a 500 — so a cross-tenant probe cannot
	// distinguish "agent does not exist" from "agent exists elsewhere".
	ErrAgentNotInTenant = errors.New("agent not found in tenant")

	// ErrKBsNotInTenant means one or more referenced knowledge bases do not
	// exist in the tenant resolved from the context. Same 404 mapping policy
	// as ErrAgentNotInTenant.
	ErrKBsNotInTenant = errors.New("one or more knowledge bases not found in tenant")
)

// GORMKnowledgeBaseRepository provides CRUD for knowledge bases and agent linking.
// Tenant isolation is applied via tenantScope(ctx) from base_repo.go.
type GORMKnowledgeBaseRepository struct {
	db *gorm.DB
}

// NewGORMKnowledgeBaseRepository creates a new repository.
func NewGORMKnowledgeBaseRepository(db *gorm.DB) *GORMKnowledgeBaseRepository {
	return &GORMKnowledgeBaseRepository{db: db}
}

// Create creates a new knowledge base, stamping tenant from context.
func (r *GORMKnowledgeBaseRepository) Create(ctx context.Context, kb *models.KnowledgeBase) error {
	kb.TenantID = tenantIDFromCtx(ctx)
	if err := r.db.WithContext(ctx).Create(kb).Error; err != nil {
		return fmt.Errorf("create knowledge base: %w", err)
	}
	return nil
}

// Update updates a knowledge base (tenant preserved from existing row).
func (r *GORMKnowledgeBaseRepository) Update(ctx context.Context, kb *models.KnowledgeBase) error {
	// Ensure the kb belongs to the current tenant before saving.
	var existing models.KnowledgeBase
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("id = ?", kb.ID).
		First(&existing).Error; err != nil {
		return fmt.Errorf("find knowledge base: %w", err)
	}
	kb.TenantID = existing.TenantID
	if err := r.db.WithContext(ctx).Save(kb).Error; err != nil {
		return fmt.Errorf("update knowledge base: %w", err)
	}
	return nil
}

// GetByID returns a knowledge base by ID (tenant-scoped).
func (r *GORMKnowledgeBaseRepository) GetByID(ctx context.Context, id string) (*models.KnowledgeBase, error) {
	var kb models.KnowledgeBase
	err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("id = ?", id).
		First(&kb).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get knowledge base: %w", err)
	}
	return &kb, nil
}

// GetKBIDByName resolves a KB name to its UUID within the caller's tenant.
//
// Used by handlers backing `/api/v1/knowledge-bases/{name}/...` routes —
// engine 1.1.0 migrated KB URLs from UUID-keyed to name-keyed for GitOps
// consumers. Returns gorm.ErrRecordNotFound when no row matches; resolver
// upstream maps that to ErrRefNotFound → handler returns 404. Method name
// matches the existing KBRefRepo consumer-side interface in resolvers.go.
func (r *GORMKnowledgeBaseRepository) GetKBIDByName(ctx context.Context, name string) (string, error) {
	var id string
	if err := r.db.WithContext(ctx).
		Raw("SELECT id FROM knowledge_bases WHERE name = ? AND tenant_id = ?", name, tenantIDFromCtx(ctx)).
		Scan(&id).Error; err != nil {
		return "", fmt.Errorf("get knowledge base id by name %q: %w", name, err)
	}
	if id == "" {
		return "", gorm.ErrRecordNotFound
	}
	return id, nil
}

// List returns all knowledge bases for the current tenant.
func (r *GORMKnowledgeBaseRepository) List(ctx context.Context) ([]models.KnowledgeBase, error) {
	var kbs []models.KnowledgeBase
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Order("created_at DESC").
		Find(&kbs).Error; err != nil {
		return nil, fmt.Errorf("list knowledge bases: %w", err)
	}
	return kbs, nil
}

// Delete removes a knowledge base and its join table entries (documents cascade via KB handler).
// Tenant-scoped — only removes rows that belong to the current tenant.
func (r *GORMKnowledgeBaseRepository) Delete(ctx context.Context, id string) error {
	// Remove agent links (link table has no tenant column; key is (kb_id, agent_id)
	// and kb_id is unique — safe to remove without tenant filter as long as we
	// only fall through to the kb delete when the kb itself is tenant-scoped).
	if err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ?", id).
		Delete(&models.KnowledgeBaseAgent{}).Error; err != nil {
		return fmt.Errorf("delete KB agent links: %w", err)
	}

	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("id = ?", id).
		Delete(&models.KnowledgeBase{}).Error; err != nil {
		return fmt.Errorf("delete knowledge base: %w", err)
	}
	return nil
}

// LinkAgent links an agent (by ID) to a knowledge base.
//
// The join table `knowledge_base_agents` has no `tenant_id` column, so the
// tenant invariant here is enforced at link-creation time: both the KB and the
// agent must belong to the tenant in the context. Without this check a caller
// could stitch together an arbitrary KB and an arbitrary agent (even across
// tenants) by passing the two UUIDs directly.
func (r *GORMKnowledgeBaseRepository) LinkAgent(ctx context.Context, kbID, agentID string) error {
	if err := r.verifyKBAndAgentInTenant(ctx, kbID, agentID); err != nil {
		return err
	}
	link := models.KnowledgeBaseAgent{
		KnowledgeBaseID: kbID,
		AgentID:         agentID,
	}
	if err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND agent_id = ?", kbID, agentID).
		FirstOrCreate(&link).Error; err != nil {
		return fmt.Errorf("link agent to KB: %w", err)
	}
	return nil
}

// UnlinkAgent removes the link between an agent and a knowledge base.
// Enforces that both sides belong to the current tenant for the same reason
// as LinkAgent — the join table has no tenant column.
func (r *GORMKnowledgeBaseRepository) UnlinkAgent(ctx context.Context, kbID, agentID string) error {
	if err := r.verifyKBAndAgentInTenant(ctx, kbID, agentID); err != nil {
		return err
	}
	if err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND agent_id = ?", kbID, agentID).
		Delete(&models.KnowledgeBaseAgent{}).Error; err != nil {
		return fmt.Errorf("unlink agent from KB: %w", err)
	}
	return nil
}

// verifyKBAndAgentInTenant ensures both the KB and the agent belong to the
// tenant resolved from ctx. Used by LinkAgent/UnlinkAgent because the join
// table carries no tenant_id of its own.
func (r *GORMKnowledgeBaseRepository) verifyKBAndAgentInTenant(ctx context.Context, kbID, agentID string) error {
	tenantID := tenantIDFromCtx(ctx)

	var kbCount int64
	if err := r.db.WithContext(ctx).
		Model(&models.KnowledgeBase{}).
		Where("id = ? AND tenant_id = ?", kbID, tenantID).
		Count(&kbCount).Error; err != nil {
		return fmt.Errorf("verify knowledge base tenant: %w", err)
	}
	if kbCount == 0 {
		return ErrKBsNotInTenant
	}

	var agentCount int64
	if err := r.db.WithContext(ctx).
		Model(&models.AgentModel{}).
		Where("id = ? AND tenant_id = ?", agentID, tenantID).
		Count(&agentCount).Error; err != nil {
		return fmt.Errorf("verify agent tenant: %w", err)
	}
	if agentCount == 0 {
		return ErrAgentNotInTenant
	}
	return nil
}

// ListLinkedAgentIDs returns agent IDs linked to a knowledge base.
func (r *GORMKnowledgeBaseRepository) ListLinkedAgentIDs(ctx context.Context, kbID string) ([]string, error) {
	var ids []string
	if err := r.db.WithContext(ctx).
		Model(&models.KnowledgeBaseAgent{}).
		Where("knowledge_base_id = ?", kbID).
		Pluck("agent_id", &ids).Error; err != nil {
		return nil, fmt.Errorf("list linked agents: %w", err)
	}
	return ids, nil
}

// ListKBsByAgentID returns knowledge base IDs linked to an agent (by UUID).
func (r *GORMKnowledgeBaseRepository) ListKBsByAgentID(ctx context.Context, agentID string) ([]string, error) {
	var kbIDs []string
	if err := r.db.WithContext(ctx).
		Model(&models.KnowledgeBaseAgent{}).
		Where("agent_id = ?", agentID).
		Pluck("knowledge_base_id", &kbIDs).Error; err != nil {
		return nil, fmt.Errorf("list KBs by agent: %w", err)
	}
	return kbIDs, nil
}

// ReplaceAgentKBs replaces the KB membership for the given agent with the
// exact list of kbIDs provided. Empty kbIDs means "unlink from all KBs".
// Bug 7: admin UI patches an agent with knowledge_base_ids and expects the
// M2M table to match exactly — this single call handles both additions
// (for previously-absent IDs) and removals (for previously-present IDs
// that are not in the new set).
//
// The operation is tenant-safe in the sense that every KB touched is
// verified to belong to the current tenant before any write; stray IDs
// from other tenants short-circuit with an error and no write is applied.
// This uses a short transaction so partial failure rolls back the whole
// membership change.
func (r *GORMKnowledgeBaseRepository) ReplaceAgentKBs(ctx context.Context, agentID string, kbIDs []string) error {
	tenantID := tenantIDFromCtx(ctx)

	// Verify agent belongs to the tenant — prevents stitching an agent to
	// a KB owned by a different tenant.
	var agentCount int64
	if err := r.db.WithContext(ctx).
		Model(&models.AgentModel{}).
		Where("id = ? AND tenant_id = ?", agentID, tenantID).
		Count(&agentCount).Error; err != nil {
		return fmt.Errorf("verify agent tenant: %w", err)
	}
	if agentCount == 0 {
		return ErrAgentNotInTenant
	}

	// Verify every incoming KB also belongs to the tenant. We do this in
	// a single IN-query so the cost stays O(1) round-trips regardless of
	// how many KBs were passed.
	if len(kbIDs) > 0 {
		var kbCount int64
		if err := r.db.WithContext(ctx).
			Model(&models.KnowledgeBase{}).
			Where("id IN ? AND tenant_id = ?", kbIDs, tenantID).
			Count(&kbCount).Error; err != nil {
			return fmt.Errorf("verify kb tenants: %w", err)
		}
		if int(kbCount) != len(uniqueStrings(kbIDs)) {
			return ErrKBsNotInTenant
		}
	}

	// Replace the agent's KB set inside a transaction so a mid-way error
	// does not leave the membership half-updated. PATCH with []=unlink all
	// is a supported case — both the delete and a no-op insert are OK.
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("agent_id = ?", agentID).
			Delete(&models.KnowledgeBaseAgent{}).Error; err != nil {
			return fmt.Errorf("clear agent KB links: %w", err)
		}
		if len(kbIDs) == 0 {
			return nil
		}
		rows := make([]models.KnowledgeBaseAgent, 0, len(kbIDs))
		seen := make(map[string]struct{}, len(kbIDs))
		for _, kbID := range kbIDs {
			if _, dup := seen[kbID]; dup {
				continue
			}
			seen[kbID] = struct{}{}
			rows = append(rows, models.KnowledgeBaseAgent{
				KnowledgeBaseID: kbID,
				AgentID:         agentID,
			})
		}
		if err := tx.Create(&rows).Error; err != nil {
			return fmt.Errorf("insert agent KB links: %w", err)
		}
		return nil
	})
}

// uniqueStrings returns the input with duplicates removed (order preserved).
// Used by ReplaceAgentKBs to make the tenant-count comparison correct when
// callers pass the same KB twice.
func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// ListKBsByAgentName resolves agent name → ID (tenant-scoped), then returns linked KB IDs.
// Implements KnowledgeKBResolver interface for builtin_tool_store.
func (r *GORMKnowledgeBaseRepository) ListKBsByAgentName(ctx context.Context, agentName string) ([]string, error) {
	tenantID := tenantIDFromCtx(ctx)

	var agentID string
	if err := r.db.WithContext(ctx).
		Raw("SELECT id FROM agents WHERE name = ? AND tenant_id = ?", agentName, tenantID).
		Scan(&agentID).Error; err != nil || agentID == "" {
		return nil, nil // agent not found — no KBs
	}
	return r.ListKBsByAgentID(ctx, agentID)
}

// GetKBsWithEmbeddingModel returns knowledge bases with their embedding model info for an agent (tenant-scoped).
// Used by the knowledge_search tool to resolve per-KB embedding models.
func (r *GORMKnowledgeBaseRepository) GetKBsWithEmbeddingModel(ctx context.Context, agentName string) ([]models.KnowledgeBase, error) {
	kbIDs, err := r.ListKBsByAgentName(ctx, agentName)
	if err != nil || len(kbIDs) == 0 {
		return nil, err
	}
	var kbs []models.KnowledgeBase
	if err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("id IN ?", kbIDs).
		Find(&kbs).Error; err != nil {
		return nil, fmt.Errorf("get KBs with embedding model: %w", err)
	}
	return kbs, nil
}
