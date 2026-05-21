package app

import (
	"context"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	pluginpkg "github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// SchemaListerByTenant lists schemas scoped by `tenant_id` from the context.
type SchemaListerByTenant interface {
	List(ctx context.Context) ([]configrepo.SchemaRecord, error)
}

// NewSchemaCounter returns a SchemaCounterFunc for plugin-provided quota
// middleware to enforce SchemasLimit. Empty tenant_id short-circuits to 0
// (CE single-tenant has no quota plugin wired).
func NewSchemaCounter(repo SchemaListerByTenant) pluginpkg.SchemaCounterFunc {
	return func(ctx context.Context, tenantID string) (int, error) {
		if tenantID == "" {
			return 0, nil
		}
		scoped := domain.WithTenantID(ctx, tenantID)
		recs, err := repo.List(scoped)
		if err != nil {
			return 0, fmt.Errorf("count schemas: %w", err)
		}
		return len(recs), nil
	}
}
