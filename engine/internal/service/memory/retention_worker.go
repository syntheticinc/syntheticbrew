// Package memory provides background cleanup workers for the memory subsystem.
//
// RetentionWorker periodically deletes memory entries that have exceeded the
// per-agent retention_days configured on a memory capability.
package memory

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// RetentionWorker drives the periodic memory retention cleanup loop.
type RetentionWorker struct {
	db       *gorm.DB
	storage  *persistence.MemoryStorage
	interval time.Duration
}

// NewRetentionWorker constructs a worker that runs cleanup every hour.
// Pass the same *gorm.DB used by the rest of the server.
func NewRetentionWorker(db *gorm.DB) *RetentionWorker {
	return &RetentionWorker{
		db:       db,
		storage:  persistence.NewMemoryStorage(db),
		interval: 1 * time.Hour,
	}
}

// Start launches the cleanup goroutine. Returns immediately. The goroutine
// runs cleanup once on startup, then on every tick of w.interval. It exits
// silently when ctx is cancelled.
func (w *RetentionWorker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	go func() {
		slog.InfoContext(ctx, "Memory retention cleanup goroutine started (every 1h)")
		w.runOnce(ctx) // run once on startup
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				w.runOnce(ctx)
			}
		}
	}()
}

// runOnce iterates all memory capabilities and deletes expired entries.
// Returns the total number of rows deleted across all capabilities.
func (w *RetentionWorker) runOnce(ctx context.Context) int64 {
	var caps []models.CapabilityModel
	if err := w.db.WithContext(ctx).Where("type = ? AND enabled = ?", "memory", true).Find(&caps).Error; err != nil {
		slog.WarnContext(ctx, "memory retention cleanup: failed to list capabilities", "error", err)
		return 0
	}

	totalDeleted := int64(0)

	for _, cap := range caps {
		if cap.Config == "" {
			continue
		}
		var config map[string]interface{}
		if err := json.Unmarshal([]byte(cap.Config), &config); err != nil {
			continue
		}

		unlimitedRetention, _ := config["unlimited_retention"].(bool)
		if unlimitedRetention {
			continue
		}

		retentionDays := 0
		if rd, ok := config["retention_days"].(float64); ok {
			retentionDays = int(rd)
		}
		if retentionDays <= 0 {
			continue
		}

		// Derive schema_ids for this agent via agent_relations. Raw SQL —
		// migrating to GORM models requires schema-locked changes.
		var agentName string
		if err := w.db.WithContext(ctx).
			Raw("SELECT name FROM agents WHERE id = ?", cap.AgentID).
			Scan(&agentName).Error; err != nil || agentName == "" {
			slog.WarnContext(ctx, "memory retention cleanup: failed to resolve agent name",
				"agent_id", cap.AgentID, "error", err)
			continue
		}
		var agentID string
		if err := w.db.WithContext(ctx).
			Raw("SELECT id FROM agents WHERE name = ?", agentName).
			Scan(&agentID).Error; err != nil || agentID == "" {
			slog.WarnContext(ctx, "memory retention cleanup: failed to resolve agent id",
				"agent_name", agentName, "error", err)
			continue
		}
		var schemaIDs []string
		if err := w.db.WithContext(ctx).
			Raw(`SELECT DISTINCT schema_id FROM agent_relations
				WHERE source_agent_id = ? OR target_agent_id = ?`, agentID, agentID).
			Scan(&schemaIDs).Error; err != nil {
			slog.WarnContext(ctx, "memory retention cleanup: failed to get schemas",
				"agent", agentName, "error", err)
			continue
		}

		for _, schemaID := range schemaIDs {
			deleted, err := w.storage.CleanupExpiredBySchema(ctx, schemaID, retentionDays)
			if err != nil {
				slog.WarnContext(ctx, "memory retention cleanup failed",
					"schema_id", schemaID, "retention_days", retentionDays, "error", err)
				continue
			}
			totalDeleted += deleted
		}
	}

	if totalDeleted > 0 {
		slog.InfoContext(ctx, "memory retention cleanup completed", "total_deleted", totalDeleted)
	}
	return totalDeleted
}
