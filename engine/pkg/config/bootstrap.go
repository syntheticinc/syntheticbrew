package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// BootstrapConfig is the minimal config loaded from YAML before connecting to the database.
// All runtime configuration (agents, models, etc.) lives in the database.
//
// Every field below is sourced from one of:
//  1. YAML at the engine config path,
//  2. an environment variable bound via Viper (see env_vars.go + bindEnvVars),
//  3. or a built-in default (see setBootstrapDefaults).
//
// Adding a new env var requires:
//   - a const in env_vars.go,
//   - a typed field here,
//   - a v.BindEnv() call in bindEnvVars,
//   - a default in setBootstrapDefaults if applicable,
//   - a line in .env.example.
type BootstrapConfig struct {
	Engine     EngineBootstrap   `mapstructure:"engine"`
	Database   BootstrapDatabase `mapstructure:"database"`
	Security   BootstrapSecurity `mapstructure:"security"`
	Logging    BootstrapLogging  `mapstructure:"logging"`
	Embeddings EmbeddingsConfig  `mapstructure:"embeddings"`
	Debug      DebugConfig       `mapstructure:"debug"`
	MCP        MCPBootstrap      `mapstructure:"mcp"`
	LSP        LSPBootstrap      `mapstructure:"lsp"`
	Updates    UpdatesConfig     `mapstructure:"updates"`
	Seed       SeedConfig        `mapstructure:"seed"`
	BYOK       BootstrapBYOK     `mapstructure:"byok"`
	OAuth      BootstrapOAuth    `mapstructure:"oauth"`
}

// BootstrapOAuth configures the embedded OAuth 2.1 authorization server that
// mints audience-bound access tokens for the MCP client flow.
//
// The server is default-on but degrades gracefully: when ASEnabled is true but
// no issuer can be resolved (neither Issuer nor Engine.PublicBaseURL set) the
// wiring logs a warning and disables the server rather than refusing to boot,
// so existing installs without a public base URL are unaffected.
type BootstrapOAuth struct {
	// ASEnabled turns the authorization server on. Defaults to true.
	ASEnabled bool `mapstructure:"as_enabled"`
	// Issuer is the authorization-server base URL. Empty falls back to
	// Engine.PublicBaseURL; if both are empty the server auto-disables.
	Issuer string `mapstructure:"issuer"`
	// ConsentURL is the consent page advertised as the authorization_endpoint.
	// Empty defaults to Issuer + "/admin/oauth/consent".
	ConsentURL string `mapstructure:"consent_url"`
	// ASKeyPath is the hex-encoded authorization-server private key file, loaded
	// only in external auth mode where the key is a pre-provisioned Secret
	// identical across replicas. Empty in local mode (the key is generated and
	// persisted next to the local-admin session key).
	ASKeyPath string `mapstructure:"as_key_path"`
}

// EngineBootstrap holds the minimal engine settings needed at startup.
type EngineBootstrap struct {
	Host         string   `mapstructure:"host"`
	Port         int      `mapstructure:"port"`          // External/data plane port (default 8443)
	InternalPort int      `mapstructure:"internal_port"` // Control plane port (default 0 = single-port mode)
	CORSOrigins  []string `mapstructure:"cors_origins"`  // Allowed CORS origins for external port (empty = allow all)
	DataDir      string   `mapstructure:"data_dir"`
	// PublicBaseURL is the deployment's public origin (e.g. https://engine.example.com),
	// used to build absolute URLs the engine cannot infer behind a reverse proxy
	// (currently the widget embed snippet). Empty = emit a placeholder.
	PublicBaseURL string `mapstructure:"public_base_url"`
}

// BootstrapDatabase holds the database connection settings.
type BootstrapDatabase struct {
	URL string `mapstructure:"url"` // PostgreSQL connection string
}

// BootstrapLogging holds logging settings loaded at startup.
type BootstrapLogging struct {
	Level string `mapstructure:"level"`
}

// AuthMode values.
const (
	// AuthModeLocal: CE single-node — engine signs its own Ed25519 keypair on
	// first boot, issues short-lived admin sessions via POST /auth/local-session,
	// `sub` is the synthetic `local-admin`, `tenant_id` is empty.
	AuthModeLocal = "local"
	// AuthModeExternal: hosted — tokens are signed by an external issuer
	// (landing service). Engine loads only the public key; there is no
	// /auth/local-session route.
	AuthModeExternal = "external"
)

// BootstrapSecurity holds auth-related settings loaded at startup.
//
// There is no shared HMAC secret any more (Wave 1+7). All JWTs are EdDSA.
// In local mode the engine generates its keypair automatically and
// persists it under JWTKeysDir. In external mode JWTPublicKeyPath points at
// the issuer's public key.
type BootstrapSecurity struct {
	// AuthMode selects local vs external JWT issuance.
	// Accepts "local" or "external"; defaults to "local" when empty.
	AuthMode string `mapstructure:"auth_mode"`

	// JWTKeysDir is the directory where the local Ed25519 keypair is stored.
	// Used only when AuthMode == "local". Defaults to <data_dir>/keys.
	JWTKeysDir string `mapstructure:"jwt_keys_dir"`

	// JWTPublicKeyPath is the path to the Ed25519 public key of the external
	// issuer. Required when AuthMode == "external".
	JWTPublicKeyPath string `mapstructure:"jwt_public_key_path"`

	// JWTExpectedAudience is the canonical URI this deployment accepts as the
	// `aud` claim of an audience-bound JWT (e.g. its public MCP endpoint URL).
	// Empty (default) rejects every token that carries an aud claim; tokens
	// without one are unaffected either way. Optional in both auth modes.
	JWTExpectedAudience string `mapstructure:"jwt_expected_audience"`

	// LocalSessionTTL is the lifetime of admin sessions issued by
	// /api/v1/auth/local-session in local mode. Defaults to 1h. Standard
	// Go duration syntax (e.g. "30m", "2h").
	LocalSessionTTL time.Duration `mapstructure:"local_session_ttl"`
}

// EmbeddingsConfig holds Ollama-style embeddings client settings. Empty fields
// fall back to the indexing package defaults at the consumer.
type EmbeddingsConfig struct {
	URL   string `mapstructure:"url"`
	Model string `mapstructure:"model"`
	Dim   int    `mapstructure:"dim"`
}

// DebugConfig holds developer-only debug switches.
type DebugConfig struct {
	// ModelDebugDir, when non-empty, makes the LLM factory wrap chat models
	// with a logger that dumps every request/response to that directory.
	ModelDebugDir string `mapstructure:"model_debug_dir"`
}

// MCPBootstrap holds MCP-related bootstrap overrides (mostly for the seeded
// syntheticbrew-docs catalog entry — useful in tests / staging).
type MCPBootstrap struct {
	DocsURL string `mapstructure:"docs_url"`
}

// LSPBootstrap holds language-server installer toggles.
type LSPBootstrap struct {
	DisableDownload bool `mapstructure:"disable_download"`
}

// UpdatesConfig overrides the version-check endpoint (default: api.syntheticbrew.ai).
type UpdatesConfig struct {
	VersionsURL string `mapstructure:"versions_url"`
}

// SeedConfig holds seed-time overrides applied once on engine startup.
type SeedConfig struct {
	// BootstrapAdminToken, when non-empty, causes the engine to seed an admin
	// API token in the api_tokens table (idempotent — skipped when a token
	// named "bootstrap-admin" already exists). Enables automated k8s GitOps
	// reconcile without manual Admin UI token generation.
	//
	// Format: bb_<64 lowercase hex chars>.
	// Generate: echo "bb_$(openssl rand -hex 32)"
	// Scope: admin (mask=16). Name: "bootstrap-admin".
	BootstrapAdminToken string `mapstructure:"bootstrap_admin_token"`
}

// BootstrapBYOK holds env/YAML-driven BYOK enablement for GitOps-declarative
// control. When an operator supplies these (chart values → env), they are
// reconciled into the settings table on every boot (see reconcileBYOKConfig)
// so the declared state wins over Admin-UI edits. When unset, BYOK is managed
// entirely through the Admin Settings API (Managed* stay false).
type BootstrapBYOK struct {
	Enabled          bool     `mapstructure:"enabled"`
	AllowedProviders []string `mapstructure:"allowed_providers"`
	// ManagedEnabled / ManagedProviders record whether the operator explicitly
	// set the corresponding env var. Not mapstructure-decoded — set in
	// LoadBootstrap via os.LookupEnv. They gate whether reconcile overwrites.
	ManagedEnabled   bool `mapstructure:"-"`
	ManagedProviders bool `mapstructure:"-"`
}

// LoadBootstrap loads the bootstrap config from a YAML file plus environment
// variables. Env vars are bound via Viper (see bindEnvVars) and take
// precedence over YAML; YAML wins over built-in defaults.
//
// When the YAML path does not exist (or is empty) we still construct the
// config from env vars + defaults — this enables zero-config Docker deploys
// where DATABASE_URL is the only mandatory input.
func LoadBootstrap(path string) (*BootstrapConfig, error) {
	if path == "" {
		return nil, fmt.Errorf("config path is required")
	}

	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	bindEnvVars(v)
	setBootstrapDefaults(v)

	if readErr := v.ReadInConfig(); readErr != nil {
		// Config file not found — fall back to env-only mode.
		return loadBootstrapEnvOnly(v)
	}

	var cfg BootstrapConfig
	if err := unmarshalBootstrap(v, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal bootstrap config: %w", err)
	}
	applyBYOKManaged(&cfg)

	expandBootstrapEnvVars(&cfg)
	applySecurityDefaults(&cfg)

	if err := validateBootstrap(&cfg); err != nil {
		return nil, fmt.Errorf("validate bootstrap config: %w", err)
	}

	// Resolve DataDir relative to config file directory if not absolute
	if cfg.Engine.DataDir != "" && !filepath.IsAbs(cfg.Engine.DataDir) {
		configDir := filepath.Dir(path)
		cfg.Engine.DataDir = filepath.Join(configDir, cfg.Engine.DataDir)
	}

	return &cfg, nil
}

// loadBootstrapEnvOnly builds the bootstrap config from env vars + defaults
// when no YAML config file is present — zero-config Docker deploys where
// DATABASE_URL is the only mandatory input. Validation still runs because
// DATABASE_URL is required regardless of source.
func loadBootstrapEnvOnly(v *viper.Viper) (*BootstrapConfig, error) {
	var cfg BootstrapConfig
	if err := unmarshalBootstrap(v, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal env-only bootstrap: %w", err)
	}
	applyBYOKManaged(&cfg)
	if cfg.Database.URL == "" {
		return nil, fmt.Errorf("no config file found and DATABASE_URL environment variable is not set")
	}
	applySecurityDefaults(&cfg)
	if err := validateBootstrap(&cfg); err != nil {
		return nil, fmt.Errorf("validate env-based config: %w", err)
	}
	return &cfg, nil
}

// unmarshalBootstrap decodes Viper state into BootstrapConfig with the
// hooks needed to handle slice-from-env (CORSOrigins) and duration parsing
// (LocalSessionTTL).
func unmarshalBootstrap(v *viper.Viper, cfg *BootstrapConfig) error {
	return v.Unmarshal(cfg, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		// Comma-separated env strings → []string for fields like CORSOrigins.
		// Each element is trimmed; empty elements are dropped (matches the
		// pre-Viper splitAndTrim semantics callers and tests rely on).
		stringToTrimmedSliceHookFunc(","),
		// "1h" / "30m" → time.Duration for LocalSessionTTL.
		mapstructure.StringToTimeDurationHookFunc(),
	)))
}

// applyBYOKManaged records whether the operator explicitly supplied the BYOK
// env vars. Presence (not value) decides reconcile — os.LookupEnv is the
// unambiguous signal and is policy-allowed inside pkg/config.
func applyBYOKManaged(cfg *BootstrapConfig) {
	_, cfg.BYOK.ManagedEnabled = os.LookupEnv(EnvBYOKEnabled)
	_, cfg.BYOK.ManagedProviders = os.LookupEnv(EnvBYOKAllowedProviders)
}

// stringToTrimmedSliceHookFunc returns a mapstructure DecodeHookFunc that
// converts string → []string by splitting on sep and trimming whitespace
// from each element. Empty elements are excluded. Mirrors the semantics of
// the legacy splitAndTrim helper so the env vars `SYNTHETICBREW_CORS_ORIGINS=
// "a, b, c"` parse identically before and after the Viper migration.
func stringToTrimmedSliceHookFunc(sep string) mapstructure.DecodeHookFunc {
	return func(from reflect.Type, to reflect.Type, data any) (any, error) {
		if from.Kind() != reflect.String {
			return data, nil
		}
		if to != reflect.SliceOf(from) {
			return data, nil
		}
		raw, ok := data.(string)
		if !ok {
			return data, nil
		}
		return splitAndTrim(raw, sep), nil
	}
}

// bindEnvVars maps each Viper key (mapstructure path) to its env var. This is
// the single registry that must stay in sync with env_vars.go.
func bindEnvVars(v *viper.Viper) {
	bindings := map[string]string{
		"database.url":                   EnvDatabaseURL,
		"engine.host":                    EnvEngineHost,
		"engine.port":                    EnvEnginePort,
		"engine.internal_port":           EnvInternalPort,
		"engine.cors_origins":            EnvCORSOrigins,
		"engine.public_base_url":         EnvPublicBaseURL,
		"security.auth_mode":             EnvAuthMode,
		"security.jwt_keys_dir":          EnvJWTKeysDir,
		"security.jwt_public_key_path":   EnvJWTPublicKeyPath,
		"security.jwt_expected_audience": EnvJWTExpectedAudience,
		"security.local_session_ttl":     EnvLocalSessionTTL,
		"embeddings.url":                 EnvEmbedURL,
		"embeddings.model":               EnvEmbedModel,
		"embeddings.dim":                 EnvEmbedDim,
		"debug.model_debug_dir":          EnvDebugModel,
		"mcp.docs_url":                   EnvDocsMCPURL,
		"lsp.disable_download":           EnvDisableLSPDownload,
		"updates.versions_url":           EnvVersionsURL,
		"seed.bootstrap_admin_token":     EnvBootstrapAdminToken,
		"byok.enabled":                   EnvBYOKEnabled,
		"byok.allowed_providers":         EnvBYOKAllowedProviders,
		"oauth.as_enabled":               EnvOAuthASEnabled,
		"oauth.issuer":                   EnvOAuthIssuer,
		"oauth.consent_url":              EnvOAuthConsentURL,
		"oauth.as_key_path":              EnvOAuthASKeyPath,
	}
	for key, env := range bindings {
		// BindEnv associates a Viper key with one or more env var names.
		// Errors only occur when the key is empty, which we control.
		_ = v.BindEnv(key, env)
	}
}

// setBootstrapDefaults registers built-in defaults. YAML and env vars layered
// on top via Viper precedence (explicit Set > flag > env > config > default).
//
// We deliberately do NOT set defaults for engine.host / engine.port here —
// pre-Viper LoadBootstrap left those zero when omitted from YAML, and
// downstream code in server.go has its own fallback (port 8443, host
// 0.0.0.0). Setting Viper defaults would change observable test behavior
// for the minimal-YAML case. Env-only mode (DATABASE_URL without YAML)
// still gets host/port via the env bindings.
func setBootstrapDefaults(v *viper.Viper) {
	v.SetDefault("security.auth_mode", AuthModeLocal)
	v.SetDefault("security.local_session_ttl", time.Hour)
	// OAuth authorization server is default-on. It auto-disables at wiring time
	// when no issuer can be resolved (see BootstrapOAuth), so this default is
	// safe for installs that never set a public base URL.
	v.SetDefault("oauth.as_enabled", true)
	// Embeddings defaults intentionally omitted — the indexing package owns
	// the canonical defaults (DefaultOllamaURL / DefaultEmbedModel /
	// DefaultDimension); the consumer fills them when the field is empty.
}

// applySecurityDefaults fills missing auth settings with sensible defaults
// after env overrides. Called after LoadBootstrap so YAML-provided keys win
// over defaults.
func applySecurityDefaults(cfg *BootstrapConfig) {
	if cfg.Security.AuthMode == "" {
		cfg.Security.AuthMode = AuthModeLocal
	}
	if cfg.Security.AuthMode == AuthModeLocal && cfg.Security.JWTKeysDir == "" {
		cfg.Security.JWTKeysDir = filepath.Join(cfg.Engine.DataDirOrDefault(), "keys")
	}
	if cfg.Security.LocalSessionTTL <= 0 {
		cfg.Security.LocalSessionTTL = time.Hour
	}
}

// expandBootstrapEnvVars expands ${VAR} placeholders in all string fields of BootstrapConfig.
func expandBootstrapEnvVars(cfg *BootstrapConfig) {
	cfg.Engine.Host = expandEnvVars(cfg.Engine.Host)
	cfg.Engine.DataDir = expandEnvVars(cfg.Engine.DataDir)
	cfg.Engine.PublicBaseURL = expandEnvVars(cfg.Engine.PublicBaseURL)
	cfg.Database.URL = expandEnvVars(cfg.Database.URL)
	cfg.Security.AuthMode = expandEnvVars(cfg.Security.AuthMode)
	cfg.Security.JWTKeysDir = expandEnvVars(cfg.Security.JWTKeysDir)
	cfg.Security.JWTPublicKeyPath = expandEnvVars(cfg.Security.JWTPublicKeyPath)
	cfg.Security.JWTExpectedAudience = expandEnvVars(cfg.Security.JWTExpectedAudience)
	cfg.Logging.Level = expandEnvVars(cfg.Logging.Level)
	cfg.Embeddings.URL = expandEnvVars(cfg.Embeddings.URL)
	cfg.Embeddings.Model = expandEnvVars(cfg.Embeddings.Model)
	cfg.Debug.ModelDebugDir = expandEnvVars(cfg.Debug.ModelDebugDir)
	cfg.MCP.DocsURL = expandEnvVars(cfg.MCP.DocsURL)
	cfg.Updates.VersionsURL = expandEnvVars(cfg.Updates.VersionsURL)
	cfg.Seed.BootstrapAdminToken = expandEnvVars(cfg.Seed.BootstrapAdminToken)
	cfg.OAuth.Issuer = expandEnvVars(cfg.OAuth.Issuer)
	cfg.OAuth.ConsentURL = expandEnvVars(cfg.OAuth.ConsentURL)
	cfg.OAuth.ASKeyPath = expandEnvVars(cfg.OAuth.ASKeyPath)
}

// validateBootstrap checks that required bootstrap fields are present.
func validateBootstrap(cfg *BootstrapConfig) error {
	if cfg.Database.URL == "" {
		return fmt.Errorf("database.url is required")
	}
	if cfg.Engine.Port < 0 || cfg.Engine.Port > 65535 {
		return fmt.Errorf("invalid engine port: %d", cfg.Engine.Port)
	}
	if cfg.Engine.InternalPort < 0 || cfg.Engine.InternalPort > 65535 {
		return fmt.Errorf("invalid internal port: %d", cfg.Engine.InternalPort)
	}
	if cfg.Engine.InternalPort > 0 && cfg.Engine.InternalPort == cfg.Engine.Port {
		return fmt.Errorf("internal_port (%d) must differ from port (%d)", cfg.Engine.InternalPort, cfg.Engine.Port)
	}
	switch cfg.Security.AuthMode {
	case AuthModeLocal:
		if cfg.Security.JWTKeysDir == "" {
			return fmt.Errorf("security.jwt_keys_dir is required when auth_mode=local")
		}
	case AuthModeExternal:
		if cfg.Security.JWTPublicKeyPath == "" {
			return fmt.Errorf("security.jwt_public_key_path is required when auth_mode=external")
		}
	default:
		return fmt.Errorf("invalid auth_mode %q (expected %q or %q)",
			cfg.Security.AuthMode, AuthModeLocal, AuthModeExternal)
	}
	return nil
}

// DefaultBootstrapConfig returns sensible defaults for BootstrapConfig.
//
// This is the legacy in-memory default used by tests and a few callers; the
// LoadBootstrap path uses Viper-managed defaults instead. We keep the same
// values so existing tests continue to pass.
func DefaultBootstrapConfig() *BootstrapConfig {
	return &BootstrapConfig{
		Engine: EngineBootstrap{
			Host: "127.0.0.1",
			Port: 8080,
		},
		Logging: BootstrapLogging{
			Level: "info",
		},
		Security: BootstrapSecurity{
			AuthMode:        AuthModeLocal,
			LocalSessionTTL: time.Hour,
		},
	}
}

// DSN returns the database connection string.
// It returns the URL directly since BootstrapDatabase uses a connection string.
func (d *BootstrapDatabase) DSN() string {
	return d.URL
}

// DataDirOrDefault returns the configured data directory or a platform-appropriate default.
func (e *EngineBootstrap) DataDirOrDefault() string {
	if e.DataDir != "" {
		return e.DataDir
	}

	// Default to user config dir + syntheticbrew
	dir, err := os.UserConfigDir()
	if err != nil {
		return "./data"
	}
	return filepath.Join(dir, "syntheticbrew")
}
