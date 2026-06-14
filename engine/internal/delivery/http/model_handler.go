package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// CacheControlPayload is the API representation of a model's prompt-cache config.
// Default off (absent or enabled=false) → request shape unchanged. Honored only
// by explicit-cache adapters (openai_compatible, anthropic); automatic-cache
// providers ignore it.
type CacheControlPayload struct {
	Enabled         bool     `json:"enabled"`
	Breakpoints     []string `json:"breakpoints,omitempty"`       // subset of: system, tools, history
	MinPrefixTokens int      `json:"min_prefix_tokens,omitempty"` // skip caching prefixes below this size
}

// ModelResponse is the API representation of an LLM provider model.
type ModelResponse struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	// Kind is "chat" or "embedding". Must be one of: chat, embedding.
	Kind         string `json:"kind"`
	BaseURL      string `json:"base_url,omitempty"`
	ModelName    string `json:"model_name"`
	HasAPIKey    bool   `json:"has_api_key"`
	APIVersion   string `json:"api_version,omitempty"`
	EmbeddingDim int    `json:"embedding_dim,omitempty"` // >0 for embedding models
	// IsDefault flags the tenant's current default chat model (at most one
	// per tenant, enforced by a partial unique DB index).
	IsDefault bool `json:"is_default"`
	// ExtraBody is merged into every LLM request body for openai_compatible
	// providers. Lets operators pass upstream-specific fields (e.g. OpenRouter
	// provider routing) without engine code changes.
	ExtraBody map[string]any `json:"extra_body,omitempty"`
	// CacheControl is the model's prompt-cache config (absent = off).
	CacheControl *CacheControlPayload `json:"cache_control,omitempty"`
	CreatedAt    string               `json:"created_at"`
}

// CreateModelRequest is the body for POST /api/v1/models.
type CreateModelRequest struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	// Kind is "chat" or "embedding". Must be one of: chat, embedding.
	Kind         string `json:"kind,omitempty"`
	BaseURL      string `json:"base_url,omitempty"`
	ModelName    string `json:"model_name"`
	APIKey       string `json:"api_key,omitempty"`
	APIVersion   string `json:"api_version,omitempty"`
	EmbeddingDim int `json:"embedding_dim,omitempty"` // required when kind=embedding
	// IsDefault, when true on a chat model, promotes it to tenant default
	// (atomic swap). When not set on the first chat model created for a
	// tenant, the server auto-promotes it (natural bootstrap).
	IsDefault bool           `json:"is_default,omitempty"`
	ExtraBody map[string]any `json:"extra_body,omitempty"`
	// CacheControl configures prompt-cache breakpoints (absent = off).
	CacheControl *CacheControlPayload `json:"cache_control,omitempty"`
}

// ModelVerifyResult contains the result of model connectivity verification.
type ModelVerifyResult struct {
	Connectivity   string  `json:"connectivity"`
	ToolCalling    string  `json:"tool_calling"`
	ResponseTimeMs int64   `json:"response_time_ms"`
	ModelName      string  `json:"model_name"`
	Provider       string  `json:"provider"`
	Error          *string `json:"error"`
}

// UpdateModelRequest is the body for PATCH /api/v1/models/{name}.
// All fields are pointers: nil means "preserve existing value".
type UpdateModelRequest struct {
	Name         *string `json:"name,omitempty"`
	Type         *string `json:"type,omitempty"`
	// Kind is "chat" or "embedding". Nil preserves existing value.
	Kind         *string `json:"kind,omitempty"`
	BaseURL      *string `json:"base_url,omitempty"`
	ModelName    *string `json:"model_name,omitempty"`
	APIKey       *string `json:"api_key,omitempty"`
	APIVersion   *string `json:"api_version,omitempty"`
	EmbeddingDim *int    `json:"embedding_dim,omitempty"`
	// IsDefault: pointer so nil = "don't touch". *true = promote this model
	// to tenant default. *false = rejected (you must promote a replacement
	// instead; you can't clear the default without picking another).
	IsDefault *bool `json:"is_default,omitempty"`
	// ExtraBody: nil preserves existing value; empty map clears it.
	ExtraBody *map[string]any `json:"extra_body,omitempty"`
	// CacheControl: nil preserves existing value; a value replaces it.
	CacheControl *CacheControlPayload `json:"cache_control,omitempty"`
}

// ModelService provides LLM model CRUD operations.
type ModelService interface {
	ListModels(ctx context.Context) ([]ModelResponse, error)
	CreateModel(ctx context.Context, req CreateModelRequest) (*ModelResponse, error)
	UpdateModel(ctx context.Context, name string, req CreateModelRequest) (*ModelResponse, error)
	PatchModel(ctx context.Context, name string, req UpdateModelRequest) (*ModelResponse, error)
	DeleteModel(ctx context.Context, name string) error
	VerifyModel(ctx context.Context, name string) (*ModelVerifyResult, error)
}

// validModelKinds is the set of accepted kind values for validation.
var validModelKinds = map[string]bool{"chat": true, "embedding": true}

// validCacheBreakpoints is the set of accepted cache_control breakpoint names.
var validCacheBreakpoints = map[string]bool{"system": true, "tools": true, "history": true}

// validateCacheControl rejects malformed cache_control payloads with a 400-grade
// message (returns "" when valid). nil is valid (= off).
func validateCacheControl(cc *CacheControlPayload) string {
	if cc == nil {
		return ""
	}
	for _, bp := range cc.Breakpoints {
		if !validCacheBreakpoints[bp] {
			return "cache_control.breakpoints entries must be one of: system, tools, history"
		}
	}
	if cc.MinPrefixTokens < 0 {
		return "cache_control.min_prefix_tokens must be >= 0"
	}
	return ""
}

// ModelHandler serves /api/v1/models endpoints.
type ModelHandler struct {
	service ModelService
}

// NewModelHandler creates a ModelHandler.
func NewModelHandler(service ModelService) *ModelHandler {
	return &ModelHandler{service: service}
}

// List handles GET /api/v1/models.
// Supports ?kind=chat (chat only), ?kind=embedding (embedding only).
// Empty ?kind returns all models. Invalid ?kind returns 400.
func (h *ModelHandler) List(w http.ResponseWriter, r *http.Request) {
	allModels, err := h.service.ListModels(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}

	kindFilter := r.URL.Query().Get("kind")
	if kindFilter == "" {
		writeJSON(w, http.StatusOK, allModels)
		return
	}

	if !validModelKinds[kindFilter] {
		writeJSONError(w, http.StatusBadRequest, "kind must be one of: chat, embedding")
		return
	}

	filtered := make([]ModelResponse, 0, len(allModels))
	for _, m := range allModels {
		if m.Kind == kindFilter {
			filtered = append(filtered, m)
		}
	}
	writeJSON(w, http.StatusOK, filtered)
}

// Create handles POST /api/v1/models.
func (h *ModelHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateModelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}
	if req.Name == "" {
		writeJSONError(w, http.StatusBadRequest, "name is required")
		return
	}
	// Models live under name-keyed URLs (`/api/v1/models/{name}`). The
	// validator enforces the same DNS-label format that schemas + KBs use,
	// so PATCH/DELETE/Verify on the model can round-trip the name through
	// the URL without depending on `%2F` / `%20` decoding behavior.
	if err := ValidateResourceName(req.Name); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid model name: "+err.Error())
		return
	}
	if req.Type == "" {
		writeJSONError(w, http.StatusBadRequest, "type is required")
		return
	}
	if req.ModelName == "" {
		writeJSONError(w, http.StatusBadRequest, "model_name is required")
		return
	}
	validTypes := map[string]bool{"ollama": true, "openai_compatible": true, "anthropic": true, "azure_openai": true, "openrouter": true}
	if !validTypes[req.Type] {
		writeJSONError(w, http.StatusBadRequest, "type must be one of: ollama, openai_compatible, anthropic, azure_openai, openrouter")
		return
	}
	if req.Kind == "" {
		writeJSONError(w, http.StatusBadRequest, "kind is required")
		return
	}
	if !validModelKinds[req.Kind] {
		writeJSONError(w, http.StatusBadRequest, "kind must be one of: chat, embedding")
		return
	}
	if msg := validateCacheControl(req.CacheControl); msg != "" {
		writeJSONError(w, http.StatusBadRequest, msg)
		return
	}

	// OpenRouter preset: normalize to openai_compatible with default base URL.
	if req.Type == "openrouter" {
		if req.APIKey == "" {
			writeJSONError(w, http.StatusBadRequest, "api_key is required for openrouter")
			return
		}
		req.Type = "openai_compatible"
		if req.BaseURL == "" {
			req.BaseURL = "https://openrouter.ai/api/v1"
		}
	}

	// Azure OpenAI: require base_url and api_key, default api_version.
	if req.Type == "azure_openai" {
		if req.BaseURL == "" {
			writeJSONError(w, http.StatusBadRequest, "base_url is required for azure_openai (e.g. https://myresource.openai.azure.com)")
			return
		}
		if req.APIKey == "" {
			writeJSONError(w, http.StatusBadRequest, "api_key is required for azure_openai")
			return
		}
		if req.APIVersion == "" {
			req.APIVersion = "2024-10-21"
		}
	}

	// Embedding model: require embedding_dim, base_url, api_key.
	// NOTE: "embedding" is NOT a DBML models.type value — callers signal an
	// embedding-capable model by setting embedding_dim (which lands in
	// config.embedding_dim jsonb). Keep the validation branch, but trigger
	// it on embedding_dim rather than the type string.
	if req.EmbeddingDim > 0 {
		if req.BaseURL == "" {
			writeJSONError(w, http.StatusBadRequest, "base_url is required for embedding models (e.g. https://api.openai.com/v1)")
			return
		}
		if req.APIKey == "" {
			writeJSONError(w, http.StatusBadRequest, "api_key is required for embedding models")
			return
		}
	}

	model, err := h.service.CreateModel(r.Context(), req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, model)
}

// Update handles PUT /api/v1/models/{name}.
// PUT is a full-replace: type and model_name are required; missing required fields return 400.
// Use PATCH for partial updates.
func (h *ModelHandler) Update(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "model name is required")
		return
	}

	var req CreateModelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}

	// PUT full-replace: required fields must be present.
	if req.Type == "" {
		writeJSONError(w, http.StatusBadRequest, "type is required for PUT (full replace); use PATCH for partial updates")
		return
	}
	if req.ModelName == "" {
		writeJSONError(w, http.StatusBadRequest, "model_name is required for PUT (full replace); use PATCH for partial updates")
		return
	}
	if req.Kind == "" {
		writeJSONError(w, http.StatusBadRequest, "kind is required for PUT (full replace); use PATCH for partial updates")
		return
	}
	if !validModelKinds[req.Kind] {
		writeJSONError(w, http.StatusBadRequest, "kind must be one of: chat, embedding")
		return
	}
	if msg := validateCacheControl(req.CacheControl); msg != "" {
		writeJSONError(w, http.StatusBadRequest, msg)
		return
	}

	// Embedding model: same validation as Create (keyed on embedding_dim, not type).
	if req.EmbeddingDim > 0 {
		if req.BaseURL == "" {
			writeJSONError(w, http.StatusBadRequest, "base_url is required for embedding models (e.g. https://api.openai.com/v1)")
			return
		}
		if req.APIKey == "" {
			writeJSONError(w, http.StatusBadRequest, "api_key is required for embedding models")
			return
		}
	}

	result, err := h.service.UpdateModel(r.Context(), name, req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// Patch handles PATCH /api/v1/models/{name}.
// Only non-nil fields are applied; all others preserve their current value.
func (h *ModelHandler) Patch(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "model name is required")
		return
	}

	var req UpdateModelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err.Error()))
		return
	}
	if req.Kind != nil && !validModelKinds[*req.Kind] {
		writeJSONError(w, http.StatusBadRequest, "kind must be one of: chat, embedding")
		return
	}
	if msg := validateCacheControl(req.CacheControl); msg != "" {
		writeJSONError(w, http.StatusBadRequest, msg)
		return
	}
	// Validate + normalize Type alias the same way Create does. Without this,
	// `type: openrouter` (a valid Create input) reaches DB unchanged on PATCH
	// and trips chk_models_type — which only enumerates the canonical set
	// {ollama, openai_compatible, anthropic, azure_openai}. brewctl reconcile
	// then fails with API 500 on the second sync.
	if req.Type != nil {
		validTypes := map[string]bool{"ollama": true, "openai_compatible": true, "anthropic": true, "azure_openai": true, "openrouter": true}
		if !validTypes[*req.Type] {
			writeJSONError(w, http.StatusBadRequest, "type must be one of: ollama, openai_compatible, anthropic, azure_openai, openrouter")
			return
		}
		if *req.Type == "openrouter" {
			canonical := "openai_compatible"
			req.Type = &canonical
			// Default base URL only when caller did not pin one.
			if req.BaseURL == nil || *req.BaseURL == "" {
				defaultURL := "https://openrouter.ai/api/v1"
				req.BaseURL = &defaultURL
			}
		}
	}
	// Invariant: at most one default chat model per tenant. Clearing a default
	// only makes sense in the context of promoting another model — we refuse
	// a bare `is_default=false` so the client can't leave the tenant without
	// a default.
	if req.IsDefault != nil && !*req.IsDefault {
		writeJSONError(w, http.StatusBadRequest, "cannot clear is_default; set another model's is_default=true instead")
		return
	}

	result, err := h.service.PatchModel(r.Context(), name, req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// Delete handles DELETE /api/v1/models/{name}.
func (h *ModelHandler) Delete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "model name is required")
		return
	}

	if err := h.service.DeleteModel(r.Context(), name); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Verify handles POST /api/v1/models/{name}/verify.
func (h *ModelHandler) Verify(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "model name is required")
		return
	}

	result, err := h.service.VerifyModel(r.Context(), name)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
