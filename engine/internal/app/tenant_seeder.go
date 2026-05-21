package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
)

// engineTenantSeeder is the concrete plugin.TenantSeeder the engine wires into
// the plugin at startup. It uses the engine's real repositories so provisioning
// goes through the same code path as normal user-driven schema creation —
// tenant scoping, validation, timestamps, etc. remain consistent.
//
// Seeds per-tenant:
//   1. "my-workspace" default schema (chat disabled until the user configures it)
//   2. builder-assistant system agent (editable by the user — deleting and
//      re-seeding via POST /admin/builder-assistant/restore is supported).
//      Model assignment is deferred: if no models exist yet the agent is
//      seeded without a model, and modelServiceHTTPAdapter.CreateModel picks
//      up the first user-created model to back-fill it. This matches the
//      dogfooding story: the AI Builder runs on the same engine the user
//      configures, so every tenant gets its own editable copy.
type engineTenantSeeder struct {
	schemaRepo *configrepo.GORMSchemaRepository
	db         *gorm.DB
}

// SeedTenant satisfies plugin.TenantSeeder. Runs under a context scoped to the
// new tenant so repo-level tenant stamping picks the right tenant_id.
func (s *engineTenantSeeder) SeedTenant(ctx context.Context, tenantID, plan string) error {
	if tenantID == "" {
		return fmt.Errorf("tenant_id is required")
	}
	if s.schemaRepo == nil {
		return fmt.Errorf("schema repository not configured")
	}
	if s.db == nil {
		return fmt.Errorf("db handle not configured")
	}

	// Scope the context to the new tenant so the repository stamps
	// tenant_id=<new> on inserted rows.
	ctx = domain.WithTenantID(ctx, tenantID)

	record := &configrepo.SchemaRecord{
		Name:        "my-workspace",
		Description: "Default workspace created on signup",
		ChatEnabled: false,
	}
	if err := s.schemaRepo.Create(ctx, record); err != nil {
		// Provisioning is idempotent — treat duplicate-name errors as a
		// signal that the tenant was seeded before and fall through to
		// builder-assistant rebinding. Without this, an existing tenant
		// that was stuck on an empty-api-key binding (2026-04-23 chat-401
		// bug) never gets rebound on subsequent engine-token handoffs.
		lower := strings.ToLower(err.Error())
		alreadyExists := strings.Contains(lower, "duplicate") ||
			strings.Contains(lower, "already exists") ||
			strings.Contains(lower, "unique constraint")
		if !alreadyExists {
			return fmt.Errorf("seed default schema: %w", err)
		}
		slog.InfoContext(ctx, "default schema already exists — continuing with builder-assistant rebinding", "tenant_id", tenantID)
	}

	// Seed the AI Builder agent for this tenant. seedBuilderAssistant is
	// tolerant of missing models (leaves ModelName empty) and idempotent
	// (updates if already present). Always reached on every provisioning
	// call so a stuck tenant can recover once the user adds a real model.
	seedBuilderAssistant(ctx, s.db)

	// Seed the builder-schema so admin-assistant chat has a schema to
	// resolve for this tenant. Without it, POST /admin/assistant/chat
	// returns "no schema found" for new tenants.
	seedBuilderSchema(ctx, s.db)
	return nil
}
