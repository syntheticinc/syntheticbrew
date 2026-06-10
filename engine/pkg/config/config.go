package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

// Config holds all configuration for the application
type Config struct {
	Server        ServerConfig        `mapstructure:"server"`
	Database      DatabaseConfig      `mapstructure:"database"`
	LLM           LLMConfig           `mapstructure:"llm"`
	Logging       LoggingConfig       `mapstructure:"logging"`
	Observability ObservabilityConfig `mapstructure:"observability"`
	Security      SecurityConfig      `mapstructure:"security"`
	QA            QAConfig            `mapstructure:"qa"`
	Agent         AgentConfig         `mapstructure:"agent"`
	PlanStorage   PlanStorageConfig   `mapstructure:"plan_storage"`
	WorkStorage   WorkStorageConfig   `mapstructure:"work_storage"`
	BYOK          BYOKConfig          `mapstructure:"byok"`
	RateLimits    []RateLimitRule     `mapstructure:"rate_limits"`

	// ConfigDir is the directory containing config files (set by Load, not from YAML)
	ConfigDir string `mapstructure:"-"`
}

// BYOKConfig is declared in agent_config.go (shared with ModelsConfig.BYOK).
// It holds bootstrap configuration for per-end-user BYOK (V2 §5.8 "Settings
// + BYOK"). On startup these values are written into the `settings` table
// (jsonb), and the middleware reads them from there at request time so
// admin UI changes can take effect without a restart.
//
// Empty AllowedProviders means "no allowlist enforcement" (any provider
// the user supplies is accepted). Disable BYOK entirely with Enabled=false.

// RateLimitRule defines a configurable rate limiting rule based on request headers.
type RateLimitRule struct {
	Name        string                   `mapstructure:"name" yaml:"name" json:"name"`
	KeyHeader   string                   `mapstructure:"key_header" yaml:"key_header" json:"key_header"`
	TierHeader  string                   `mapstructure:"tier_header" yaml:"tier_header" json:"tier_header"`
	Tiers       map[string]RateLimitTier `mapstructure:"tiers" yaml:"tiers" json:"tiers"`
	DefaultTier string                   `mapstructure:"default_tier" yaml:"default_tier" json:"default_tier"`
}

// RateLimitTier defines rate limit parameters for a specific tier.
type RateLimitTier struct {
	Requests  int    `mapstructure:"requests" yaml:"requests" json:"requests"`
	Window    string `mapstructure:"window" yaml:"window" json:"window"`
	Unlimited bool   `mapstructure:"unlimited" yaml:"unlimited" json:"unlimited"`
}

// WorkStorageConfig holds work storage (stories + tasks) configuration
type WorkStorageConfig struct {
	DBPath string `mapstructure:"db_path"`
}

// PlanStorageConfig holds plan storage configuration
type PlanStorageConfig struct {
	DBPath string `mapstructure:"db_path"`
}

// ServerConfig holds server configuration
type ServerConfig struct {
	Host string     `mapstructure:"host"`
	Port int        `mapstructure:"port"`
	GRPC GRPCConfig `mapstructure:"grpc"`
}

// GRPCConfig holds gRPC specific configuration
type GRPCConfig struct {
	MaxRecvMsgSize    int             `mapstructure:"max_recv_msg_size"`
	MaxSendMsgSize    int             `mapstructure:"max_send_msg_size"`
	Keepalive         KeepaliveConfig `mapstructure:"keepalive"`
	ConnectionTimeout time.Duration   `mapstructure:"connection_timeout"`
}

// KeepaliveConfig holds keepalive configuration
type KeepaliveConfig struct {
	Time    time.Duration `mapstructure:"time"`
	Timeout time.Duration `mapstructure:"timeout"`
}

// DatabaseConfig holds database configuration
type DatabaseConfig struct {
	Host            string        `mapstructure:"host"`
	Port            int           `mapstructure:"port"`
	User            string        `mapstructure:"user"`
	Password        string        `mapstructure:"password"`
	Database        string        `mapstructure:"database"`
	SSLMode         string        `mapstructure:"ssl_mode"`
	MaxOpenConns    int           `mapstructure:"max_open_conns"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
}

// LLMConfig holds LLM provider configuration
type LLMConfig struct {
	DefaultProvider string           `mapstructure:"default_provider"`
	Streaming       bool             `mapstructure:"streaming"` // Enable streaming mode (default: false)
	Ollama          OllamaConfig     `mapstructure:"ollama"`
	OpenRouter      OpenRouterConfig `mapstructure:"openrouter"`
	Anthropic       AnthropicConfig  `mapstructure:"anthropic"`
}

// OllamaConfig holds Ollama configuration
type OllamaConfig struct {
	BaseURL  string        `mapstructure:"base_url"`
	Model    string        `mapstructure:"model"`
	Timeout  time.Duration `mapstructure:"timeout"`
	Thinking bool          `mapstructure:"thinking"`
}

// OpenRouterConfig holds OpenRouter configuration
type OpenRouterConfig struct {
	APIKey   string                 `mapstructure:"api_key"`
	BaseURL  string                 `mapstructure:"base_url"`
	Model    string                 `mapstructure:"model"`
	Timeout  time.Duration          `mapstructure:"timeout"`
	Provider map[string]interface{} `mapstructure:"provider"` // OpenRouter provider routing preferences
}

// AnthropicConfig holds Anthropic configuration
type AnthropicConfig struct {
	APIKey  string        `mapstructure:"api_key"`
	BaseURL string        `mapstructure:"base_url"`
	Model   string        `mapstructure:"model"`
	Timeout time.Duration `mapstructure:"timeout"`
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	Level          string `mapstructure:"level"`
	Format         string `mapstructure:"format"`
	Output         string `mapstructure:"output"`
	FilePath       string `mapstructure:"file_path"`
	ClearOnStartup bool   `mapstructure:"clear_on_startup"` // Clear logs directory on server startup
}

// ObservabilityConfig holds observability configuration
type ObservabilityConfig struct {
	EnableMetrics bool          `mapstructure:"enable_metrics"`
	EnableTracing bool          `mapstructure:"enable_tracing"`
	OTLP          OTLPConfig    `mapstructure:"otlp"`
	Metrics       MetricsConfig `mapstructure:"metrics"`
}

// OTLPConfig holds OTLP exporter configuration
type OTLPConfig struct {
	Endpoint string `mapstructure:"endpoint"`
	Insecure bool   `mapstructure:"insecure"`
}

// MetricsConfig holds metrics configuration
type MetricsConfig struct {
	Port int    `mapstructure:"port"`
	Path string `mapstructure:"path"`
}

// SecurityConfig holds security configuration
type SecurityConfig struct {
	APIKey     string `mapstructure:"api_key"`
	EnableAuth bool   `mapstructure:"enable_auth"`
}

// QAConfig holds QA configuration
type QAConfig struct {
	SnapshotPath             string `mapstructure:"snapshot_path"`
	EnableRealisticResponses bool   `mapstructure:"enable_realistic_responses"`
}

// PromptsConfig holds prompts configuration
type PromptsConfig struct {
	SystemPrompt     string `mapstructure:"system_prompt"`
	SupervisorPrompt string `mapstructure:"supervisor_prompt"`
	UrgencyWarning   string `mapstructure:"urgency_warning"`
	CodeAgentPrompt  string `mapstructure:"code_agent_prompt"`
	ResearcherPrompt string `mapstructure:"researcher_prompt"`
	ReviewerPrompt   string `mapstructure:"reviewer_prompt"`
}

// AgentConfig holds agent configuration
type AgentConfig struct {
	MaxSteps                      int                 `mapstructure:"max_steps"`
	MaxContextSize                int                 `mapstructure:"max_context_size"`  // Maximum context size in TOKENS (not characters). 1 token ≈ 4 chars.
	MaxTurnDuration               int                 `mapstructure:"max_turn_duration"` // Max seconds for a single LLM stream turn. 0 = default (120s).
	MaxStepDuration               int                 `mapstructure:"max_step_duration"` // Max seconds for a single ReAct step. 0 = disabled (opt-in watchdog).
	ContextLogPath                string              `mapstructure:"context_log_path"`
	ToolReturnDirectly            map[string]struct{} `mapstructure:"tool_return_directly"`
	EnableEnhancedToolCallChecker bool                `mapstructure:"enable_enhanced_tool_call_checker"`
	Prompts                       *PromptsConfig      `mapstructure:"prompts"`
}

// DefaultAgentConfig returns default configuration for agent
func DefaultAgentConfig() *AgentConfig {
	return &AgentConfig{
		MaxSteps:                      50, // Default: 50 steps to prevent infinite loops
		MaxContextSize:                16000,
		ContextLogPath:                "./logs",
		ToolReturnDirectly:            make(map[string]struct{}),
		EnableEnhancedToolCallChecker: true,
		Prompts:                       nil,
	}
}

// DefaultConfig returns a minimal Config with sensible defaults.
// Used when no config file is provided (e.g. Docker deployments with env vars only).
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 8443,
		},
		Logging: LoggingConfig{
			Level: "info",
		},
	}
}

// Load loads configuration from file and environment variables
func Load(configPath string) (*Config, error) {
	// Load .env file if it exists (optional)
	configDir := filepath.Dir(configPath)
	envPath := filepath.Join(configDir, ".env")
	if _, err := os.Stat(envPath); err == nil {
		if err := godotenv.Load(envPath); err != nil {
			return nil, fmt.Errorf("failed to load .env file: %w", err)
		}
	}

	v := viper.New()

	// Set config file path
	v.SetConfigFile(configPath)
	v.SetConfigType("yaml")

	// Read config file
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Enable environment variable override
	v.AutomaticEnv()

	// Unmarshal config
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Load prompts from prompts.yaml if it exists
	promptsPath := filepath.Join(configDir, "prompts.yaml")
	if _, err := os.Stat(promptsPath); err == nil {
		promptsV := viper.New()
		promptsV.SetConfigFile(promptsPath)
		promptsV.SetConfigType("yaml")
		if err := promptsV.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("failed to read prompts file: %w", err)
		}

		// Use intermediate struct to handle prompts section
		type promptsWrapper struct {
			Prompts *PromptsConfig `mapstructure:"prompts"`
		}
		var wrapper promptsWrapper
		if err := promptsV.Unmarshal(&wrapper); err != nil {
			return nil, fmt.Errorf("failed to unmarshal prompts: %w", err)
		}
		cfg.Agent.Prompts = wrapper.Prompts
	}

	// Store config directory for resolving sibling files (flows.yaml, etc.)
	cfg.ConfigDir = configDir

	// Validate config
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &cfg, nil
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.Server.Port < 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}

	if c.Database.Host == "" {
		return fmt.Errorf("database host is required")
	}

	if c.LLM.DefaultProvider == "" {
		return fmt.Errorf("default LLM provider is required")
	}

	// Validate AgentConfig
	// MaxSteps: negative values are invalid, 0 defaults to 50
	if c.Agent.MaxSteps < 0 {
		c.Agent.MaxSteps = 50 // Default to 50 if negative
	}
	if c.Agent.MaxSteps == 0 {
		c.Agent.MaxSteps = 50 // Default to 50 if not set
	}

	if c.Agent.MaxContextSize <= 0 {
		c.Agent.MaxContextSize = 16000
	}

	// MaxStepDuration: 0 = disabled (opt-in watchdog), so it is NOT coerced to a
	// non-zero default. Only clamp nonsensical negative values back to disabled.
	if c.Agent.MaxStepDuration < 0 {
		c.Agent.MaxStepDuration = 0
	}

	if c.Agent.ToolReturnDirectly == nil {
		c.Agent.ToolReturnDirectly = make(map[string]struct{})
	}

	// Validate PromptsConfig - system_prompt is required
	if c.Agent.Prompts == nil || c.Agent.Prompts.SystemPrompt == "" {
		return fmt.Errorf("system_prompt is required in prompts.yaml")
	}

	return nil
}
