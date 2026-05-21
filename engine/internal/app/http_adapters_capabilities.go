package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"gorm.io/gorm"
)

// capabilityConfigReader implements tools.CapabilityConfigReader.
// Reads any capability config from DB by agent name and type.
type capabilityConfigReader struct {
	db *gorm.DB
}

func (r *capabilityConfigReader) ReadConfig(ctx context.Context, agentName, capType string) (map[string]interface{}, error) {
	return resolveCapabilityConfigFromDB(r.db, ctx, agentName, capType)
}

// resolveCapabilityConfigFromDB reads capability config from DB by agent name and type.
// Shared by capabilityConfigReader and any other consumers that need per-agent config.
func resolveCapabilityConfigFromDB(db *gorm.DB, ctx context.Context, agentName, capType string) (map[string]interface{}, error) {
	var agentID string
	if err := db.WithContext(ctx).
		Raw("SELECT id FROM agents WHERE name = ?", agentName).
		Scan(&agentID).Error; err != nil || agentID == "" {
		return nil, nil
	}

	var cap models.CapabilityModel
	if err := db.WithContext(ctx).
		Where("agent_id = ? AND type = ?", agentID, capType).
		First(&cap).Error; err != nil {
		return nil, nil
	}

	if cap.Config == "" {
		return nil, nil
	}

	var config map[string]interface{}
	if err := json.Unmarshal([]byte(cap.Config), &config); err != nil {
		return nil, fmt.Errorf("parse %s config: %w", capType, err)
	}

	return config, nil
}
