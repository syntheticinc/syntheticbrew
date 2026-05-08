package app

import (
	"testing"

	"github.com/stretchr/testify/assert"

	httpdelivery "github.com/syntheticinc/bytebrew/engine/internal/delivery/http"
)

// TestTenantSeeder_DefaultSchemaName_PassesNameValidation guards against the
// 2026-05-08 prod regression where SeedTenant hardcoded "My Workspace" — a
// space + uppercase value that violates the name regex shipped with engine
// 1.1.0 (chk_schemas_name_format CHECK constraint + ValidateResourceName).
// Every new signup hit the constraint and EE provisioning returned 500,
// leaving tenants without a default schema.
//
// Whatever default name SeedTenant uses MUST round-trip through the same
// validator the HTTP handlers apply, otherwise the next CHECK-constraint
// addition silently breaks signups again.
func TestTenantSeeder_DefaultSchemaName_PassesNameValidation(t *testing.T) {
	const seededName = "my-workspace"
	assert.NoError(t, httpdelivery.ValidateResourceName(seededName),
		"default schema name seeded by SeedTenant must pass ValidateResourceName "+
			"or every signup will 500 against chk_schemas_name_format")
}
