package http

import (
	"context"
	"net/http"
	"strings"

	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

const (
	// ContextKeyBYOKProvider holds the provider name from the BYOK provider header.
	ContextKeyBYOKProvider contextKey = "byok_provider"
	// ContextKeyBYOKAPIKey holds the API key from the BYOK API key header.
	ContextKeyBYOKAPIKey contextKey = "byok_api_key"
	// ContextKeyBYOKModel holds the model name from the BYOK model header.
	ContextKeyBYOKModel contextKey = "byok_model"
	// ContextKeyBYOKBaseURL holds the optional base URL override from the BYOK base-URL header.
	ContextKeyBYOKBaseURL contextKey = "byok_base_url"
)

// BYOK request header names (V2 §5.8).
const (
	headerBYOKProvider = "X-BYOK-Provider"
	headerBYOKAPIKey   = "X-BYOK-API-Key"
	headerBYOKModel    = "X-BYOK-Model"
	headerBYOKBaseURL  = "X-BYOK-Base-URL"
)

// BYOKConfig holds the resolved per-tenant BYOK configuration for a request.
// It is produced by a BYOKConfigResolver from the tenant's `settings` rows, so
// one tenant's toggle never affects another (V2 §5.8).
type BYOKConfig struct {
	Enabled          bool
	AllowedProviders []string // e.g. ["openai", "anthropic", "openrouter"]
}

// BYOKConfigResolver returns the BYOK configuration for the request's tenant.
// Defined consumer-side (delivery) so the middleware never imports the app
// layer. The concrete resolver (in package app) reads the tenant-scoped
// settings rows and fails closed on an unresolvable tenant.
type BYOKConfigResolver interface {
	Resolve(ctx context.Context) BYOKConfig
}

// customBaseURLProviders require an explicit allowlist entry even when the
// allowlist is empty (F1). openai_compatible and ollama accept a user base URL,
// so an empty "allow-all" list must NOT silently expose them on the untrusted
// end-user path (a zero-config tenant would otherwise route ollama at the
// engine's own localhost). A tenant opts into these by listing them.
var customBaseURLProviders = map[string]bool{
	"openai_compatible": true,
	"ollama":            true,
}

// pinnedBYOKProviders use a fixed hosted endpoint (F3). An end-user base-URL
// override for them is illegitimate and rejected at the boundary with 400 —
// duplicated delivery-side so the middleware need not import the infra layer.
var pinnedBYOKProviders = map[string]bool{
	"openai":     true,
	"openrouter": true,
	"anthropic":  true,
}

// untrustedBaseURLPolicy validates a user-supplied BYOK base URL at the URL
// layer (scheme, metadata hostname, literal private IP). It mirrors the
// engine-owned untrusted deny-private baseline so a rejection surfaces as a
// clean 400 before execution; the dial-time Control check remains the
// DNS-rebinding backstop.
var untrustedBaseURLPolicy plugin.EgressPolicy = plugin.DenyPrivateEgressPolicy{}

// BYOKMiddleware parses BYOK headers and injects them into request context.
// The active configuration is resolved per-tenant on each request so a
// tenant-admin's toggle is isolated to that tenant.
type BYOKMiddleware struct {
	resolver BYOKConfigResolver
}

// NewBYOKMiddleware creates a new BYOKMiddleware backed by resolver.
func NewBYOKMiddleware(resolver BYOKConfigResolver) *BYOKMiddleware {
	return &BYOKMiddleware{resolver: resolver}
}

// providerAllowed reports whether provider may be used under allowlist. A
// non-empty allowlist is authoritative. An empty allowlist means allow-all
// EXCEPT the custom-base_url providers, which always require explicit listing.
func providerAllowed(provider string, allowlist map[string]struct{}) bool {
	if len(allowlist) > 0 {
		_, ok := allowlist[provider]
		return ok
	}
	return !customBaseURLProviders[provider]
}

// InjectBYOK is middleware that reads BYOK headers and adds them to context.
// If BYOK is disabled or headers are absent, the request passes through unchanged.
//
// It must be mounted only under mandatory Authenticate+Tenant middleware — the
// resolver fails closed on a missing tenant, so an optional-auth mount would
// wrongly reject or (worse) mis-scope requests.
func (m *BYOKMiddleware) InjectBYOK(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provider := r.Header.Get(headerBYOKProvider)
		apiKey := r.Header.Get(headerBYOKAPIKey)
		modelName := r.Header.Get(headerBYOKModel)
		baseURL := r.Header.Get(headerBYOKBaseURL)

		// No BYOK headers present — pass through.
		if provider == "" && apiKey == "" && modelName == "" && baseURL == "" {
			next.ServeHTTP(w, r)
			return
		}

		cfg := m.resolver.Resolve(r.Context())
		if !cfg.Enabled {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "BYOK is disabled"})
			return
		}

		if provider == "" || apiKey == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "X-BYOK-Provider and X-BYOK-API-Key are required for BYOK"})
			return
		}

		allowlist := make(map[string]struct{}, len(cfg.AllowedProviders))
		for _, p := range cfg.AllowedProviders {
			if p = strings.TrimSpace(p); p != "" {
				allowlist[strings.ToLower(p)] = struct{}{}
			}
		}

		providerLower := strings.ToLower(provider)
		if !providerAllowed(providerLower, allowlist) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "provider not allowed: " + provider})
			return
		}

		// Base-URL policy (F3 + untrusted deny-private). Enforced here so a
		// violation is a clean 400 at the boundary rather than a silent
		// fallback to the tenant model deep in the turn executor.
		if baseURL != "" {
			if pinnedBYOKProviders[providerLower] {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "base_url override is not permitted for provider " + provider})
				return
			}
			if err := untrustedBaseURLPolicy.CheckURL(baseURL); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "base URL not permitted"})
				return
			}
		}

		ctx := context.WithValue(r.Context(), ContextKeyBYOKProvider, providerLower)
		ctx = context.WithValue(ctx, ContextKeyBYOKAPIKey, apiKey)
		if modelName != "" {
			ctx = context.WithValue(ctx, ContextKeyBYOKModel, modelName)
		}
		if baseURL != "" {
			ctx = context.WithValue(ctx, ContextKeyBYOKBaseURL, baseURL)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
