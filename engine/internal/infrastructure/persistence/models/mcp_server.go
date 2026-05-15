package models

import "time"

// MCPServerModel maps to the "mcp_servers" table.
//
// V2 Commit Group C (§5.5, §5.6): tenant-scoped MCP instance. The catalog
// moved to a dedicated `mcp_catalog` table — installing from the catalog
// copies the selected package fields into a new row here and then forgets
// the catalog origin (no FK back). Connection status (`mcp_server_runtime`)
// is ephemeral in V2 and answered by a live ping when the UI asks; it is no
// longer persisted.
type MCPServerModel struct {
	ID             string    `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	Name           string    `gorm:"uniqueIndex;not null"`
	Type           string    `gorm:"type:varchar(20);not null"`
	Command        string    `gorm:"type:varchar(500)"`
	Args           *string   `gorm:"type:jsonb"`
	URL            string    `gorm:"type:varchar(500)"`
	EnvVars        *string   `gorm:"type:jsonb"`
	ForwardHeaders *string   `gorm:"type:jsonb"`                             // JSON array of HTTP header names to forward
	AuthType       string    `gorm:"type:varchar(30);not null;default:none"` // none, api_key, forward_headers, oauth2, service_account (AC-AUTH-01)
	AuthKeyEnv     string    `gorm:"type:varchar(255)"`                      // env var for api_key
	AuthTokenEnv   string    `gorm:"type:varchar(255)"`                      // env var for service_account/oauth2 token
	AuthClientID   string    `gorm:"type:varchar(255)"`                      // oauth2 client ID
	Enabled        bool      `gorm:"not null;default:true"`                  // false suppresses tool registration without deletion
	TenantID       string    `gorm:"type:uuid;not null;default:'00000000-0000-0000-0000-000000000001'" json:"tenant_id"`
	// CatalogRefreshIntervalSeconds enables periodic tools/list refresh per server.
	// NULL (default) disables refresh; allowed range 30..86400 enforced by DB CHECK
	// chk_mcp_refresh_range and dual-validated at the API layer. Engine 1.1.9.
	CatalogRefreshIntervalSeconds *int      `gorm:"column:catalog_refresh_interval_seconds"`
	CreatedAt                     time.Time `gorm:"autoCreateTime"`
	UpdatedAt                     time.Time `gorm:"autoUpdateTime"`
}

func (MCPServerModel) TableName() string { return "mcp_servers" }
