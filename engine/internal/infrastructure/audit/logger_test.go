package audit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: gormlogger.Discard,
	})
	require.NoError(t, err)

	// Create table manually to avoid PostgreSQL-specific syntax in GORM tags.
	// Mirrors migration 002 schema (actor_sub only; actor_user_id dropped).
	err = db.Exec(`CREATE TABLE audit_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		occurred_at DATETIME,
		actor_type VARCHAR(20) NOT NULL,
		actor_sub VARCHAR(255),
		action VARCHAR(50) NOT NULL,
		resource VARCHAR(500),
		details TEXT,
		session_id VARCHAR(36),
		task_id TEXT,
		tenant_id TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'
	)`).Error
	require.NoError(t, err)

	return db
}

func TestLogger_Log(t *testing.T) {
	db := setupTestDB(t)
	logger := NewLogger(db)

	ts := time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC)
	sessionID := "session-123"

	actorSub := "admin@example.com"
	err := logger.Log(context.Background(), Entry{
		Timestamp: ts,
		ActorType: "admin",
		ActorID:   actorSub,
		Action:    "api_call",
		Resource:  "GET /api/v1/agents",
		Details: map[string]interface{}{
			"method":      "GET",
			"status_code": 200,
		},
		SessionID: sessionID,
	})
	require.NoError(t, err)

	var result models.AuditLogModel
	require.NoError(t, db.First(&result).Error)

	assert.Equal(t, "admin", result.ActorType)
	require.NotNil(t, result.ActorSub)
	assert.Equal(t, actorSub, *result.ActorSub)
	assert.Equal(t, "api_call", result.Action)
	assert.Equal(t, "GET /api/v1/agents", result.Resource)
	assert.Contains(t, result.Details, `"method":"GET"`)
	assert.Contains(t, result.Details, `"status_code":200`)
	require.NotNil(t, result.SessionID)
	assert.Equal(t, "session-123", *result.SessionID)
	assert.Nil(t, result.TaskID)
}

func TestLogger_Log_EmptySessionID(t *testing.T) {
	db := setupTestDB(t)
	logger := NewLogger(db)

	err := logger.Log(context.Background(), Entry{
		ActorType: "system",
		Action:    "config_change",
	})
	require.NoError(t, err)

	var result models.AuditLogModel
	require.NoError(t, db.First(&result).Error)
	assert.Nil(t, result.SessionID)
}

func TestLogger_Log_WithTaskID(t *testing.T) {
	db := setupTestDB(t)
	logger := NewLogger(db)

	taskID := "task-uuid-42"
	err := logger.Log(context.Background(), Entry{
		ActorType: "api_token",
		ActorID:   "bot-token",
		Action:    "task_created",
		Resource:  "POST /api/v1/tasks",
		TaskID:    &taskID,
	})
	require.NoError(t, err)

	var result models.AuditLogModel
	require.NoError(t, db.First(&result).Error)
	require.NotNil(t, result.TaskID)
	assert.Equal(t, "task-uuid-42", *result.TaskID)
	require.NotNil(t, result.ActorSub)
	assert.Equal(t, "bot-token", *result.ActorSub)
}

func TestLogger_Log_ZeroTimestamp(t *testing.T) {
	db := setupTestDB(t)
	logger := NewLogger(db)

	before := time.Now()
	err := logger.Log(context.Background(), Entry{
		ActorType: "system",
		Action:    "config_change",
	})
	require.NoError(t, err)

	var result models.AuditLogModel
	require.NoError(t, db.First(&result).Error)
	assert.False(t, result.OccurredAt.Before(before))
}

func TestLogger_Log_NilDetails(t *testing.T) {
	db := setupTestDB(t)
	logger := NewLogger(db)

	err := logger.Log(context.Background(), Entry{
		ActorType: "admin",
		Action:    "api_call",
		Details:   nil,
	})
	require.NoError(t, err)

	var result models.AuditLogModel
	require.NoError(t, db.First(&result).Error)
	assert.Equal(t, "null", result.Details)
}

func TestLogger_Log_MultipleEntries(t *testing.T) {
	db := setupTestDB(t)
	logger := NewLogger(db)

	for i := 0; i < 5; i++ {
		require.NoError(t, logger.Log(context.Background(), Entry{
			ActorType: "admin",
			Action:    "api_call",
		}))
	}

	var count int64
	db.Model(&models.AuditLogModel{}).Count(&count)
	assert.Equal(t, int64(5), count)
}

// TestLogger_Log_CETenantFallback verifies that in CE mode (no tenant in ctx)
// entries default to the CE sentinel tenant, keeping single-tenant semantics.
func TestLogger_Log_CETenantFallback(t *testing.T) {
	db := setupTestDB(t)
	logger := NewLogger(db)

	err := logger.Log(context.Background(), Entry{
		ActorType: "admin",
		Action:    "agent.create",
	})
	require.NoError(t, err)

	var result models.AuditLogModel
	require.NoError(t, db.First(&result).Error)
	assert.Equal(t, domain.CETenantID, result.TenantID,
		"CE fallback must stamp the CETenantID sentinel")
}

// TestLogger_Log_CloudTenantStamp verifies that a Cloud-style ctx with a
// tenant_id attached is persisted verbatim — this is the Bug 1 regression.
// Without the fix, all audit rows land under the CE sentinel even in Cloud.
func TestLogger_Log_CloudTenantStamp(t *testing.T) {
	db := setupTestDB(t)
	logger := NewLogger(db)

	tenantA := "11111111-1111-1111-1111-111111111111"
	ctx := domain.WithTenantID(context.Background(), tenantA)

	err := logger.Log(ctx, Entry{
		ActorType: "admin",
		ActorID:   "alice@tenant-a.com",
		Action:    "agent.create",
	})
	require.NoError(t, err)

	var result models.AuditLogModel
	require.NoError(t, db.First(&result).Error)
	assert.Equal(t, tenantA, result.TenantID,
		"Cloud tenant_id from ctx must be persisted verbatim")
	assert.NotEqual(t, domain.CETenantID, result.TenantID,
		"must not collapse Cloud tenant to CE sentinel")
}

// TestLogger_Log_MultipleTenants verifies that two concurrent tenants write
// entries under their own tenant_id — no leakage, no fall-through to the
// default column value.
func TestLogger_Log_MultipleTenants(t *testing.T) {
	db := setupTestDB(t)
	logger := NewLogger(db)

	tenantA := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	tenantB := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	require.NoError(t, logger.Log(domain.WithTenantID(context.Background(), tenantA), Entry{
		ActorType: "admin", Action: "a",
	}))
	require.NoError(t, logger.Log(domain.WithTenantID(context.Background(), tenantB), Entry{
		ActorType: "admin", Action: "b",
	}))

	var rows []models.AuditLogModel
	require.NoError(t, db.Order("action ASC").Find(&rows).Error)
	require.Len(t, rows, 2)
	assert.Equal(t, tenantA, rows[0].TenantID)
	assert.Equal(t, tenantB, rows[1].TenantID)
}
