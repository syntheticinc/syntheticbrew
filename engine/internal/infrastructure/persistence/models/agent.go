package models

import "time"

// AgentModel maps to the "agents" table.
type AgentModel struct {
	ID             string    `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	Name           string    `gorm:"uniqueIndex;not null"`
	ModelID        *string   `gorm:"type:uuid;index"`
	SystemPrompt   string    `gorm:"type:text;not null"`
	Lifecycle      string    `gorm:"type:varchar(20);not null;default:persistent"`
	ToolExecution  string    `gorm:"type:varchar(20);not null;default:sequential"`
	MaxSteps       int       `gorm:"not null;default:0"`
	MaxContextSize  int       `gorm:"not null;default:16000"`
	MaxTurnDuration int       `gorm:"not null;default:120"` // seconds, max time for a single LLM stream turn
	MaxStepDuration int       `gorm:"not null;default:0"`   // seconds, per-step watchdog timeout (0 = disabled)
	Temperature    *float64  `gorm:"type:double precision"`
	TopP           *float64  `gorm:"type:double precision"`
	MaxTokens      *int      `gorm:""`
	StopSequences  *string   `gorm:"type:jsonb"`
	ConfirmBefore  *string   `gorm:"type:jsonb"`
	IsSystem       bool      `gorm:"not null;default:false"`
	TenantID       string    `gorm:"type:uuid;not null;default:'00000000-0000-0000-0000-000000000001'" json:"tenant_id"`
	CreatedAt      time.Time `gorm:"autoCreateTime"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime"`

	// Associations (not loaded by default).
	Model *LLMProviderModel `gorm:"foreignKey:ModelID"`
	Tools []AgentToolModel  `gorm:"foreignKey:AgentID"`
	// MCPServers loaded manually via separate query (GORM many2many infers wrong column names from AgentModel → agent_model_id)
}

func (AgentModel) TableName() string { return "agents" }
