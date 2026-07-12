package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	deliveryhttp "github.com/syntheticinc/syntheticbrew/internal/delivery/http"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/schemacreate"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
	"gorm.io/gorm"
)

// resolveAgentNameByID resolves an agent UUID to its name via a raw DB query.
// Returns an empty string when the ID is nil or the agent is not found.
func (a *schemaServiceHTTPAdapter) resolveAgentNameByID(ctx context.Context, agentID *string) string {
	if agentID == nil || *agentID == "" {
		return ""
	}
	var name string
	_ = a.db.WithContext(ctx).Raw("SELECT name FROM agents WHERE id = ? LIMIT 1", *agentID).Scan(&name).Error
	return name
}

// countAgentsInSchema returns the number of distinct agents linked to the schema.
// Membership mirrors deriveAgentNames: entry agent + all relation endpoints.
func (a *schemaServiceHTTPAdapter) countAgentsInSchema(ctx context.Context, schemaID string) int {
	var count int64
	_ = a.db.WithContext(ctx).Raw(`
		SELECT COUNT(DISTINCT agent_id) FROM (
			SELECT entry_agent_id AS agent_id FROM schemas WHERE id = ? AND entry_agent_id IS NOT NULL
			UNION
			SELECT source_agent_id AS agent_id FROM agent_relations WHERE schema_id = ?
			UNION
			SELECT target_agent_id AS agent_id FROM agent_relations WHERE schema_id = ?
		) members`, schemaID, schemaID, schemaID).Scan(&count).Error
	return int(count)
}

// schemaServiceHTTPAdapter bridges GORMSchemaRepository to the http.SchemaService interface.
// Creation goes through the guarded schemacreate usecase (the shared seam for
// every facade); reads/updates/deletes stay on the repository.
type schemaServiceHTTPAdapter struct {
	repo    *configrepo.GORMSchemaRepository
	db      *gorm.DB
	creator *schemacreate.Usecase
}

func (a *schemaServiceHTTPAdapter) ListSchemas(ctx context.Context) ([]deliveryhttp.SchemaInfo, error) {
	records, err := a.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list schemas: %w", err)
	}

	result := make([]deliveryhttp.SchemaInfo, 0, len(records))
	for _, r := range records {
		result = append(result, deliveryhttp.SchemaInfo{
			ID:              r.ID,
			Name:            r.Name,
			Description:     r.Description,
			Agents:          r.AgentNames,
			IsSystem:        r.IsSystem,
			EntryAgentName:  a.resolveAgentNameByID(ctx, r.EntryAgentID),
			AgentsCount:     a.countAgentsInSchema(ctx, r.ID),
			ChatEnabled:     r.ChatEnabled,
			ChatLastFiredAt: r.ChatLastFiredAt,
			CreatedAt:       r.CreatedAt,
		})
	}
	return result, nil
}

func (a *schemaServiceHTTPAdapter) GetSchema(ctx context.Context, id string) (*deliveryhttp.SchemaInfo, error) {
	record, err := a.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, pkgerrors.NotFound(fmt.Sprintf("schema not found: %s", id))
		}
		return nil, fmt.Errorf("get schema: %w", err)
	}

	return &deliveryhttp.SchemaInfo{
		ID:              record.ID,
		Name:            record.Name,
		Description:     record.Description,
		Agents:          record.AgentNames,
		IsSystem:        record.IsSystem,
		EntryAgentName:  a.resolveAgentNameByID(ctx, record.EntryAgentID),
		AgentsCount:     a.countAgentsInSchema(ctx, record.ID),
		ChatEnabled:     record.ChatEnabled,
		ChatLastFiredAt: record.ChatLastFiredAt,
		CreatedAt:       record.CreatedAt,
	}, nil
}

func (a *schemaServiceHTTPAdapter) CreateSchema(ctx context.Context, req deliveryhttp.CreateSchemaRequest) (*deliveryhttp.SchemaInfo, error) {
	entryAgentID := req.EntryAgentID
	if entryAgentID != nil && *entryAgentID == "" {
		entryAgentID = nil
	}
	if entryAgentID != nil {
		resolved, err := a.resolveEntryAgentRef(ctx, *entryAgentID)
		if err != nil {
			return nil, err
		}
		entryAgentID = &resolved
	}

	chatEnabled := false
	if req.ChatEnabled != nil {
		chatEnabled = *req.ChatEnabled
	}

	out, err := a.creator.Execute(ctx, schemacreate.Input{
		Name:         req.Name,
		Description:  req.Description,
		EntryAgentID: entryAgentID,
		ChatEnabled:  chatEnabled,
	})
	if err != nil {
		return nil, err
	}

	return &deliveryhttp.SchemaInfo{
		ID:          out.ID,
		Name:        out.Name,
		Description: out.Description,
		ChatEnabled: out.ChatEnabled,
		CreatedAt:   out.CreatedAt,
	}, nil
}

func (a *schemaServiceHTTPAdapter) UpdateSchema(ctx context.Context, id string, req deliveryhttp.UpdateSchemaRequest) error {
	existing, err := a.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return pkgerrors.NotFound(fmt.Sprintf("schema not found: %s", id))
		}
		return fmt.Errorf("load schema for update: %w", err)
	}

	record := &configrepo.SchemaRecord{
		Name:         existing.Name,
		Description:  existing.Description,
		EntryAgentID: existing.EntryAgentID,
		ChatEnabled:  existing.ChatEnabled,
	}
	if req.Name != nil {
		record.Name = *req.Name
	}
	if req.Description != nil {
		record.Description = *req.Description
	}
	if req.EntryAgentID != nil {
		if *req.EntryAgentID == "" {
			record.EntryAgentID = nil
		} else {
			resolved, err := a.resolveEntryAgentRef(ctx, *req.EntryAgentID)
			if err != nil {
				return err
			}
			if err := a.requireAgentInSchema(ctx, id, resolved); err != nil {
				return err
			}
			record.EntryAgentID = &resolved
		}
	}
	if req.ChatEnabled != nil {
		record.ChatEnabled = *req.ChatEnabled
	}

	if err := a.repo.Update(ctx, id, record); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return pkgerrors.NotFound(fmt.Sprintf("schema not found: %s", id))
		}
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") || strings.Contains(err.Error(), "UNIQUE constraint") {
			return pkgerrors.AlreadyExists(fmt.Sprintf("schema with name %q already exists", record.Name))
		}
		return fmt.Errorf("update schema: %w", err)
	}
	return nil
}

// PatchSchema applies only the non-nil fields in req to the existing schema.
// The implementation is identical to UpdateSchema — pointer-typed fields already
// preserve existing values when nil. The distinction between PUT and PATCH is
// enforced at the handler layer (PUT requires name; PATCH does not).
func (a *schemaServiceHTTPAdapter) PatchSchema(ctx context.Context, id string, req deliveryhttp.UpdateSchemaRequest) error {
	return a.UpdateSchema(ctx, id, req)
}

func (a *schemaServiceHTTPAdapter) DeleteSchema(ctx context.Context, id string) error {
	if err := a.repo.Delete(ctx, id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return pkgerrors.NotFound(fmt.Sprintf("schema not found: %s", id))
		}
		// A schema that still has chat sessions (or other child rows) trips a
		// foreign-key constraint. That is a foreseeable conflict, not a server
		// fault: return a clean 409 without leaking the raw constraint/SQLSTATE.
		if isForeignKeyViolation(err) {
			return pkgerrors.Conflict("cannot delete schema: it still has chat sessions or other dependents — remove them first")
		}
		slog.ErrorContext(ctx, "delete schema failed", "schema_id", id, "error", err)
		return pkgerrors.New(pkgerrors.CodeInternal, "failed to delete schema")
	}
	return nil
}

// isForeignKeyViolation reports whether err is a Postgres foreign-key violation
// (SQLSTATE 23503) — a child row still references the record being deleted.
// Kept constraint-name-agnostic: any FK block on delete means "has dependents".
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// resolveEntryAgentRef returns the agent UUID for a name or UUID reference.
// UUID values pass through verbatim; names are resolved via a raw DB lookup.
func (a *schemaServiceHTTPAdapter) resolveEntryAgentRef(ctx context.Context, ref string) (string, error) {
	if isUUID(ref) {
		return ref, nil
	}
	var id string
	if err := a.db.WithContext(ctx).Raw("SELECT id FROM agents WHERE name = ? LIMIT 1", ref).Scan(&id).Error; err != nil || id == "" {
		return "", pkgerrors.InvalidInput(fmt.Sprintf("agent not found: %s", ref))
	}
	return id, nil
}

// Entry agent must appear in the schema's agent_relations (source or target).
// Fresh schemas with no relations get a pass — first entry agent is unconstrained.
func (a *schemaServiceHTTPAdapter) requireAgentInSchema(ctx context.Context, schemaID, agentID string) error {
	var totalRelations int64
	if err := a.db.WithContext(ctx).
		Raw(`SELECT COUNT(*) FROM agent_relations WHERE schema_id = ?`, schemaID).
		Scan(&totalRelations).Error; err != nil {
		return fmt.Errorf("count schema relations: %w", err)
	}
	if totalRelations == 0 {
		return nil
	}

	var memberCount int64
	if err := a.db.WithContext(ctx).Raw(
		`SELECT COUNT(*) FROM agent_relations
		 WHERE schema_id = ? AND (source_agent_id = ? OR target_agent_id = ?)`,
		schemaID, agentID, agentID,
	).Scan(&memberCount).Error; err != nil {
		return fmt.Errorf("verify schema membership: %w", err)
	}
	if memberCount == 0 {
		return pkgerrors.InvalidInput(fmt.Sprintf("agent %s is not a member of schema %s — add a delegation relation before assigning it as entry", agentID, schemaID))
	}
	return nil
}

// ListSchemaAgents returns the derived membership list for a schema (V2:
// union of source/target agents in agent_relations — see
// docs/architecture/agent-first-runtime.md §2.1).
func (a *schemaServiceHTTPAdapter) ListSchemaAgents(ctx context.Context, schemaID string) ([]string, error) {
	names, err := a.repo.ListAgents(ctx, schemaID)
	if err != nil {
		return nil, fmt.Errorf("list schema agents: %w", err)
	}
	if names == nil {
		return []string{}, nil
	}
	return names, nil
}

// agentRelationLister is the minimal consumer-side contract the relation
// adapter needs from an agent-relation repository. Narrowing this from the
// concrete *GORMAgentRelationRepository lets tests inject a fake in-memory
// store for cycle-check integration coverage without spinning up a DB.
//
// Only List + GetByID + Create + Update + Delete are in the contract — these
// are the exact calls CreateAgentRelation/UpdateAgentRelation/etc. make on
// the repository today.
type agentRelationLister interface {
	List(ctx context.Context, schemaID string) ([]configrepo.AgentRelationRecord, error)
	GetByID(ctx context.Context, id string) (*configrepo.AgentRelationRecord, error)
	Create(ctx context.Context, record *configrepo.AgentRelationRecord) error
	Update(ctx context.Context, id string, record *configrepo.AgentRelationRecord) error
	Delete(ctx context.Context, id string) error
}

// agentResolver narrows GORMAgentRepository to the single call the relation
// adapter actually uses (name → UUID resolution). Consumer-side interface —
// the relation adapter is the sole caller, so it owns the contract shape.
type agentResolver interface {
	GetByName(ctx context.Context, name string) (*configrepo.AgentRecord, error)
}

// schemaTenantChecker is the consumer-side contract the relation adapter needs
// from the schema repo for SCC-02 (tenant-ownership) checks and the entry-agent
// auto-assignment side-effect in CreateAgentRelation.
type schemaTenantChecker interface {
	GetByID(ctx context.Context, id string) (*configrepo.SchemaRecord, error)
	Update(ctx context.Context, id string, record *configrepo.SchemaRecord) error
}

// agentRelationServiceHTTPAdapter bridges GORMAgentRelationRepository to the
// http.AgentRelationService interface.
//
// agentRepo is used to resolve agent names to UUIDs — the API accepts either
// form in source/target fields so admin UI can work directly with agent names.
// schemaRepo is used to verify schema ownership (SCC-02 tenant isolation) before
// any operation that takes a schemaID parameter.
//
// Fields are typed against consumer-side interfaces (agentRelationLister,
// agentResolver, schemaTenantChecker) so tests can substitute fakes for the
// cycle-detection integration coverage without a real DB. The constructor
// still accepts the concrete types wired in server.go so no call sites change.
type agentRelationServiceHTTPAdapter struct {
	repo       agentRelationLister
	agentRepo  agentResolver
	schemaRepo schemaTenantChecker
	db         *gorm.DB
}

// resolveNameByID resolves an agent UUID to its name via a raw DB query.
// Returns the UUID unchanged if the agent is not found (safe fallback) or if
// db is nil (unit-test wiring where name-resolution isn't exercised).
func (a *agentRelationServiceHTTPAdapter) resolveNameByID(ctx context.Context, id string) string {
	if id == "" {
		return ""
	}
	if a.db == nil {
		return id
	}
	var name string
	if err := a.db.WithContext(ctx).Raw("SELECT name FROM agents WHERE id = ? LIMIT 1", id).Scan(&name).Error; err != nil || name == "" {
		return id
	}
	return name
}

// resolveAgentRef returns the agent UUID for a name or UUID reference.
// UUIDs pass through verbatim. Names are looked up via agentRepo.
// Returns InvalidInput error for unknown names so the caller can surface 400.
func (a *agentRelationServiceHTTPAdapter) resolveAgentRef(ctx context.Context, ref string) (string, error) {
	if ref == "" {
		return "", pkgerrors.InvalidInput("agent reference is empty")
	}
	if isUUID(ref) {
		return ref, nil
	}
	rec, err := a.agentRepo.GetByName(ctx, ref)
	if err != nil || rec == nil {
		return "", pkgerrors.InvalidInput(fmt.Sprintf("agent not found: %s", ref))
	}
	return rec.ID, nil
}

// isUUID returns true for canonical 8-4-4-4-12 hex strings.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
				return false
			}
		}
	}
	return true
}

func (a *agentRelationServiceHTTPAdapter) ListAgentRelations(ctx context.Context, schemaID string) ([]deliveryhttp.AgentRelationInfo, error) {
	// SCC-02: verify the schema belongs to the requesting tenant before returning
	// its relations. schemaRepo.GetByID is tenant-scoped, so it returns
	// gorm.ErrRecordNotFound when the schema belongs to a different tenant.
	if _, err := a.schemaRepo.GetByID(ctx, schemaID); err != nil {
		return nil, pkgerrors.NotFound(fmt.Sprintf("schema not found: %s", schemaID))
	}

	records, err := a.repo.List(ctx, schemaID)
	if err != nil {
		return nil, fmt.Errorf("list agent relations: %w", err)
	}

	result := make([]deliveryhttp.AgentRelationInfo, 0, len(records))
	for _, r := range records {
		result = append(result, deliveryhttp.AgentRelationInfo{
			ID:            r.ID,
			SchemaID:      r.SchemaID,
			SourceAgentID: a.resolveNameByID(ctx, r.SourceAgentID),
			TargetAgentID: a.resolveNameByID(ctx, r.TargetAgentID),
			Config:        r.Config,
		})
	}
	return result, nil
}

func (a *agentRelationServiceHTTPAdapter) GetAgentRelation(ctx context.Context, id string) (*deliveryhttp.AgentRelationInfo, error) {
	record, err := a.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, pkgerrors.NotFound(fmt.Sprintf("agent relation not found: %s", id))
		}
		return nil, fmt.Errorf("get agent relation: %w", err)
	}

	return &deliveryhttp.AgentRelationInfo{
		ID:            record.ID,
		SchemaID:      record.SchemaID,
		SourceAgentID: a.resolveNameByID(ctx, record.SourceAgentID),
		TargetAgentID: a.resolveNameByID(ctx, record.TargetAgentID),
		Config:        record.Config,
	}, nil
}

func (a *agentRelationServiceHTTPAdapter) CreateAgentRelation(ctx context.Context, schemaID string, req deliveryhttp.CreateAgentRelationRequest) (*deliveryhttp.AgentRelationInfo, error) {
	// SCC-02: verify the schema belongs to the requesting tenant before creating
	// a relation under it. Capture the record for entry-agent auto-assignment below.
	existingSchema, err := a.schemaRepo.GetByID(ctx, schemaID)
	if err != nil {
		return nil, pkgerrors.NotFound(fmt.Sprintf("schema not found: %s", schemaID))
	}

	sourceID, err := a.resolveAgentRef(ctx, req.Source)
	if err != nil {
		return nil, err
	}
	targetID, err := a.resolveAgentRef(ctx, req.Target)
	if err != nil {
		return nil, err
	}
	if sourceID == targetID {
		return nil, pkgerrors.InvalidInput("source and target must be different agents")
	}

	if err := a.checkNoCycle(ctx, schemaID, sourceID, targetID); err != nil {
		return nil, err
	}

	record := &configrepo.AgentRelationRecord{
		SchemaID:      schemaID,
		SourceAgentID: sourceID,
		TargetAgentID: targetID,
		Config:        req.Config,
	}
	if err := a.repo.Create(ctx, record); err != nil {
		if isAgentRelationFKViolation(err) {
			return nil, pkgerrors.NotFound("source or target agent no longer exists (deleted concurrently)")
		}
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") || strings.Contains(err.Error(), "UNIQUE constraint") {
			return nil, pkgerrors.AlreadyExists("agent relation between these agents already exists in this schema")
		}
		return nil, fmt.Errorf("create agent relation: %w", err)
	}

	// Auto-set entry_agent_id to the source agent when the schema has none.
	// This makes the canvas work correctly for schemas created without an explicit entry agent.
	if existingSchema.EntryAgentID == nil && sourceID != "" {
		updated := &configrepo.SchemaRecord{
			Name:         existingSchema.Name,
			Description:  existingSchema.Description,
			EntryAgentID: &sourceID,
			ChatEnabled:  existingSchema.ChatEnabled,
		}
		_ = a.schemaRepo.Update(ctx, schemaID, updated)
	}

	return &deliveryhttp.AgentRelationInfo{
		ID:            record.ID,
		SchemaID:      record.SchemaID,
		SourceAgentID: a.resolveNameByID(ctx, record.SourceAgentID),
		TargetAgentID: a.resolveNameByID(ctx, record.TargetAgentID),
		Config:        record.Config,
	}, nil
}

func (a *agentRelationServiceHTTPAdapter) UpdateAgentRelation(ctx context.Context, id string, req deliveryhttp.CreateAgentRelationRequest) error {
	sourceID, err := a.resolveAgentRef(ctx, req.Source)
	if err != nil {
		return err
	}
	targetID, err := a.resolveAgentRef(ctx, req.Target)
	if err != nil {
		return err
	}
	if sourceID == targetID {
		return pkgerrors.InvalidInput("source and target must be different agents")
	}

	// schema_id is needed to scope the cycle check; excluding `id` avoids a
	// self-report when the update doesn't change the edge endpoints.
	existing, err := a.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return pkgerrors.NotFound(fmt.Sprintf("agent relation not found: %s", id))
		}
		return fmt.Errorf("load agent relation for cycle check: %w", err)
	}
	if err := a.checkNoCycleExcluding(ctx, existing.SchemaID, sourceID, targetID, id); err != nil {
		return err
	}

	record := &configrepo.AgentRelationRecord{
		SourceAgentID: sourceID,
		TargetAgentID: targetID,
		Config:        req.Config,
	}
	if err := a.repo.Update(ctx, id, record); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return pkgerrors.NotFound(fmt.Sprintf("agent relation not found: %s", id))
		}
		return fmt.Errorf("update agent relation: %w", err)
	}
	return nil
}

// checkNoCycle returns an InvalidInput error when adding the edge
// source→target to the schema's existing delegation graph would close a
// cycle. A cycle is closed iff there is already a path target→…→source.
func (a *agentRelationServiceHTTPAdapter) checkNoCycle(ctx context.Context, schemaID, sourceID, targetID string) error {
	return a.checkNoCycleExcluding(ctx, schemaID, sourceID, targetID, "")
}

// checkNoCycleExcluding is the variant used by updates: it ignores a specific
// existing relation ID when building the graph so that re-saving an edge
// does not falsely self-report a cycle through itself.
func (a *agentRelationServiceHTTPAdapter) checkNoCycleExcluding(ctx context.Context, schemaID, sourceID, targetID, excludeID string) error {
	// Self-loop is caught by the caller, but belt-and-braces:
	if sourceID == targetID {
		return pkgerrors.InvalidInput("source and target must be different agents")
	}

	existing, err := a.repo.List(ctx, schemaID)
	if err != nil {
		return fmt.Errorf("list existing agent relations: %w", err)
	}

	// Build an adjacency list of current edges (excluding the one being
	// updated, if any) and probe reachability from target back to source.
	adj := make(map[string][]string, len(existing))
	for _, r := range existing {
		if excludeID != "" && r.ID == excludeID {
			continue
		}
		adj[r.SourceAgentID] = append(adj[r.SourceAgentID], r.TargetAgentID)
	}

	if reachable(adj, targetID, sourceID) {
		return pkgerrors.InvalidInput("circular delegation: adding this edge would close a cycle")
	}
	return nil
}

// reachable reports whether dst is reachable from src in the directed graph adj.
// BFS; stops as soon as dst is dequeued. O(V+E).
func reachable(adj map[string][]string, src, dst string) bool {
	if src == dst {
		return true
	}
	visited := map[string]struct{}{src: {}}
	queue := []string{src}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range adj[cur] {
			if next == dst {
				return true
			}
			if _, seen := visited[next]; seen {
				continue
			}
			visited[next] = struct{}{}
			queue = append(queue, next)
		}
	}
	return false
}

func (a *agentRelationServiceHTTPAdapter) DeleteAgentRelation(ctx context.Context, id string) error {
	if err := a.repo.Delete(ctx, id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return pkgerrors.NotFound(fmt.Sprintf("agent relation not found: %s", id))
		}
		return fmt.Errorf("delete agent relation: %w", err)
	}
	return nil
}

// isAgentRelationFKViolation reports whether err is a Postgres FK violation on
// the agent_id columns of agent_relations — the only FK that fires when an
// agent is deleted between resolve-by-name and INSERT. Constraint names
// fk_agent_relations_source_agent_id / fk_agent_relations_target_agent_id are
// owned by migration 001.
func isAgentRelationFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		return false
	}
	return pgErr.ConstraintName == "fk_agent_relations_source_agent_id" ||
		pgErr.ConstraintName == "fk_agent_relations_target_agent_id"
}

// agentSchemaListerHTTPAdapter bridges GORMSchemaRepository to the http.AgentSchemaLister interface.
type agentSchemaListerHTTPAdapter struct {
	repo *configrepo.GORMSchemaRepository
}

func (a *agentSchemaListerHTTPAdapter) ListSchemasForAgent(ctx context.Context, agentName string) ([]string, error) {
	return a.repo.ListSchemasForAgent(ctx, agentName)
}
