package mcp

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/syntheticinc/bytebrew/engine/internal/domain"
	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/persistence/models"
)

// ForwardHeadersStore holds the deduplicated list of HTTP header names that
// MCP server configs declare for forwarding from the originating chat
// request. Lookups are tenant-scoped so one tenant's /config/reload (or any
// CRUD-driven Manager refresh) cannot wipe out another tenant's list.
//
// In single-tenant mode (CE) the store keeps one slice and ignores the
// tenantID argument — Set/Get/GetForContext all act on the same value.
// In multi-tenant mode (used when RequireTenant=true) it holds a per-tenant
// map; Get for an unknown tenant returns an empty slice (never nil —
// ChatHandler iterates over the result).
type ForwardHeadersStore struct {
	mu          sync.RWMutex
	perTenant   map[string][]string
	single      []string
	isPerTenant bool
}

// NewForwardHeadersStore creates an empty store. Pass perTenant=sc.RequireTenant.
func NewForwardHeadersStore(perTenant bool) *ForwardHeadersStore {
	s := &ForwardHeadersStore{isPerTenant: perTenant}
	if perTenant {
		s.perTenant = make(map[string][]string)
	} else {
		s.single = []string{}
	}
	return s
}

// Set installs the header list for a tenant. In single-tenant mode the
// tenantID is ignored and the singleton slice is replaced. The provided
// slice is copied to insulate the store from later mutation by the caller.
func (s *ForwardHeadersStore) Set(tenantID string, headers []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cp := make([]string, len(headers))
	copy(cp, headers)

	if !s.isPerTenant {
		s.single = cp
		return
	}
	s.perTenant[tenantID] = cp
}

// Get returns the header list for a tenant. Always returns a non-nil slice
// (empty when the tenant has no headers configured) so callers can iterate
// without nil checks. In single-tenant mode tenantID is ignored.
func (s *ForwardHeadersStore) Get(tenantID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.isPerTenant {
		if s.single == nil {
			return []string{}
		}
		out := make([]string, len(s.single))
		copy(out, s.single)
		return out
	}
	headers, ok := s.perTenant[tenantID]
	if !ok {
		return []string{}
	}
	out := make([]string, len(headers))
	copy(out, headers)
	return out
}

// GetForContext extracts tenantID from ctx via domain.TenantIDFromContext
// and delegates to Get. In single-tenant mode the ctx is irrelevant — the
// singleton slice is returned regardless of whether tenant_id is set.
func (s *ForwardHeadersStore) GetForContext(ctx context.Context) []string {
	if !s.isPerTenant {
		return s.Get("")
	}
	return s.Get(domain.TenantIDFromContext(ctx))
}

// CollectForwardHeaders returns the deduplicated union of forward_headers
// configured across the supplied MCP servers. Lifted from app.collectForwardHeaders
// so the Manager can refresh the per-tenant store after every load/reconnect
// without depending on the app package (would be an import cycle).
func CollectForwardHeaders(mcpServers []models.MCPServerModel) []string {
	seen := make(map[string]bool)
	headers := []string{}
	for _, srv := range mcpServers {
		if srv.ForwardHeaders == nil || *srv.ForwardHeaders == "" {
			continue
		}
		var fh []string
		if err := json.Unmarshal([]byte(*srv.ForwardHeaders), &fh); err != nil {
			continue
		}
		for _, h := range fh {
			if !seen[h] {
				seen[h] = true
				headers = append(headers, h)
			}
		}
	}
	return headers
}
