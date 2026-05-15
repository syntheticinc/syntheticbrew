package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/bytebrew/engine/internal/domain"
)

// TestStore_TenantIsolation verifies a /reload (or any Set) for one tenant
// does not overwrite another tenant's headers — the original multi-tenant
// regression this store was introduced to prevent.
func TestStore_TenantIsolation(t *testing.T) {
	s := NewForwardHeadersStore(true)

	s.Set("tenant-a", []string{"X-Trace-Id", "X-User-Country"})
	s.Set("tenant-b", []string{"Authorization"})

	gotA := s.Get("tenant-a")
	gotB := s.Get("tenant-b")
	assert.ElementsMatch(t, []string{"X-Trace-Id", "X-User-Country"}, gotA)
	assert.ElementsMatch(t, []string{"Authorization"}, gotB)

	s.Set("tenant-a", []string{"X-Override"})

	require.ElementsMatch(t, []string{"X-Override"}, s.Get("tenant-a"))
	require.ElementsMatch(t, []string{"Authorization"}, s.Get("tenant-b"),
		"tenant-b headers must survive tenant-a Set")
}

// TestStore_CESingleton verifies single-tenant mode ignores tenantID and
// every Get returns the same slice.
func TestStore_CESingleton(t *testing.T) {
	s := NewForwardHeadersStore(false)

	s.Set("tenant-a", []string{"X-Trace-Id"})
	require.ElementsMatch(t, []string{"X-Trace-Id"}, s.Get("tenant-a"))
	require.ElementsMatch(t, []string{"X-Trace-Id"}, s.Get("tenant-b"),
		"single-tenant mode must ignore tenantID")
	require.ElementsMatch(t, []string{"X-Trace-Id"}, s.Get(""))

	// Set with a different (or empty) tenantID overwrites the single slot.
	s.Set("", []string{"Authorization"})
	require.ElementsMatch(t, []string{"Authorization"}, s.Get("any-tenant"))
}

// TestStore_GetForContextNoTenant covers the edge where multi-tenant mode
// receives a ctx without tenant_id — must return an empty (non-nil) slice
// rather than panic or return nil.
func TestStore_GetForContextNoTenant(t *testing.T) {
	s := NewForwardHeadersStore(true)
	s.Set("tenant-a", []string{"X-Trace-Id"})

	got := s.GetForContext(context.Background())
	require.NotNil(t, got, "GetForContext must never return nil")
	assert.Empty(t, got)

	gotA := s.GetForContext(domain.WithTenantID(context.Background(), "tenant-a"))
	assert.ElementsMatch(t, []string{"X-Trace-Id"}, gotA)
}

// TestStore_NeverReturnsNil locks in the contract that ChatHandler relies on:
// callers iterate over the result, so nil is unacceptable in any mode/path.
func TestStore_NeverReturnsNil(t *testing.T) {
	cloud := NewForwardHeadersStore(true)
	require.NotNil(t, cloud.Get("never-set"))
	require.NotNil(t, cloud.GetForContext(context.Background()))

	ce := NewForwardHeadersStore(false)
	require.NotNil(t, ce.Get(""))
	require.NotNil(t, ce.GetForContext(context.Background()))
}
