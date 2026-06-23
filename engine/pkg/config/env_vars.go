package config

// Env var names — single registry.
//
// All `os.Getenv` calls for application configuration MUST live inside
// `pkg/config` and reference these constants instead of raw strings. New env
// vars MUST be added here, bound via Viper in `bindEnvVars`, declared as a
// typed field in `BootstrapConfig`, and documented in `.env.example`.
//
// See `.claude/rules/code-review.md` for the enforced env-vars policy.
const (
	EnvDatabaseURL        = "DATABASE_URL"
	EnvEngineHost         = "ENGINE_HOST"
	EnvEnginePort         = "ENGINE_PORT"
	EnvInternalPort       = "SYNTHETICBREW_INTERNAL_PORT"
	EnvCORSOrigins        = "SYNTHETICBREW_CORS_ORIGINS"
	EnvAuthMode           = "SYNTHETICBREW_AUTH_MODE"
	EnvJWTKeysDir         = "SYNTHETICBREW_JWT_KEYS_DIR"
	EnvJWTPublicKeyPath   = "SYNTHETICBREW_JWT_PUBLIC_KEY_PATH"
	EnvLocalSessionTTL    = "SYNTHETICBREW_LOCAL_SESSION_TTL"
	EnvEmbedURL           = "EMBED_URL"
	EnvEmbedModel         = "EMBED_MODEL"
	EnvEmbedDim           = "EMBED_DIM"
	EnvDebugModel         = "SYNTHETICBREW_DEBUG_MODEL"
	EnvDocsMCPURL         = "SYNTHETICBREW_DOCS_MCP_URL"
	EnvDisableLSPDownload    = "SYNTHETICBREW_DISABLE_LSP_DOWNLOAD"
	EnvVersionsURL           = "SYNTHETICBREW_VERSIONS_URL"
	EnvBootstrapAdminToken   = "SYNTHETICBREW_BOOTSTRAP_ADMIN_TOKEN"
)
