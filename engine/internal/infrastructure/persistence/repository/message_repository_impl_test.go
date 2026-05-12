package repository

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/syntheticinc/bytebrew/engine/internal/domain"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupEventTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err, "failed to open in-memory SQLite")

	// Create table manually — SQLite doesn't support uuid/jsonb/gen_random_uuid()
	err = db.Exec(`CREATE TABLE messages (
		id         TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		event_type TEXT NOT NULL,
		agent_id   TEXT,
		call_id    TEXT,
		payload    TEXT NOT NULL DEFAULT '{}',
		tenant_id  TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
		created_at DATETIME
	)`).Error
	require.NoError(t, err, "failed to create messages table")

	return db
}

func TestEventRepository_CreateAndGetBySessionID(t *testing.T) {
	db := setupEventTestDB(t)
	repo := NewMessageRepositoryImpl(db)
	ctx := context.Background()

	sessionID := uuid.New().String()
	baseTime := time.Now().Add(-1 * time.Hour)

	msg1, err := domain.NewUserMessageEvent(sessionID, "First message")
	require.NoError(t, err)
	msg1.AgentID = "supervisor"
	msg1.CreatedAt = baseTime

	msg2, err := domain.NewAssistantEvent(sessionID, "Second message")
	require.NoError(t, err)
	msg2.AgentID = "supervisor"
	msg2.CreatedAt = baseTime.Add(1 * time.Second)

	msg3, err := domain.NewUserMessageEvent(sessionID, "Third message")
	require.NoError(t, err)
	msg3.AgentID = "supervisor"
	msg3.CreatedAt = baseTime.Add(2 * time.Second)

	require.NoError(t, repo.Create(ctx, msg1))
	require.NoError(t, repo.Create(ctx, msg2))
	require.NoError(t, repo.Create(ctx, msg3))

	events, err := repo.GetBySessionID(ctx, sessionID, 0, 0)
	require.NoError(t, err)
	require.Len(t, events, 3)

	// Chronological order
	assert.Equal(t, "First message", events[0].GetContent())
	assert.Equal(t, "Second message", events[1].GetContent())
	assert.Equal(t, "Third message", events[2].GetContent())

	for _, ev := range events {
		assert.Equal(t, sessionID, ev.SessionID)
		assert.NotEmpty(t, ev.ID)
		assert.Equal(t, "supervisor", ev.AgentID)
	}
}

func TestEventRepository_GetBySessionAndAgent(t *testing.T) {
	db := setupEventTestDB(t)
	repo := NewMessageRepositoryImpl(db)
	ctx := context.Background()

	sessionID := uuid.New().String()
	baseTime := time.Now().Add(-1 * time.Hour)

	msg1, _ := domain.NewUserMessageEvent(sessionID, "Agent-1 msg")
	msg1.AgentID = "agent-1"
	msg1.CreatedAt = baseTime

	msg2, _ := domain.NewAssistantEvent(sessionID, "Agent-2 msg")
	msg2.AgentID = "agent-2"
	msg2.CreatedAt = baseTime.Add(1 * time.Second)

	msg3, _ := domain.NewUserMessageEvent(sessionID, "Agent-1 msg 2")
	msg3.AgentID = "agent-1"
	msg3.CreatedAt = baseTime.Add(2 * time.Second)

	require.NoError(t, repo.Create(ctx, msg1))
	require.NoError(t, repo.Create(ctx, msg2))
	require.NoError(t, repo.Create(ctx, msg3))

	events, err := repo.GetBySessionAndAgent(ctx, sessionID, "agent-1", 0, 0)
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "Agent-1 msg", events[0].GetContent())
	assert.Equal(t, "Agent-1 msg 2", events[1].GetContent())

	events2, err := repo.GetBySessionAndAgent(ctx, sessionID, "agent-2", 0, 0)
	require.NoError(t, err)
	require.Len(t, events2, 1)
}

func TestEventRepository_EmptySession(t *testing.T) {
	db := setupEventTestDB(t)
	repo := NewMessageRepositoryImpl(db)
	ctx := context.Background()

	events, err := repo.GetBySessionID(ctx, uuid.New().String(), 0, 0)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestEventRepository_ToolCallRoundtrip(t *testing.T) {
	db := setupEventTestDB(t)
	repo := NewMessageRepositoryImpl(db)
	ctx := context.Background()

	sessionID := uuid.New().String()

	// Create tool call event
	tc, err := domain.NewToolCallEvent(sessionID, "call-1", "search", map[string]string{"q": "main.go"})
	require.NoError(t, err)
	tc.AgentID = "supervisor"
	require.NoError(t, repo.Create(ctx, tc))

	// Create tool result event
	tr, err := domain.NewToolResultEvent(sessionID, "call-1", "search", "Found 3 files", false)
	require.NoError(t, err)
	tr.AgentID = "supervisor"
	require.NoError(t, repo.Create(ctx, tr))

	events, err := repo.GetBySessionID(ctx, sessionID, 0, 0)
	require.NoError(t, err)
	require.Len(t, events, 2)

	// Tool call
	assert.Equal(t, domain.MessageTypeToolCall, events[0].Type)
	assert.Equal(t, "call-1", events[0].CallID)
	p, ok := events[0].GetToolCallPayload()
	require.True(t, ok)
	assert.Equal(t, "search", p.Tool)
	assert.Equal(t, "main.go", p.Arguments["q"])

	// Tool result
	assert.Equal(t, domain.MessageTypeToolResult, events[1].Type)
	assert.Equal(t, "call-1", events[1].CallID)
	rp, ok := events[1].GetToolResultPayload()
	require.True(t, ok)
	assert.Equal(t, "search", rp.Tool)
	assert.Equal(t, "Found 3 files", rp.Content)
}

func TestEventRepository_ReasoningEvent(t *testing.T) {
	db := setupEventTestDB(t)
	repo := NewMessageRepositoryImpl(db)
	ctx := context.Background()

	sessionID := uuid.New().String()

	re, err := domain.NewReasoningEvent(sessionID, "I should use search tool")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, re))

	events, err := repo.GetBySessionID(ctx, sessionID, 0, 0)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, domain.MessageTypeReasoning, events[0].Type)
	assert.Equal(t, "I should use search tool", events[0].GetContent())
}
