package plugin

// JWTVerifier verifies a JWT token and returns the decoded claims.
//
// Implementations are plugged into the server via Plugin.JWTVerifier().
// When nil, the server falls back to a default HMAC shared-secret verifier.
type JWTVerifier interface {
	Verify(token string) (Claims, error)
}

// Claims contains the minimal set of JWT claims the engine needs.
//
// Subject identifies the authenticated principal (user UUID in the default
// flow). TenantID, when non-empty, scopes the request to a workspace.
// Scopes is a bitmask matching the ScopeXxx constants below.
type Claims struct {
	Subject  string
	TenantID string
	Scopes   int
}

// Scope constants mirrored here so verifier implementations (e.g. an external EdDSA
// verifier) can set correct bitmasks without importing internal/delivery/http.
// Values must stay in sync with the ScopeXxx constants in that package.
const (
	ScopeChat        = 1
	ScopeTasks       = 2
	ScopeAgentsRead  = 4
	ScopeConfig      = 8
	ScopeAdmin       = 16
	ScopeAgentsWrite = 32
	ScopeModelsRead  = 64
	ScopeModelsWrite = 128
	ScopeMCPRead     = 256
)
