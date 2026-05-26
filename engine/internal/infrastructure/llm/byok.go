package llm

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

// BYOKCredentials carries per-request, per-end-user model credentials
// extracted from the X-BYOK-* request headers (V2 §5.8). When present in
// the request context, the LLM factory builds an ad-hoc ChatModel from
// these values instead of using the tenant-configured model.
//
// The struct lives in the llm package (not delivery/http) so that the
// turn executor factory can consume it without pulling a delivery-layer
// import. The HTTP middleware translates its own context keys into a
// BYOKCredentials value and re-attaches it via WithBYOKCredentials.
type BYOKCredentials struct {
	Provider string // canonical provider id, lowercased (e.g. "openai", "anthropic")
	APIKey   string // raw user-supplied API key — must never be logged
	Model    string // optional model name; falls back to provider default
	BaseURL  string // optional base URL override (e.g. self-hosted gateway)
}

// byokCtxKey is unexported so callers must use WithBYOKCredentials /
// BYOKCredentialsFrom to read and write — keeps the key shape stable
// even if its concrete type changes later.
type byokCtxKey struct{}

// WithBYOKCredentials returns a derived context that carries creds.
// Passing nil is a no-op (returns ctx unchanged).
func WithBYOKCredentials(ctx context.Context, creds *BYOKCredentials) context.Context {
	if creds == nil {
		return ctx
	}
	return context.WithValue(ctx, byokCtxKey{}, creds)
}

// BYOKCredentialsFrom returns the BYOK credentials attached to ctx, or
// nil when none are present. Callers must treat a nil result as
// "fall back to the tenant-configured model".
func BYOKCredentialsFrom(ctx context.Context) *BYOKCredentials {
	if ctx == nil {
		return nil
	}
	creds, _ := ctx.Value(byokCtxKey{}).(*BYOKCredentials)
	return creds
}

// RedactAPIKey returns a non-sensitive representation of an API key for
// logging purposes — first 4 chars, last 4 chars, length stamp. Never
// returns the raw key.
func RedactAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "***"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

// BuildBYOKChatModel constructs an ad-hoc ToolCallingChatModel from
// per-end-user credentials. The provider id selects the wire shape:
//
//   - openai / openai_compatible / openrouter / ollama: OpenAI-compatible
//     chat completions endpoint. base_url defaults to OpenAI when omitted.
//   - anthropic: OpenAI-compatible adapter with the anthropic-version
//     header injected by anthropicTransport (mirrors createAnthropicModel).
//
// Other providers are explicitly unsupported for BYOK to keep the
// surface narrow and audited. Adding one is a small change but should
// be reviewed alongside the allowed_providers config.
func BuildBYOKChatModel(ctx context.Context, creds BYOKCredentials) (model.ToolCallingChatModel, error) {
	if creds.APIKey == "" {
		return nil, fmt.Errorf("byok: api key required")
	}
	if creds.Provider == "" {
		return nil, fmt.Errorf("byok: provider required")
	}

	provider := strings.ToLower(creds.Provider)

	var cfg *openai.ChatModelConfig

	switch provider {
	case "openai":
		cfg = &openai.ChatModelConfig{
			BaseURL: defaultBaseURL(creds.BaseURL, "https://api.openai.com/v1"),
			Model:   defaultString(creds.Model, "gpt-4o-mini"),
			APIKey:  creds.APIKey,
		}

	case "openrouter":
		cfg = &openai.ChatModelConfig{
			BaseURL: defaultBaseURL(creds.BaseURL, "https://openrouter.ai/api/v1"),
			Model:   creds.Model, // no sensible default — must be supplied
			APIKey:  creds.APIKey,
		}

	case "openai_compatible":
		// User supplies their own base URL (e.g. self-hosted vLLM, LM
		// Studio). No default — without base URL the call cannot route.
		if creds.BaseURL == "" {
			return nil, fmt.Errorf("byok: base_url required for openai_compatible provider")
		}
		cfg = &openai.ChatModelConfig{
			BaseURL: creds.BaseURL,
			Model:   creds.Model,
			APIKey:  creds.APIKey,
		}

	case "ollama":
		baseURL := defaultBaseURL(creds.BaseURL, "http://localhost:11434/v1")
		// Auto-promote /api → /v1 for the OpenAI-compatible adapter.
		if strings.HasSuffix(baseURL, "/api") {
			baseURL = strings.TrimSuffix(baseURL, "/api") + "/v1"
		}
		if !strings.Contains(baseURL, "/v1") {
			baseURL = strings.TrimRight(baseURL, "/") + "/v1"
		}
		cfg = &openai.ChatModelConfig{
			BaseURL: baseURL,
			Model:   creds.Model,
			APIKey:  creds.APIKey, // Ollama ignores but field is required
		}

	case "anthropic":
		httpClient := &http.Client{}
		httpClient.Transport = &anthropicCacheTransport{base: http.DefaultTransport}
		cfg = &openai.ChatModelConfig{
			BaseURL:    defaultBaseURL(creds.BaseURL, "https://api.anthropic.com/v1"),
			Model:      defaultString(creds.Model, "claude-3-5-sonnet-20241022"),
			APIKey:     creds.APIKey,
			HTTPClient: httpClient,
		}

	default:
		return nil, fmt.Errorf("byok: unsupported provider %q", creds.Provider)
	}

	client, err := openai.NewChatModel(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return WrapWithRetry(client), nil
}

// defaultString returns v when non-empty, otherwise fallback.
func defaultString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// defaultBaseURL is identical to defaultString but documents intent at
// the call site.
func defaultBaseURL(v, fallback string) string {
	return defaultString(v, fallback)
}
