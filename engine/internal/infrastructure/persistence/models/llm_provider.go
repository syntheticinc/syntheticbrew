package models

import (
	"encoding/json"
	"time"
)

// ModelConfig holds type-specific configuration for an LLM provider model.
// Stored as jsonb in the "config" column. Extensible for future fields
// (context_window, supports_tools, etc.).
//
// ExtraBody is merged verbatim into the JSON request body for each LLM call
// on openai_compatible providers. Lets operators pass through fields the
// upstream understands but Eino doesn't model — e.g. OpenRouter's
// {"provider":{"order":["zai"],"allow_fallbacks":false}} routing override.
// Keys colliding with engine-set fields (messages, tools, stream, model)
// are dropped at merge time so the engine's request contract stays intact.
type ModelConfig struct {
	EmbeddingDim int            `json:"embedding_dim,omitempty"` // >0 for embedding models (e.g. 1536 for text-embedding-3-small)
	ExtraBody    map[string]any `json:"extra_body,omitempty"`
}

// LLMProviderModel maps to the "models" table (LLM provider configuration).
type LLMProviderModel struct {
	ID              string    `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	Name            string    `gorm:"uniqueIndex;not null"`
	Type            string    `gorm:"type:varchar(30);not null"`
	// Kind discriminates between 'chat' and 'embedding' models.
	// DB CHECK constraint (chk_models_kind) enforces the allowed values.
	// Application layer is the primary enforcement point.
	Kind            string    `gorm:"type:varchar(20);not null;default:'chat'"`
	// IsDefault flags the tenant-default model for this Kind. At most one
	// row per (tenant_id, kind='chat') can have IsDefault=true — enforced
	// by a partial unique index (idx_models_tenant_default_chat).
	IsDefault       bool      `gorm:"not null;default:false" json:"is_default"`
	BaseURL         string    `gorm:"type:varchar(500)"`
	ModelName       string    `gorm:"type:varchar(255);not null"`
	APIKeyEncrypted string    `gorm:"type:varchar(1000)"`
	APIVersion      string    `gorm:"type:varchar(30);default:''"`
	Config          string    `gorm:"type:jsonb;not null;default:'{}'"` // JSON (ModelConfig)
	TenantID        string    `gorm:"type:uuid;not null;default:'00000000-0000-0000-0000-000000000001'" json:"tenant_id"`
	CreatedAt       time.Time `gorm:"autoCreateTime"`
	UpdatedAt       time.Time `gorm:"autoUpdateTime"`
}

func (LLMProviderModel) TableName() string { return "models" }

// GetConfig parses the Config jsonb into a ModelConfig struct.
func (m *LLMProviderModel) GetConfig() ModelConfig {
	var cfg ModelConfig
	if m.Config != "" {
		_ = json.Unmarshal([]byte(m.Config), &cfg)
	}
	return cfg
}

// SetConfig serializes a ModelConfig into the Config jsonb field.
func (m *LLMProviderModel) SetConfig(cfg ModelConfig) {
	data, err := json.Marshal(cfg)
	if err != nil {
		m.Config = "{}"
		return
	}
	m.Config = string(data)
}

// EmbeddingDim returns the embedding dimension from config (convenience accessor).
func (m *LLMProviderModel) EmbeddingDim() int {
	return m.GetConfig().EmbeddingDim
}
