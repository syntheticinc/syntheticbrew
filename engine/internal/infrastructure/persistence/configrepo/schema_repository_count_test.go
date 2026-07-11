package configrepo

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// TestSchemaRepo_CountUserSchemas verifies that CountUserSchemas excludes
// engine-managed system schemas and stays tenant-scoped. This is the DB-level
// root cause of B6: the schema quota must count only user-created schemas.
func TestSchemaRepo_CountUserSchemas(t *testing.T) {
	db := setupSchemaDB(t)
	repo := NewGORMSchemaRepository(db)

	ctxA := domain.WithTenantID(context.Background(), "aaaaaaaa-0000-0000-0000-000000000001")
	ctxB := domain.WithTenantID(context.Background(), "bbbbbbbb-0000-0000-0000-000000000002")

	// Tenant A: one user schema + one system schema.
	require.NoError(t, repo.Create(ctxA, &SchemaRecord{Name: "my-workspace"}))
	require.NoError(t, repo.Create(ctxA, &SchemaRecord{Name: "builder-schema", IsSystem: true}))

	// Tenant B: one user schema (must not leak into tenant A's count).
	require.NoError(t, repo.Create(ctxB, &SchemaRecord{Name: "other-workspace"}))

	countA, err := repo.CountUserSchemas(ctxA)
	require.NoError(t, err)
	assert.Equal(t, int64(1), countA, "tenant A count excludes the system schema")

	countB, err := repo.CountUserSchemas(ctxB)
	require.NoError(t, err)
	assert.Equal(t, int64(1), countB, "tenant B sees only its own user schema")

	// Tenant with no schemas → zero.
	ctxC := domain.WithTenantID(context.Background(), "cccccccc-0000-0000-0000-000000000003")
	countC, err := repo.CountUserSchemas(ctxC)
	require.NoError(t, err)
	assert.Equal(t, int64(0), countC)
}
