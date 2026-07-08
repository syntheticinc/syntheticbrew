package app

import (
	"context"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	pluginpkg "github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// KnowledgeDocumentCounterByTenant counts knowledge documents scoped by
// `tenant_id` from the context.
type KnowledgeDocumentCounterByTenant interface {
	CountDocuments(ctx context.Context) (int64, error)
}

// NewKnowledgeDocumentCounter returns a KnowledgeDocumentCounterFunc for
// plugin-provided enforcement to count a tenant's knowledge documents. Empty
// tenant_id short-circuits to 0 (CE single-tenant has no such plugin wired).
func NewKnowledgeDocumentCounter(repo KnowledgeDocumentCounterByTenant) pluginpkg.KnowledgeDocumentCounterFunc {
	return func(ctx context.Context, tenantID string) (int, error) {
		if tenantID == "" {
			return 0, nil
		}
		scoped := domain.WithTenantID(ctx, tenantID)
		count, err := repo.CountDocuments(scoped)
		if err != nil {
			return 0, fmt.Errorf("count knowledge documents: %w", err)
		}
		return int(count), nil
	}
}
