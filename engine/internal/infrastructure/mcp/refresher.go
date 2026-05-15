package mcp

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/syntheticinc/bytebrew/engine/internal/domain"
)

// refreshKey identifies an active refresh task by tenant + server name.
// Tasks are scoped per (tenantID, serverName) — never global. Tenant A's
// task can never trigger a refresh against tenant B's MCP client.
type refreshKey struct {
	tenantID   string
	serverName string
}

// refreshTask holds the cancellation handle and configured interval for an
// active per-server refresh goroutine.
type refreshTask struct {
	cancel   context.CancelFunc
	interval time.Duration
}

// Refresher schedules per-server periodic tools/list refresh tasks.
//
// One goroutine per (tenantID, serverName) with refresh enabled. All
// goroutines are bound to the rootCtx supplied at construction so that
// shutdown propagates via context cancel; StopAll covers the explicit
// teardown path used in server.go's defer.
//
// In single-tenant mode (CE) the same surface is used with the CE sentinel
// tenant key — no separate code path. Goroutine count is bounded by
// `tenants × servers with refresh enabled`.
type Refresher struct {
	mu      sync.Mutex
	tasks   map[refreshKey]*refreshTask
	manager *Manager
	rootCtx context.Context
}

// NewRefresher returns a Refresher whose goroutines are bound to rootCtx.
// Cancellation of rootCtx (typically the server's main ctx) stops every
// active refresh task; StopAll provides explicit teardown for use in
// `defer` at boot.
func NewRefresher(m *Manager, rootCtx context.Context) *Refresher {
	return &Refresher{
		tasks:   make(map[refreshKey]*refreshTask),
		manager: m,
		rootCtx: rootCtx,
	}
}

// Schedule starts (or replaces) a periodic tools/list refresh for the given
// tenant + server. If a task with the same key already exists it is
// cancelled before the new one starts — useful when the operator changes
// the interval via PATCH.
func (rf *Refresher) Schedule(tenantID, serverName string, interval time.Duration) {
	if interval <= 0 {
		return
	}

	key := refreshKey{tenantID: tenantID, serverName: serverName}

	rf.mu.Lock()
	if existing, ok := rf.tasks[key]; ok {
		existing.cancel()
		delete(rf.tasks, key)
	}

	taskCtx, cancel := context.WithCancel(rf.rootCtx)
	rf.tasks[key] = &refreshTask{cancel: cancel, interval: interval}
	rf.mu.Unlock()

	go rf.run(taskCtx, key, interval)
}

// Stop cancels the refresh task for (tenantID, serverName). No-op when no
// such task exists — callers (DisconnectServer, ReconnectServer with NULL
// interval) don't have to know whether one was previously scheduled.
func (rf *Refresher) Stop(tenantID, serverName string) {
	key := refreshKey{tenantID: tenantID, serverName: serverName}

	rf.mu.Lock()
	defer rf.mu.Unlock()

	if existing, ok := rf.tasks[key]; ok {
		existing.cancel()
		delete(rf.tasks, key)
	}
}

// StopAll cancels every active refresh task and clears the task map. Used
// in the server.go shutdown defer so an aborted boot still cleans up
// goroutines deterministically.
func (rf *Refresher) StopAll() {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	for key, task := range rf.tasks {
		task.cancel()
		delete(rf.tasks, key)
	}
}

// active returns the number of currently scheduled tasks. Test-only helper.
func (rf *Refresher) active() int {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return len(rf.tasks)
}

// run is the per-task goroutine body. Each tick:
//  1. Lazy-load the per-tenant ClientRegistry via the Manager.
//  2. Look up the *Client for the server name.
//  3. Snapshot the current tool name set.
//  4. Call client.RefreshTools — on error, log warn and keep the existing
//     tool list intact (no flush).
//  5. Snapshot the new tool name set and emit an info log if added/removed
//     is non-empty so operators can confirm the refresh worked.
func (rf *Refresher) run(taskCtx context.Context, key refreshKey, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-taskCtx.Done():
			return
		case <-ticker.C:
			rf.tick(taskCtx, key)
		}
	}
}

// tick performs one refresh cycle. Extracted for direct unit-test
// invocation without waiting for the ticker.
func (rf *Refresher) tick(taskCtx context.Context, key refreshKey) {
	ctx := domain.WithTenantID(taskCtx, key.tenantID)

	registry, err := rf.manager.GetForContext(ctx)
	if err != nil {
		slog.WarnContext(ctx, "mcp refresher: get registry failed",
			"tenant_id", key.tenantID, "server", key.serverName, "error", err)
		return
	}

	client, ok := registry.client(key.serverName)
	if !ok {
		// Server may have been deleted between Schedule and this tick.
		// Stop self so we don't keep ticking.
		rf.Stop(key.tenantID, key.serverName)
		return
	}

	before := toolNameSet(client.ListTools())

	if refreshErr := client.RefreshTools(ctx); refreshErr != nil {
		slog.WarnContext(ctx, "mcp refresher: tools/list refresh failed",
			"tenant_id", key.tenantID, "server", key.serverName, "error", refreshErr)
		return
	}

	after := toolNameSet(client.ListTools())
	added, removed := diffSets(before, after)
	if len(added) == 0 && len(removed) == 0 {
		return
	}

	slog.InfoContext(ctx, "mcp tools refreshed",
		"tenant_id", key.tenantID,
		"server", key.serverName,
		"added", added,
		"removed", removed,
		"total", len(after),
	)
}

// toolNameSet builds a sorted-name set from the client's tool list.
func toolNameSet(tools []MCPTool) map[string]struct{} {
	out := make(map[string]struct{}, len(tools))
	for _, t := range tools {
		out[t.Name] = struct{}{}
	}
	return out
}

// diffSets returns sorted (added, removed) name lists between before and after.
func diffSets(before, after map[string]struct{}) (added, removed []string) {
	for name := range after {
		if _, ok := before[name]; !ok {
			added = append(added, name)
		}
	}
	for name := range before {
		if _, ok := after[name]; !ok {
			removed = append(removed, name)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}
