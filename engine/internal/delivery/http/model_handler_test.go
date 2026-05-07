package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockModelService struct {
	listFunc   func(ctx context.Context) ([]ModelResponse, error)
	createFunc func(ctx context.Context, req CreateModelRequest) (*ModelResponse, error)
	updateFunc func(ctx context.Context, name string, req CreateModelRequest) (*ModelResponse, error)
	patchFunc  func(ctx context.Context, name string, req UpdateModelRequest) (*ModelResponse, error)
	deleteFunc func(ctx context.Context, name string) error
	verifyFunc func(ctx context.Context, name string) (*ModelVerifyResult, error)
}

func (m *mockModelService) ListModels(ctx context.Context) ([]ModelResponse, error) {
	if m.listFunc != nil {
		return m.listFunc(ctx)
	}
	return nil, nil
}

func (m *mockModelService) CreateModel(ctx context.Context, req CreateModelRequest) (*ModelResponse, error) {
	if m.createFunc != nil {
		return m.createFunc(ctx, req)
	}
	return nil, nil
}

func (m *mockModelService) UpdateModel(ctx context.Context, name string, req CreateModelRequest) (*ModelResponse, error) {
	if m.updateFunc != nil {
		return m.updateFunc(ctx, name, req)
	}
	return nil, nil
}

func (m *mockModelService) DeleteModel(ctx context.Context, name string) error {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, name)
	}
	return nil
}

func (m *mockModelService) VerifyModel(ctx context.Context, name string) (*ModelVerifyResult, error) {
	if m.verifyFunc != nil {
		return m.verifyFunc(ctx, name)
	}
	return nil, nil
}

func (m *mockModelService) PatchModel(ctx context.Context, name string, req UpdateModelRequest) (*ModelResponse, error) {
	if m.patchFunc != nil {
		return m.patchFunc(ctx, name, req)
	}
	return &ModelResponse{Name: name}, nil
}

func TestModelHandler_Verify(t *testing.T) {
	tests := []struct {
		name           string
		modelName      string
		verifyFunc     func(ctx context.Context, name string) (*ModelVerifyResult, error)
		wantStatus     int
		wantResult     *ModelVerifyResult
		wantErrMessage string
	}{
		{
			name:      "successful verification with known provider",
			modelName: "gpt-4",
			verifyFunc: func(ctx context.Context, name string) (*ModelVerifyResult, error) {
				return &ModelVerifyResult{
					Connectivity:   "ok",
					ToolCalling:    "skipped",
					ResponseTimeMs: 150,
					ModelName:      "gpt-4",
					Provider:       "openai",
				}, nil
			},
			wantStatus: http.StatusOK,
			wantResult: &ModelVerifyResult{
				Connectivity:   "ok",
				ToolCalling:    "skipped",
				ResponseTimeMs: 150,
				ModelName:      "gpt-4",
				Provider:       "openai",
			},
		},
		{
			name:      "connectivity error",
			modelName: "bad-model",
			verifyFunc: func(ctx context.Context, name string) (*ModelVerifyResult, error) {
				errMsg := "connectivity check failed: connection refused"
				return &ModelVerifyResult{
					Connectivity: "error",
					ToolCalling:  "skipped",
					ModelName:    "bad-model",
					Provider:     "ollama",
					Error:        &errMsg,
				}, nil
			},
			wantStatus: http.StatusOK,
		},
		{
			name:      "model not found",
			modelName: "nonexistent",
			verifyFunc: func(ctx context.Context, name string) (*ModelVerifyResult, error) {
				return nil, fmt.Errorf("model not found: nonexistent")
			},
			wantStatus:     http.StatusInternalServerError,
			wantErrMessage: "model not found: nonexistent",
		},
		{
			name:      "tool calling supported",
			modelName: "llama3",
			verifyFunc: func(ctx context.Context, name string) (*ModelVerifyResult, error) {
				return &ModelVerifyResult{
					Connectivity:   "ok",
					ToolCalling:    "supported",
					ResponseTimeMs: 1200,
					ModelName:      "llama3",
					Provider:       "ollama",
				}, nil
			},
			wantStatus: http.StatusOK,
			wantResult: &ModelVerifyResult{
				Connectivity:   "ok",
				ToolCalling:    "supported",
				ResponseTimeMs: 1200,
				ModelName:      "llama3",
				Provider:       "ollama",
			},
		},
		{
			name:      "tool calling not detected",
			modelName: "phi3",
			verifyFunc: func(ctx context.Context, name string) (*ModelVerifyResult, error) {
				return &ModelVerifyResult{
					Connectivity:   "ok",
					ToolCalling:    "not_detected",
					ResponseTimeMs: 800,
					ModelName:      "phi3",
					Provider:       "ollama",
				}, nil
			},
			wantStatus: http.StatusOK,
			wantResult: &ModelVerifyResult{
				Connectivity:   "ok",
				ToolCalling:    "not_detected",
				ResponseTimeMs: 800,
				ModelName:      "phi3",
				Provider:       "ollama",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &mockModelService{verifyFunc: tt.verifyFunc}
			handler := NewModelHandler(svc)

			// Use chi router to inject URL params.
			r := chi.NewRouter()
			r.Mount("/api/v1/models", handler.Routes())

			req := httptest.NewRequest(http.MethodPost, "/api/v1/models/"+tt.modelName+"/verify", nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

			if tt.wantResult != nil {
				var result ModelVerifyResult
				err := json.NewDecoder(rec.Body).Decode(&result)
				require.NoError(t, err)
				assert.Equal(t, tt.wantResult.Connectivity, result.Connectivity)
				assert.Equal(t, tt.wantResult.ToolCalling, result.ToolCalling)
				assert.Equal(t, tt.wantResult.ModelName, result.ModelName)
				assert.Equal(t, tt.wantResult.Provider, result.Provider)
			}

			if tt.wantErrMessage != "" {
				var errResp map[string]string
				err := json.NewDecoder(rec.Body).Decode(&errResp)
				require.NoError(t, err)
				assert.Contains(t, errResp["error"], tt.wantErrMessage)
			}
		})
	}
}

func TestModelHandler_Verify_ErrorField(t *testing.T) {
	errMsg := "connection refused"
	svc := &mockModelService{
		verifyFunc: func(ctx context.Context, name string) (*ModelVerifyResult, error) {
			return &ModelVerifyResult{
				Connectivity: "error",
				ToolCalling:  "skipped",
				ModelName:    "test",
				Provider:     "ollama",
				Error:        &errMsg,
			}, nil
		},
	}
	handler := NewModelHandler(svc)
	r := chi.NewRouter()
	r.Mount("/api/v1/models", handler.Routes())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/models/test/verify", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result ModelVerifyResult
	err := json.NewDecoder(rec.Body).Decode(&result)
	require.NoError(t, err)
	assert.Equal(t, "error", result.Connectivity)
	require.NotNil(t, result.Error)
	assert.Equal(t, "connection refused", *result.Error)
}

func TestModelHandler_List_KindFilter(t *testing.T) {
	chatModel := ModelResponse{ID: "1", Name: "gpt-4", Type: "openai_compatible", Kind: "chat", ModelName: "gpt-4", CreatedAt: "2026-01-01T00:00:00Z"}
	embModel := ModelResponse{ID: "2", Name: "text-emb", Type: "openai_compatible", Kind: "embedding", ModelName: "text-embedding-3-small", EmbeddingDim: 1536, CreatedAt: "2026-01-01T00:00:00Z"}

	svc := &mockModelService{
		listFunc: func(ctx context.Context) ([]ModelResponse, error) {
			return []ModelResponse{chatModel, embModel}, nil
		},
	}
	handler := NewModelHandler(svc)
	r := chi.NewRouter()
	r.Mount("/api/v1/models", handler.Routes())

	t.Run("kind=embedding returns only embedding models", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/models/?kind=embedding", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		var result []ModelResponse
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&result))
		require.Len(t, result, 1)
		assert.Equal(t, "embedding", result[0].Kind)
		assert.Equal(t, "text-emb", result[0].Name)
	})

	t.Run("kind=chat returns only chat models", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/models/?kind=chat", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		var result []ModelResponse
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&result))
		require.Len(t, result, 1)
		assert.Equal(t, "chat", result[0].Kind)
		assert.Equal(t, "gpt-4", result[0].Name)
	})

	t.Run("no filter returns all models", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/models/", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		var result []ModelResponse
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&result))
		assert.Len(t, result, 2)
	})

	t.Run("invalid kind returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/models/?kind=unknown", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestModelHandler_Create_KindValidation(t *testing.T) {
	svc := &mockModelService{
		createFunc: func(ctx context.Context, req CreateModelRequest) (*ModelResponse, error) {
			return &ModelResponse{ID: "1", Name: req.Name, Type: req.Type, Kind: req.Kind, ModelName: req.ModelName, CreatedAt: "2026-01-01T00:00:00Z"}, nil
		},
	}
	handler := NewModelHandler(svc)
	r := chi.NewRouter()
	r.Mount("/api/v1/models", handler.Routes())

	t.Run("kind absent returns 400", func(t *testing.T) {
		body, _ := json.Marshal(CreateModelRequest{Name: "m", Type: "ollama", ModelName: "llama3"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/models/", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		var errResp map[string]string
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&errResp))
		assert.Contains(t, errResp["error"], "kind is required")
	})

	t.Run("kind=embedding is accepted", func(t *testing.T) {
		var capturedReq CreateModelRequest
		svc.createFunc = func(ctx context.Context, req CreateModelRequest) (*ModelResponse, error) {
			capturedReq = req
			return &ModelResponse{ID: "2", Name: req.Name, Type: req.Type, Kind: req.Kind, ModelName: req.ModelName, CreatedAt: "2026-01-01T00:00:00Z"}, nil
		}
		body, _ := json.Marshal(CreateModelRequest{
			Name: "emb", Type: "openai_compatible", ModelName: "text-embedding-3-small",
			Kind: "embedding", BaseURL: "https://api.openai.com/v1", APIKey: "sk-test", EmbeddingDim: 1536,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/models/", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusCreated, rec.Code)
		assert.Equal(t, "embedding", capturedReq.Kind)
	})

	t.Run("invalid kind returns 400", func(t *testing.T) {
		body, _ := json.Marshal(CreateModelRequest{Name: "m", Type: "ollama", ModelName: "llama3", Kind: "reranker"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/models/", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		var errResp map[string]string
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&errResp))
		assert.Contains(t, errResp["error"], "kind must be one of")
	})
}

func TestModelHandler_Create_OpenRouterPreset(t *testing.T) {
	tests := []struct {
		name           string
		req            CreateModelRequest
		wantStatus     int
		wantType       string
		wantBaseURL    string
		wantErrMessage string
	}{
		{
			name: "normalizes openrouter to openai_compatible with default base URL",
			req: CreateModelRequest{
				Name:      "my-openrouter",
				Type:      "openrouter",
				ModelName: "anthropic/claude-sonnet-4-6",
				APIKey:    "sk-or-test-key",
				Kind:      "chat",
			},
			wantStatus:  http.StatusCreated,
			wantType:    "openai_compatible",
			wantBaseURL: "https://openrouter.ai/api/v1",
		},
		{
			name: "preserves custom base URL for openrouter",
			req: CreateModelRequest{
				Name:      "my-openrouter-custom",
				Type:      "openrouter",
				ModelName: "anthropic/claude-sonnet-4-6",
				APIKey:    "sk-or-test-key",
				BaseURL:   "https://custom.openrouter.ai/api/v1",
				Kind:      "chat",
			},
			wantStatus:  http.StatusCreated,
			wantType:    "openai_compatible",
			wantBaseURL: "https://custom.openrouter.ai/api/v1",
		},
		{
			name: "rejects openrouter without api_key",
			req: CreateModelRequest{
				Name:      "no-key",
				Type:      "openrouter",
				ModelName: "anthropic/claude-sonnet-4-6",
				Kind:      "chat",
			},
			wantStatus:     http.StatusBadRequest,
			wantErrMessage: "api_key is required for openrouter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedReq CreateModelRequest
			svc := &mockModelService{
				createFunc: func(ctx context.Context, req CreateModelRequest) (*ModelResponse, error) {
					capturedReq = req
					return &ModelResponse{
						ID:        "1",
						Name:      req.Name,
						Type:      req.Type,
						BaseURL:   req.BaseURL,
						ModelName: req.ModelName,
						HasAPIKey: req.APIKey != "",
						CreatedAt: "2026-01-01T00:00:00Z",
					}, nil
				},
			}
			handler := NewModelHandler(svc)

			body, _ := json.Marshal(tt.req)
			r := chi.NewRouter()
			r.Mount("/api/v1/models", handler.Routes())

			req := httptest.NewRequest(http.MethodPost, "/api/v1/models/", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)

			if tt.wantErrMessage != "" {
				var errResp map[string]string
				err := json.NewDecoder(rec.Body).Decode(&errResp)
				require.NoError(t, err)
				assert.Contains(t, errResp["error"], tt.wantErrMessage)
				return
			}

			assert.Equal(t, tt.wantType, capturedReq.Type)
			assert.Equal(t, tt.wantBaseURL, capturedReq.BaseURL)
		})
	}
}

// TestModelHandler_Patch_NormalizesAlias guards against the regression caught
// by production canary: brewctl reconcile after engine canonicalized
// `type: openrouter` → `openai_compatible` on Create. Bundle reapply then
// PATCHed with `type: openrouter` (the desired form), but Patch handler did
// not re-run the same alias normalization Create does. Result: type=openrouter
// reached DB → chk_models_type constraint rejected (enum has no openrouter)
// → API 500 → brewctl exits non-zero → Job BackoffLimitExceeded → Helm
// upgrade fails. PATCH must mirror Create's normalization so reconcile is
// idempotent.
func TestModelHandler_Patch_NormalizesAlias(t *testing.T) {
	tests := []struct {
		name            string
		path            string
		body            string
		wantStatus      int
		wantPatchedType string
	}{
		{
			name:            "openrouter alias normalized to openai_compatible",
			path:            "/my-router",
			body:            `{"type":"openrouter","model_name":"openai/gpt-4o-mini"}`,
			wantStatus:      http.StatusOK,
			wantPatchedType: "openai_compatible",
		},
		{
			name:            "non-alias type passes through",
			path:            "/my-anthropic",
			body:            `{"type":"anthropic"}`,
			wantStatus:      http.StatusOK,
			wantPatchedType: "anthropic",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured UpdateModelRequest
			svc := &mockModelService{
				patchFunc: func(ctx context.Context, name string, req UpdateModelRequest) (*ModelResponse, error) {
					captured = req
					tp := ""
					if req.Type != nil {
						tp = *req.Type
					}
					return &ModelResponse{Name: name, Type: tp}, nil
				},
			}
			h := NewModelHandler(svc)
			r := chi.NewRouter()
			r.Mount("/api/v1/models", h.Routes())

			req := httptest.NewRequest(http.MethodPatch, "/api/v1/models"+tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			require.Equal(t, tt.wantStatus, w.Code, "body: %s", w.Body.String())
			require.NotNil(t, captured.Type, "service must receive a non-nil Type pointer")
			assert.Equal(t, tt.wantPatchedType, *captured.Type)
		})
	}
}
