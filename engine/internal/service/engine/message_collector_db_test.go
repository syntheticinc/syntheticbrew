package engine

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/repository"
)

// setupMessagesDB spins up an in-memory SQLite that mirrors the messages
// table shape the GORMEventRepository / MessageRepositoryImpl write into.
// Tenant_id is the column GET /sessions/{id}/messages filters on, so the
// regression test below cares only about that column.
func setupMessagesDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)

	require.NoError(t, db.Exec(`CREATE TABLE messages (
		id         TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		event_type TEXT NOT NULL,
		agent_id   TEXT,
		call_id    TEXT,
		payload    TEXT NOT NULL DEFAULT '{}',
		tenant_id  TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
		created_at DATETIME
	)`).Error)
	return db
}

// TestMessageCollector_StampsTenantThroughFullDBPath is the integration
// regression for the 2026-04-27 "last AI message disappears after refresh"
// bug: the eino streaming-finalize path emits EventTypeAnswer through the
// MessageCollector wrapped callback, and the row written via the real
// MessageRepositoryImpl must carry the caller's tenant_id — otherwise
// GET /sessions/{id}/messages (tenant-scoped) drops it on reload.
func TestMessageCollector_StampsTenantThroughFullDBPath(t *testing.T) {
	const tenant = "9238e024-adbd-ef67-933d-51465a5a5280"

	db := setupMessagesDB(t)
	repo := repository.NewMessageRepositoryImpl(db)

	sessionID := uuid.New().String()
	parentCtx := domain.WithTenantID(context.Background(), tenant)

	mc := NewMessageCollector(parentCtx, sessionID, "supervisor", repo)

	// User message goes through CollectUserMessage with the same parent ctx.
	mc.CollectUserMessage(parentCtx, "echo something")

	// Stream-finalize path: assistant answer fires through the eino callback
	// chain, which has no ctx parameter — handleEvent must inherit the
	// tenant from the collector's stored writeCtx.
	cb := mc.WrapEventCallback(nil)
	require.NoError(t, cb(&domain.AgentEvent{
		Type:    domain.EventTypeAnswer,
		Content: "Echo: ok",
	}))
	require.NoError(t, cb(&domain.AgentEvent{
		Type: domain.EventTypeToolCall,
		Metadata: map[string]interface{}{
			"id":                 "call-1",
			"tool_name":          "echo_message",
			"function_arguments": `{"text":"hi"}`,
		},
	}))
	require.NoError(t, cb(&domain.AgentEvent{
		Type: domain.EventTypeToolResult,
		Metadata: map[string]interface{}{
			"tool_name":   "echo_message",
			"full_result": "ok",
		},
		Content: "ok",
	}))
	require.NoError(t, cb(&domain.AgentEvent{
		Type:       domain.EventTypeReasoning,
		Content:    "I'll echo it back.",
		IsComplete: true,
	}))

	// Read back tenant_id for every row this session produced. Use raw SQL
	// to bypass repository tenant scoping — we want to see what was actually
	// stamped, not what the read-side filter accepts.
	type row struct {
		EventType string
		TenantID  string
	}
	var rows []row
	require.NoError(t, db.
		Table("messages").
		Select("event_type, tenant_id").
		Where("session_id = ?", sessionID).
		Order("created_at ASC").
		Scan(&rows).Error)

	require.NotEmpty(t, rows, "MessageCollector must persist messages for the session")

	// Without the fix, user_message lands under the request tenant
	// (CollectUserMessage uses the parent ctx) but assistant_message /
	// tool_call / tool_result / reasoning all fall back to CETenantID
	// because handleEvent uses context.Background() — the read-side
	// tenant_scope then hides them and the chat looks empty after refresh.
	for _, r := range rows {
		assert.Equalf(t, tenant, r.TenantID,
			"%s row stamped with %q (want %q) — handleEvent dropped tenant ctx",
			r.EventType, r.TenantID, tenant)
	}

	// Sanity: assistant_message must be among the persisted rows. Without
	// the fix it could still be there under the wrong tenant; with the fix
	// every row uses the right tenant.
	var seenAssistant bool
	for _, r := range rows {
		if r.EventType == string(domain.MessageTypeAssistantMessage) {
			seenAssistant = true
			break
		}
	}
	assert.True(t, seenAssistant, "expected assistant_message row to be persisted")
}
