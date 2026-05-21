package app

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
	"github.com/syntheticinc/syntheticbrew/pkg/errors"
	pluginpkg "github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// createChatModel creates a ToolCallingChatModel based on provider config.
// Returns nil, nil when no provider is configured (configless Docker mode).
func createChatModel(cfg config.Config) (model.ToolCallingChatModel, error) {
	ctx := context.Background()

	switch cfg.LLM.DefaultProvider {
	case "":
		// No LLM configured — Engine starts without a default model.
		// Models are configured later through Admin Dashboard or YAML import.
		slog.InfoContext(ctx, "No default LLM provider configured — configure models via Admin Dashboard")
		return nil, nil
	case "openrouter":
		return createOpenRouterModel(ctx, cfg.LLM.OpenRouter)
	case "ollama":
		return createOllamaModel(ctx, cfg.LLM.Ollama)
	case "anthropic":
		return createAnthropicModel(ctx, cfg.LLM.Anthropic)
	default:
		return nil, errors.New(errors.CodeInvalidInput, "unsupported LLM provider: "+cfg.LLM.DefaultProvider)
	}
}

func createOpenRouterModel(ctx context.Context, cfg config.OpenRouterConfig) (model.ToolCallingChatModel, error) {
	orCfg := &openai.ChatModelConfig{
		BaseURL: cfg.BaseURL,
		Model:   cfg.Model,
		APIKey:  cfg.APIKey,
	}
	if len(cfg.Provider) > 0 {
		orCfg.ExtraFields = map[string]any{
			"provider": cfg.Provider,
		}
	}
	chatModel, err := openai.NewChatModel(ctx, orCfg)
	if err != nil {
		return nil, errors.Wrap(err, errors.CodeInternal, "failed to create openrouter chat model")
	}
	return chatModel, nil
}

func createOllamaModel(ctx context.Context, cfg config.OllamaConfig) (model.ToolCallingChatModel, error) {
	baseURL := cfg.BaseURL

	// Use OpenAI-compatible adapter for tool calling support.
	// Ollama's native Eino adapter doesn't properly support tool calling,
	// but Ollama exposes an OpenAI-compatible endpoint at /v1.
	// Auto-convert /api URLs to /v1 for compatibility.
	if strings.HasSuffix(baseURL, "/api") {
		baseURL = strings.TrimSuffix(baseURL, "/api") + "/v1"
		slog.InfoContext(ctx, "Converting Ollama native API to OpenAI-compatible endpoint",
			"original", cfg.BaseURL, "converted", baseURL)
	}
	if !strings.Contains(baseURL, "/v1") {
		baseURL = strings.TrimRight(baseURL, "/") + "/v1"
	}

	openaiCfg := &openai.ChatModelConfig{
		BaseURL: baseURL,
		Model:   cfg.Model,
		APIKey:  "ollama", // Ollama ignores API key but field is required
	}

	chatModel, err := openai.NewChatModel(ctx, openaiCfg)
	if err != nil {
		return nil, errors.Wrap(err, errors.CodeInternal, "failed to create ollama chat model (via OpenAI-compatible endpoint)")
	}
	slog.InfoContext(ctx, "Ollama model created via OpenAI-compatible endpoint",
		"base_url", baseURL, "model", cfg.Model)
	return chatModel, nil
}

func createAnthropicModel(ctx context.Context, cfg config.AnthropicConfig) (model.ToolCallingChatModel, error) {
	baseURL := "https://api.anthropic.com/v1"
	if cfg.BaseURL != "" {
		baseURL = cfg.BaseURL
	}

	httpClient := &http.Client{Timeout: cfg.Timeout}
	httpClient.Transport = &anthropicTransport{
		base: http.DefaultTransport,
	}

	anthropicCfg := &openai.ChatModelConfig{
		BaseURL:    baseURL,
		Model:      cfg.Model,
		APIKey:     cfg.APIKey,
		HTTPClient: httpClient,
	}
	chatModel, err := openai.NewChatModel(ctx, anthropicCfg)
	if err != nil {
		return nil, errors.Wrap(err, errors.CodeInternal, "failed to create anthropic model")
	}
	return chatModel, nil
}

// anthropicTransport adds the required anthropic-version header to all requests.
type anthropicTransport struct {
	base http.RoundTripper
}

func (t *anthropicTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("anthropic-version", "2023-06-01")
	return t.base.RoundTrip(req)
}

// wrapWithDebugModel wraps the chat model with a request/response logger when
// debugDir is non-empty. The directory comes from the bootstrap config
// (Debug.ModelDebugDir, env var SYNTHETICBREW_DEBUG_MODEL) — see pkg/config.
func wrapWithDebugModel(chatModel model.ToolCallingChatModel, debugDir string) model.ToolCallingChatModel {
	if debugDir == "" {
		return chatModel
	}
	slog.InfoContext(context.Background(), "debug model wrapper enabled", "log_dir", debugDir)
	return llm.NewDebugChatModelWrapper(chatModel, debugDir, "global")
}

// createModelSelector creates a ModelSelector and lets the plugin register per-agent models.
func createModelSelector(plug pluginpkg.Plugin, chatModel model.ToolCallingChatModel, modelName string) *llm.ModelSelector {
	selector := llm.NewModelSelector(chatModel, modelName)
	plug.PrepareModelSelector(selector, chatModel)
	return selector
}

// getModelName returns model name based on LLM provider config.
func getModelName(cfg config.Config) string {
	switch cfg.LLM.DefaultProvider {
	case "openrouter":
		return cfg.LLM.OpenRouter.Model
	case "ollama":
		return cfg.LLM.Ollama.Model
	case "anthropic":
		return cfg.LLM.Anthropic.Model
	default:
		return cfg.LLM.Ollama.Model
	}
}
