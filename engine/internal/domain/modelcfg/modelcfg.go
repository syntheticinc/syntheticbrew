// Package modelcfg holds the provider-type and base_url invariants shared by
// every model-config facade — the REST model handler and the MCP admin model
// tool. Keeping the canonical type list, the openrouter→openai_compatible
// normalization, and the base_url format check in one place stops the facades
// from diverging (e.g. an MCP-created "openrouter" model bypassing
// canonicalization and tripping the DB chk_models_type constraint).
package modelcfg

import "net/url"

// validTypes are the provider types accepted at the input boundary. All except
// "openrouter" are canonical persistence values (they match the models.type DB
// check constraint); "openrouter" is an accepted alias that Canonicalize
// rewrites to openai_compatible.
var validTypes = map[string]bool{
	"ollama":            true,
	"openai_compatible": true,
	"anthropic":         true,
	"azure_openai":      true,
	"openrouter":        true,
}

// TypeError is the shared invalid-type message.
const TypeError = "type must be one of: ollama, openai_compatible, anthropic, azure_openai, openrouter"

// IsValidType reports whether t is an accepted provider type (including the
// openrouter alias).
func IsValidType(t string) bool { return validTypes[t] }

// Canonicalize rewrites the openrouter alias to openai_compatible, filling in
// the default base URL when none was supplied. Non-alias types pass through
// unchanged.
func Canonicalize(providerType, baseURL string) (string, string) {
	if providerType == "openrouter" {
		if baseURL == "" {
			baseURL = "https://openrouter.ai/api/v1"
		}
		return "openai_compatible", baseURL
	}
	return providerType, baseURL
}

// ValidateBaseURL returns a non-empty message when raw is not a well-formed
// absolute http(s) URL. It intentionally does NOT block private/localhost hosts
// — self-hosted CE legitimately targets localhost/on-prem gateways; egress
// policy for multi-tenant deployments belongs in the plugin layer, not here.
func ValidateBaseURL(raw string) string {
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return "base_url must be a valid absolute URL (http:// or https://)"
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "base_url scheme must be http or https"
	}
	if u.Host == "" {
		return "base_url must include a host"
	}
	return ""
}
