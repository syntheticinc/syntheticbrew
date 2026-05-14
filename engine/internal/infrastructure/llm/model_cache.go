package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"gorm.io/gorm"

	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/persistence/models"
)

// ModelCache provides thread-safe caching of LLM clients resolved from the database.
// Clients are created lazily on first access and cached until explicitly invalidated.
type ModelCache struct {
	mu      sync.RWMutex
	clients map[string]*cachedModel
	db      *gorm.DB
}

// ResolvedModel is the cached view of a model record — client plus the
// routing metadata downstream callers use for provider-specific behaviour.
type ResolvedModel struct {
	Client       model.ToolCallingChatModel
	Name         string
	ProviderType string
	BaseURL      string
}

type cachedModel struct {
	resolved  *ResolvedModel
	createdAt time.Time
}

// NewModelCache creates a new ModelCache backed by the given database.
func NewModelCache(db *gorm.DB) *ModelCache {
	return &ModelCache{
		clients: make(map[string]*cachedModel),
		db:      db,
	}
}

// Get returns a cached model client or creates one. Back-compat shim —
// new callers should use Resolve.
func (c *ModelCache) Get(ctx context.Context, modelID string) (model.ToolCallingChatModel, string, error) {
	resolved, err := c.Resolve(ctx, modelID)
	if err != nil {
		return nil, "", err
	}
	return resolved.Client, resolved.Name, nil
}

// Resolve returns the cached client + routing metadata for a model.
func (c *ModelCache) Resolve(ctx context.Context, modelID string) (*ResolvedModel, error) {
	c.mu.RLock()
	if cached, ok := c.clients[modelID]; ok {
		c.mu.RUnlock()
		return cached.resolved, nil
	}
	c.mu.RUnlock()

	var dbModel models.LLMProviderModel
	if err := c.db.WithContext(ctx).First(&dbModel, "id = ?", modelID).Error; err != nil {
		return nil, fmt.Errorf("model ID %s not found: %w", modelID, err)
	}

	client, err := CreateClientFromDBModel(dbModel)
	if err != nil {
		return nil, fmt.Errorf("create client for model %q: %w", dbModel.Name, err)
	}

	resolved := &ResolvedModel{
		Client:       client,
		Name:         dbModel.ModelName,
		ProviderType: dbModel.Type,
		BaseURL:      dbModel.BaseURL,
	}

	c.mu.Lock()
	c.clients[modelID] = &cachedModel{resolved: resolved, createdAt: time.Now()}
	c.mu.Unlock()

	slog.InfoContext(ctx, "model client created and cached",
		"model_id", modelID, "name", dbModel.Name, "model", dbModel.ModelName, "type", dbModel.Type)

	return resolved, nil
}

// Invalidate removes a cached model client, forcing re-creation on next access.
func (c *ModelCache) Invalidate(modelID string) {
	c.mu.Lock()
	delete(c.clients, modelID)
	c.mu.Unlock()

	slog.InfoContext(context.Background(), "model cache invalidated", "model_id", modelID)
}

// InvalidateAll clears the entire cache.
func (c *ModelCache) InvalidateAll() {
	c.mu.Lock()
	c.clients = make(map[string]*cachedModel)
	c.mu.Unlock()

	slog.InfoContext(context.Background(), "model cache fully invalidated")
}

// extraBodyTransport merges operator-supplied JSON fields into every outgoing
// request body. Used to pass through upstream-specific options (e.g.
// OpenRouter provider routing) that Eino's ChatModelConfig doesn't model.
//
// Only JSON POST bodies are touched. Engine-set top-level keys (messages,
// tools, stream, model) take precedence — operator extras cannot overwrite
// them, so the engine's wire contract stays predictable.
type extraBodyTransport struct {
	base  http.RoundTripper
	extra map[string]any
}

var reservedExtraBodyKeys = map[string]struct{}{
	"messages": {},
	"tools":    {},
	"stream":   {},
	"model":    {},
}

func (t *extraBodyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if len(t.extra) == 0 || req.Body == nil || req.Method != http.MethodPost {
		return t.base.RoundTrip(req)
	}
	ct := req.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		return t.base.RoundTrip(req)
	}

	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("extra_body: read request body: %w", err)
	}
	if cerr := req.Body.Close(); cerr != nil {
		slog.WarnContext(req.Context(), "extra_body: close original body", "error", cerr)
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		// Non-JSON body — restore and forward unchanged.
		req.Body = io.NopCloser(bytes.NewReader(raw))
		req.ContentLength = int64(len(raw))
		return t.base.RoundTrip(req)
	}

	for k, v := range t.extra {
		if _, reserved := reservedExtraBodyKeys[k]; reserved {
			continue
		}
		payload[k] = v
	}

	merged, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("extra_body: marshal merged body: %w", err)
	}
	req.Body = io.NopCloser(bytes.NewReader(merged))
	req.ContentLength = int64(len(merged))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(merged)), nil
	}
	return t.base.RoundTrip(req)
}

// anthropicTransport adds the required anthropic-version header to all requests.
type anthropicCacheTransport struct {
	base http.RoundTripper
}

func (t *anthropicCacheTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("anthropic-version", "2023-06-01")
	return t.base.RoundTrip(req)
}

// CreateClientFromDBModel creates a ToolCallingChatModel from a database LLMProviderModel record.
func CreateClientFromDBModel(m models.LLMProviderModel) (model.ToolCallingChatModel, error) {
	ctx := context.Background()

	switch m.Type {
	case "ollama":
		baseURL := m.BaseURL
		if strings.HasSuffix(baseURL, "/api") {
			baseURL = strings.TrimSuffix(baseURL, "/api") + "/v1"
		}
		if !strings.Contains(baseURL, "/v1") {
			baseURL = strings.TrimRight(baseURL, "/") + "/v1"
		}
		cfg := &openai.ChatModelConfig{
			BaseURL: baseURL,
			Model:   m.ModelName,
			APIKey:  "ollama",
		}
		return openai.NewChatModel(ctx, cfg)

	case "openai", "openai_compatible":
		cfg := &openai.ChatModelConfig{
			BaseURL: m.BaseURL,
			Model:   m.ModelName,
			APIKey:  m.APIKeyEncrypted,
		}
		// Transport chain (innermost → outermost): default → properties
		// normaliser (OpenAI-strict only) → extra body → response logging.
		var transport http.RoundTripper = http.DefaultTransport
		if IsOpenAIStrictRoute(m.Type, m.ModelName, m.BaseURL) {
			transport = &propertiesNormalizingTransport{base: transport}
		}
		if extra := m.GetConfig().ExtraBody; len(extra) > 0 {
			transport = &extraBodyTransport{base: transport, extra: extra}
		}
		transport = &responseLoggingTransport{base: transport}
		cfg.HTTPClient = &http.Client{Transport: transport}
		return openai.NewChatModel(ctx, cfg)

	case "azure_openai":
		return NewAzureOpenAIChatModel(m.BaseURL, m.APIKeyEncrypted, m.ModelName, m.APIVersion)

	case "anthropic":
		baseURL := "https://api.anthropic.com/v1"
		if m.BaseURL != "" {
			baseURL = m.BaseURL
		}
		httpClient := &http.Client{}
		httpClient.Transport = &anthropicCacheTransport{
			base: http.DefaultTransport,
		}
		cfg := &openai.ChatModelConfig{
			BaseURL:    baseURL,
			Model:      m.ModelName,
			APIKey:     m.APIKeyEncrypted,
			HTTPClient: httpClient,
		}
		return openai.NewChatModel(ctx, cfg)

	case "google":
		var opts []GeminiOption
		if m.BaseURL != "" {
			opts = append(opts, WithGeminiBaseURL(m.BaseURL))
		}
		return NewGeminiChatModel(m.APIKeyEncrypted, m.ModelName, opts...), nil

	default:
		return nil, fmt.Errorf("unsupported provider type: %s", m.Type)
	}
}
