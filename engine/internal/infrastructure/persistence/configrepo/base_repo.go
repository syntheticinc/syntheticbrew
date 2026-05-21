package configrepo

import (
	"context"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"gorm.io/gorm"
)

// tenantIDFromCtx returns the tenant ID from the context.
// Falls back to domain.CETenantID for CE single-tenant mode so that
// CE deployments continue to work without any tenant middleware wired up.
func tenantIDFromCtx(ctx context.Context) string {
	tid := domain.TenantIDFromContext(ctx)
	if tid == "" {
		return domain.CETenantID
	}
	return tid
}

// tenantScope is a GORM scope that filters queries by tenant_id resolved
// from the context. Usage:
//
//	db.Scopes(tenantScope(ctx)).Find(&items)
//
// This keeps the tenant filter applied consistently across all tenant-scoped
// repositories without spreading the WHERE clause across callsites.
func tenantScope(ctx context.Context) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Where("tenant_id = ?", tenantIDFromCtx(ctx))
	}
}
