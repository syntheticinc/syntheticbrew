package repository

import (
	"context"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// tenantIDFromCtx resolves the tenant ID for writes in this package.
// Mirrors the configrepo helper: reads domain.TenantIDFromContext and falls
// back to domain.CETenantID so CE single-tenant deployments keep working
// without any tenant middleware wired up.
func tenantIDFromCtx(ctx context.Context) string {
	if tid := domain.TenantIDFromContext(ctx); tid != "" {
		return tid
	}
	return domain.CETenantID
}
