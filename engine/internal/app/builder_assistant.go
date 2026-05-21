package app

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// NewBuilderSchemaResolver returns a per-request resolver that looks up the
// builder-assistant schema id by name. The tenant_id predicate is required
// for tenant isolation (SCC-02): without it any tenant would resolve to
// whichever row GORM returned first. Empty tenant_id (CE single-tenant)
// omits the predicate.
func NewBuilderSchemaResolver(db *gorm.DB, schemaName string) func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		if db == nil {
			return "", fmt.Errorf("no db")
		}
		var id string
		q := db.WithContext(ctx).Table("schemas").Select("id").Where("name = ?", schemaName)
		if tenantID := domain.TenantIDFromContext(ctx); tenantID != "" {
			q = q.Where("tenant_id = ?", tenantID)
		}
		if err := q.Limit(1).Scan(&id).Error; err != nil {
			return "", err
		}
		return id, nil
	}
}
