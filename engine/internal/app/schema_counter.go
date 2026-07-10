package app

import (
	"context"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	pluginpkg "github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// UserSchemaCounter counts user-created schemas scoped by `tenant_id` from the
// context. Engine-managed system schemas are excluded so they do not consume
// the tenant's schema quota.
type UserSchemaCounter interface {
	CountUserSchemas(ctx context.Context) (int64, error)
}

// NewSchemaCounter returns a SchemaCounterFunc for plugin-provided limit
// middleware to enforce SchemasLimit. Empty tenant_id short-circuits to 0
// (the base single-tenant engine has no limit plugin wired). The count
// excludes engine-managed system schemas.
func NewSchemaCounter(repo UserSchemaCounter) pluginpkg.SchemaCounterFunc {
	return func(ctx context.Context, tenantID string) (int, error) {
		if tenantID == "" {
			return 0, nil
		}
		scoped := domain.WithTenantID(ctx, tenantID)
		count, err := repo.CountUserSchemas(scoped)
		if err != nil {
			return 0, fmt.Errorf("count schemas: %w", err)
		}
		return int(count), nil
	}
}
