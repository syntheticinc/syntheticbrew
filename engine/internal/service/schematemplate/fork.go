// Package schematemplate implements the "Use template" fork operation for
// the V2 schema template catalog (§2.2). Given a curated template from the
// `schema_templates` table, ForkService clones its Definition into
// tenant-owned rows in schemas + agents + agent_relations + triggers +
// capabilities in a single transaction. Forked rows have no FK back — the
// copy is independent of the catalog (catalog updates never touch existing
// forks).
//
// See docs/architecture/agent-first-runtime.md §2.2 and
// docs/plan/v2-cleanup-checklist.md "Commit Group L".
package schematemplate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// ErrTemplateNotFound is returned by Fork when the named template is not
// present in the catalog. Callers typically map this to HTTP 404.
var ErrTemplateNotFound = errors.New("schema template not found")

// ErrSchemaNameTaken is returned by Fork when the requested new schema name
// already exists. Schema names are globally unique in V2
// (`idx_schemas_tenant_name`), so duplicate names are rejected up front.
// Callers typically map this to HTTP 409.
var ErrSchemaNameTaken = errors.New("schema name already taken")

// ErrInvalidTemplate is returned when the loaded template definition is
// self-inconsistent (missing entry agent, dangling relation endpoint,
// empty required field). The catalog seeder should catch these at boot,
// but we double-check at fork time so a corrupt row never writes partial
// data.
var ErrInvalidTemplate = errors.New("invalid schema template")

// ForkedSchema is the lightweight result of a successful fork, returned to
// the caller so it can navigate to the new schema detail page.
type ForkedSchema struct {
	SchemaID   string
	SchemaName string
	AgentIDs   map[string]string // logical name → newly minted uuid
}

// TemplateReader is the consumer-side interface ForkService needs to load a
// catalog template by name. Implemented by
// configrepo.GORMSchemaTemplateRepository.
type TemplateReader interface {
	GetByName(ctx context.Context, name string) (*domain.SchemaTemplate, error)
}

// CreateGuard admits or rejects the fork's schema creation before any row is
// written — the same plugin quota seam the REST handler and the admin tools
// pass through. Satisfied structurally by pkg/plugin.Plugin.
type CreateGuard interface {
	OnSchemaCreate(ctx context.Context, tenantID string, n int) error
}

// ForkService clones a catalog template into tenant-owned runtime rows.
// One instance is safe to reuse — state-free apart from the injected DB
// handle.
type ForkService struct {
	db    *gorm.DB
	repo  TemplateReader
	guard CreateGuard
}

// NewForkService constructs a ForkService backed by the given DB handle,
// template reader, and creation guard.
func NewForkService(db *gorm.DB, repo TemplateReader, guard CreateGuard) *ForkService {
	return &ForkService{db: db, repo: repo, guard: guard}
}

// Fork clones `templateName` into a new schema called `newSchemaName`,
// scoped to the tenant carried in `ctx` (matches the rest of the engine's
// services — tenant_id is read from context, never passed as an argument).
// The new schema, agents, capabilities and relations inherit that tenant_id
// so subsequent tenant-filtered queries (`GET /schemas`, `GET /agents`) can
// see the freshly forked rows.
//
// All writes happen inside a single transaction; any error rolls back the
// entire fork so a failed attempt never leaves half-built rows.
func (s *ForkService) Fork(ctx context.Context, templateName, newSchemaName string) (*ForkedSchema, error) {
	tenantID := domain.TenantIDFromContext(ctx)
	newSchemaName = strings.TrimSpace(newSchemaName)
	if newSchemaName == "" {
		return nil, fmt.Errorf("schema name is required")
	}
	templateName = strings.TrimSpace(templateName)
	if templateName == "" {
		return nil, fmt.Errorf("template name is required")
	}

	tmpl, err := s.repo.GetByName(ctx, templateName)
	if err != nil {
		return nil, fmt.Errorf("load template %q: %w", templateName, err)
	}
	if tmpl == nil {
		return nil, ErrTemplateNotFound
	}

	if err := validateDefinition(tmpl.Definition); err != nil {
		return nil, fmt.Errorf("template %q: %w", templateName, err)
	}

	// Quota seam, before any row is written: a fork creates exactly one
	// schema. Checked outside the transaction — a rejection costs nothing.
	if err := s.guard.OnSchemaCreate(ctx, tenantID, 1); err != nil {
		if errors.Is(err, plugin.ErrSchemaQuotaExceeded) {
			return nil, pkgerrors.UsageLimited("schema limit reached: upgrade your plan or remove a schema to free a slot")
		}
		return nil, fmt.Errorf("schema creation admission: %w", err)
	}

	var result ForkedSchema
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Guard against a duplicate schema name up front — the unique index
		// (idx_schemas_tenant_name) would catch it on insert, but the early
		// check gives a clean typed error for the HTTP 409 path. Scoping by
		// tenant_id mirrors the index, so two tenants can each fork the
		// same template name independently.
		var existing int64
		q := tx.Model(&models.SchemaModel{}).Where("name = ?", newSchemaName)
		if tenantID != "" {
			q = q.Where("tenant_id = ?", tenantID)
		}
		if err := q.Count(&existing).Error; err != nil {
			return fmt.Errorf("check schema name: %w", err)
		}
		if existing > 0 {
			return ErrSchemaNameTaken
		}

		forked, err := s.forkInTx(tx, tenantID, tmpl, newSchemaName)
		if err != nil {
			return err
		}
		result = forked
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// forkInTx performs the actual row-creation inside an open transaction. All
// errors propagate to the outer Transaction closure → rollback. Every
// row written here carries `tenantID` so tenant-filtered listings can see
// the fork (the column default is the single-tenant sentinel UUID, which
// would otherwise hide the rows from a real tenant).
func (s *ForkService) forkInTx(tx *gorm.DB, tenantID string, tmpl *domain.SchemaTemplate, newSchemaName string) (ForkedSchema, error) {
	def := tmpl.Definition

	// 1. Create the schema row.
	schema := models.SchemaModel{
		TenantID:    tenantID,
		Name:        newSchemaName,
		Description: tmpl.Description,
	}
	if err := tx.Create(&schema).Error; err != nil {
		return ForkedSchema{}, fmt.Errorf("create schema: %w", err)
	}

	// 2. Create each agent with a freshly namespaced name. Agent names are
	//    globally unique (§5.1 — agents are a global library), so we
	//    prefix with the schema name to avoid collisions across forks of
	//    the same template.
	agentIDByLogical := make(map[string]string, len(def.Agents))
	agentNameByLogical := make(map[string]string, len(def.Agents))
	for _, a := range def.Agents {
		newAgentName := fmt.Sprintf("%s__%s", newSchemaName, a.Name)
		model := models.AgentModel{
			TenantID:        tenantID,
			Name:            newAgentName,
			SystemPrompt:    a.SystemPrompt,
			Lifecycle:       "persistent",
			ToolExecution:   "sequential",
			MaxContextSize:  16000,
			MaxTurnDuration: 120,
		}
		if err := tx.Create(&model).Error; err != nil {
			return ForkedSchema{}, fmt.Errorf("create agent %q: %w", newAgentName, err)
		}
		agentIDByLogical[a.Name] = model.ID
		agentNameByLogical[a.Name] = newAgentName

		// 3. Attach capabilities to the newly created agent.
		for _, cap := range a.Capabilities {
			configJSON := "{}"
			if len(cap.Config) > 0 {
				raw, err := json.Marshal(cap.Config)
				if err != nil {
					return ForkedSchema{}, fmt.Errorf("marshal capability %q config: %w", cap.Type, err)
				}
				configJSON = string(raw)
			}
			capModel := models.CapabilityModel{
				TenantID: tenantID,
				AgentID:  model.ID,
				Type:     cap.Type,
				Config:   configJSON,
				Enabled:  true,
			}
			if err := tx.Create(&capModel).Error; err != nil {
				return ForkedSchema{}, fmt.Errorf("attach capability %q to agent %q: %w", cap.Type, newAgentName, err)
			}
		}
	}

	// 4. Resolve the entry agent. Validation has already asserted it
	//    exists in the agents list.
	entryAgentID := agentIDByLogical[def.EntryAgentName]

	// Set the entry agent on the schema.
	if entryAgentID != "" {
		if err := tx.Model(&models.SchemaModel{}).Where("id = ?", schema.ID).
			Update("entry_agent_id", entryAgentID).Error; err != nil {
			return ForkedSchema{}, fmt.Errorf("set entry agent: %w", err)
		}
	}

	// 5. Create delegation relations. Each relation stores source/target
	//    agent UUIDs (Q.5: was name-based, now id-based).
	for _, rel := range def.Relations {
		sourceID, ok := agentIDByLogical[rel.Source]
		if !ok {
			return ForkedSchema{}, fmt.Errorf("relation source %q: %w", rel.Source, ErrInvalidTemplate)
		}
		targetID, ok := agentIDByLogical[rel.Target]
		if !ok {
			return ForkedSchema{}, fmt.Errorf("relation target %q: %w", rel.Target, ErrInvalidTemplate)
		}
		relModel := models.AgentRelationModel{
			TenantID:      tenantID,
			SchemaID:      schema.ID,
			SourceAgentID: sourceID,
			TargetAgentID: targetID,
			Config:        "{}",
		}
		if err := tx.Create(&relModel).Error; err != nil {
			return ForkedSchema{}, fmt.Errorf("create relation %s → %s: %w", rel.Source, rel.Target, err)
		}
	}

	// 6. Enable chat on the forked schema. Templates are authored to be
	//    chat-facing; a forked schema always has chat_enabled=true and a
	//    resolved entry_agent_id. Schedulers (cron/webhook) are tenant-owned
	//    and call POST /api/v1/schemas/{name}/chat directly (engine 1.1.0+).
	if err := tx.Model(&models.SchemaModel{}).Where("id = ?", schema.ID).
		Update("chat_enabled", true).Error; err != nil {
		return ForkedSchema{}, fmt.Errorf("enable chat on forked schema: %w", err)
	}

	return ForkedSchema{
		SchemaID:   schema.ID,
		SchemaName: schema.Name,
		AgentIDs:   agentIDByLogical,
	}, nil
}

// validateDefinition asserts self-consistency of the template before the
// fork transaction starts. Any failure short-circuits with
// ErrInvalidTemplate so the DB never sees partial writes.
func validateDefinition(def domain.SchemaTemplateDefinition) error {
	if len(def.Agents) == 0 {
		return fmt.Errorf("no agents defined: %w", ErrInvalidTemplate)
	}
	if strings.TrimSpace(def.EntryAgentName) == "" {
		return fmt.Errorf("entry_agent_name is empty: %w", ErrInvalidTemplate)
	}

	names := make(map[string]struct{}, len(def.Agents))
	for _, a := range def.Agents {
		if strings.TrimSpace(a.Name) == "" {
			return fmt.Errorf("agent with empty name: %w", ErrInvalidTemplate)
		}
		if _, dup := names[a.Name]; dup {
			return fmt.Errorf("duplicate agent name %q: %w", a.Name, ErrInvalidTemplate)
		}
		names[a.Name] = struct{}{}
	}
	if _, ok := names[def.EntryAgentName]; !ok {
		return fmt.Errorf("entry agent %q not in agents list: %w", def.EntryAgentName, ErrInvalidTemplate)
	}

	for _, rel := range def.Relations {
		if _, ok := names[rel.Source]; !ok {
			return fmt.Errorf("relation source %q not in agents list: %w", rel.Source, ErrInvalidTemplate)
		}
		if _, ok := names[rel.Target]; !ok {
			return fmt.Errorf("relation target %q not in agents list: %w", rel.Target, ErrInvalidTemplate)
		}
	}

	return nil
}
