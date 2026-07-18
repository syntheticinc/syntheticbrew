package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// staticBYOKResolver is a settable test resolver. Tests flip cfg to simulate a
// per-tenant config change (the production resolver reads it from settings).
type staticBYOKResolver struct{ cfg BYOKConfig }

func (s *staticBYOKResolver) Resolve(context.Context) BYOKConfig { return s.cfg }

func newTestBYOKMiddleware(cfg BYOKConfig) *BYOKMiddleware {
	return NewBYOKMiddleware(&staticBYOKResolver{cfg: cfg})
}

func TestBYOKMiddleware_NoHeaders(t *testing.T) {
	mw := newTestBYOKMiddleware(BYOKConfig{Enabled: true, AllowedProviders: []string{"openai"}})

	var called bool
	handler := mw.InjectBYOK(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		// No BYOK context should be set
		provider, _ := r.Context().Value(ContextKeyBYOKProvider).(string)
		assert.Empty(t, provider)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestBYOKMiddleware_Disabled(t *testing.T) {
	mw := newTestBYOKMiddleware(BYOKConfig{Enabled: false})

	handler := mw.InjectBYOK(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-BYOK-Provider", "openai")
	req.Header.Set("X-BYOK-API-Key", "sk-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "BYOK is disabled")
}

func TestBYOKMiddleware_ValidHeaders(t *testing.T) {
	// openai_compatible legitimately accepts a (public) base URL; a pinned
	// provider with a base URL is rejected (see RejectsBaseURLOverrideForPinned).
	mw := newTestBYOKMiddleware(BYOKConfig{
		Enabled:          true,
		AllowedProviders: []string{"openai_compatible", "anthropic"},
	})

	var capturedProvider, capturedKey, capturedModel, capturedBaseURL string
	handler := mw.InjectBYOK(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedProvider, _ = r.Context().Value(ContextKeyBYOKProvider).(string)
		capturedKey, _ = r.Context().Value(ContextKeyBYOKAPIKey).(string)
		capturedModel, _ = r.Context().Value(ContextKeyBYOKModel).(string)
		capturedBaseURL, _ = r.Context().Value(ContextKeyBYOKBaseURL).(string)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-BYOK-Provider", "OpenAI_Compatible")
	req.Header.Set("X-BYOK-API-Key", "sk-test-key")
	req.Header.Set("X-BYOK-Model", "gpt-4o")
	req.Header.Set("X-BYOK-Base-URL", "https://example.com/v1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "openai_compatible", capturedProvider) // lowercased
	assert.Equal(t, "sk-test-key", capturedKey)
	assert.Equal(t, "gpt-4o", capturedModel)
	assert.Equal(t, "https://example.com/v1", capturedBaseURL)
}

func TestBYOKMiddleware_ProviderNotAllowed(t *testing.T) {
	mw := newTestBYOKMiddleware(BYOKConfig{
		Enabled:          true,
		AllowedProviders: []string{"openai"},
	})

	handler := mw.InjectBYOK(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-BYOK-Provider", "anthropic")
	req.Header.Set("X-BYOK-API-Key", "sk-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "provider not allowed")
}

func TestBYOKMiddleware_MissingAPIKey(t *testing.T) {
	mw := newTestBYOKMiddleware(BYOKConfig{
		Enabled:          true,
		AllowedProviders: []string{"openai"},
	})

	handler := mw.InjectBYOK(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-BYOK-Provider", "openai")
	// No X-BYOK-API-Key
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "X-BYOK-Provider and X-BYOK-API-Key are required")
}

func TestBYOKMiddleware_NoModelName(t *testing.T) {
	mw := newTestBYOKMiddleware(BYOKConfig{
		Enabled:          true,
		AllowedProviders: []string{"openai"},
	})

	var capturedModel string
	handler := mw.InjectBYOK(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedModel, _ = r.Context().Value(ContextKeyBYOKModel).(string)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-BYOK-Provider", "openai")
	req.Header.Set("X-BYOK-API-Key", "sk-123")
	// No X-BYOK-Model
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, capturedModel)
}

func TestBYOKMiddleware_AllProvidersAllowed(t *testing.T) {
	// Empty AllowedProviders = all providers allowed (except custom-base_url — F1).
	mw := newTestBYOKMiddleware(BYOKConfig{
		Enabled:          true,
		AllowedProviders: nil,
	})

	var capturedProvider string
	handler := mw.InjectBYOK(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedProvider, _ = r.Context().Value(ContextKeyBYOKProvider).(string)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-BYOK-Provider", "openai")
	req.Header.Set("X-BYOK-API-Key", "sk-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "openai", capturedProvider)
}

// TestBYOKMiddleware_EmptyAllowlistExcludesCustomBaseURL is F1: an empty
// allow-all list must NOT silently permit the custom-base_url providers
// (openai_compatible, ollama) — a zero-config tenant would otherwise let an
// end-user route the engine at its own localhost. They require explicit opt-in.
func TestBYOKMiddleware_EmptyAllowlistExcludesCustomBaseURL(t *testing.T) {
	for _, provider := range []string{"openai_compatible", "ollama"} {
		t.Run(provider+"_rejected_when_empty", func(t *testing.T) {
			mw := newTestBYOKMiddleware(BYOKConfig{Enabled: true, AllowedProviders: nil})
			handler := mw.InjectBYOK(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("X-BYOK-Provider", provider)
			req.Header.Set("X-BYOK-API-Key", "sk-1")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusForbidden, rec.Code)
			assert.Contains(t, rec.Body.String(), "provider not allowed")
		})
		t.Run(provider+"_allowed_when_listed", func(t *testing.T) {
			mw := newTestBYOKMiddleware(BYOKConfig{Enabled: true, AllowedProviders: []string{provider}})
			var got string
			handler := mw.InjectBYOK(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, _ = r.Context().Value(ContextKeyBYOKProvider).(string)
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("X-BYOK-Provider", provider)
			req.Header.Set("X-BYOK-API-Key", "sk-1")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, provider, got)
		})
	}
}

// TestBYOKMiddleware_RejectsBaseURLOverrideForPinned is F3 at the boundary: a
// base URL for a pinned hosted provider is a clean 400 (not a silent downgrade
// to the tenant model deep in execution).
func TestBYOKMiddleware_RejectsBaseURLOverrideForPinned(t *testing.T) {
	for _, provider := range []string{"openai", "openrouter", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			mw := newTestBYOKMiddleware(BYOKConfig{Enabled: true})
			handler := mw.InjectBYOK(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("X-BYOK-Provider", provider)
			req.Header.Set("X-BYOK-API-Key", "sk-1")
			req.Header.Set("X-BYOK-Base-URL", "http://169.254.169.254/v1")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Body.String(), "base_url override is not permitted")
		})
	}
}

// TestBYOKMiddleware_RejectsPrivateBaseURL is the SSRF boundary check: a
// private / metadata base URL for a custom-base_url provider is a clean 400 at
// the URL layer (the dial-time Control check remains for DNS-rebinding).
func TestBYOKMiddleware_RejectsPrivateBaseURL(t *testing.T) {
	for _, target := range []string{"http://169.254.169.254/v1", "http://127.0.0.1/v1", "http://10.0.0.1/v1", "ftp://x/v1"} {
		t.Run(target, func(t *testing.T) {
			mw := newTestBYOKMiddleware(BYOKConfig{Enabled: true, AllowedProviders: []string{"openai_compatible"}})
			handler := mw.InjectBYOK(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("X-BYOK-Provider", "openai_compatible")
			req.Header.Set("X-BYOK-API-Key", "sk-1")
			req.Header.Set("X-BYOK-Base-URL", target)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Body.String(), "base URL not permitted")
		})
	}
}

// TestBYOKMiddleware_AllowsPublicBaseURL confirms a public base URL for a
// custom-base_url provider passes through with the header in context.
func TestBYOKMiddleware_AllowsPublicBaseURL(t *testing.T) {
	mw := newTestBYOKMiddleware(BYOKConfig{Enabled: true, AllowedProviders: []string{"openai_compatible"}})
	var got string
	handler := mw.InjectBYOK(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = r.Context().Value(ContextKeyBYOKBaseURL).(string)
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-BYOK-Provider", "openai_compatible")
	req.Header.Set("X-BYOK-API-Key", "sk-1")
	req.Header.Set("X-BYOK-Base-URL", "https://vllm.example.com/v1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "https://vllm.example.com/v1", got)
}

// TestBYOKMiddleware_ResolverConfigChange verifies the middleware reads the
// live per-tenant config on every request: flipping the resolver's config
// between enabled/disabled/allowlist hits the new branch immediately.
func TestBYOKMiddleware_ResolverConfigChange(t *testing.T) {
	resolver := &staticBYOKResolver{cfg: BYOKConfig{Enabled: true, AllowedProviders: []string{"openai"}}}
	mw := NewBYOKMiddleware(resolver)

	handler := mw.InjectBYOK(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	makeReq := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-BYOK-Provider", "openai")
		req.Header.Set("X-BYOK-API-Key", "sk-1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	assert.Equal(t, http.StatusOK, makeReq().Code)

	resolver.cfg = BYOKConfig{Enabled: false}
	rec := makeReq()
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "BYOK is disabled")

	resolver.cfg = BYOKConfig{Enabled: true, AllowedProviders: []string{"openai"}}
	assert.Equal(t, http.StatusOK, makeReq().Code)

	resolver.cfg = BYOKConfig{Enabled: true, AllowedProviders: []string{"anthropic"}}
	rec = makeReq()
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "provider not allowed")
}
