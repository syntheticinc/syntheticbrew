package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/llm"
)

// fakeChatService is a minimal ChatService that captures the context it
// was called with so the test can assert that BYOK credentials were
// propagated end-to-end.
type fakeChatService struct {
	gotCreds    *llm.BYOKCredentials
	gotSchemaID string
	gotUserSub  string
}

func (f *fakeChatService) Chat(ctx context.Context, schemaID, _, userSub, _ string) (<-chan SSEEvent, error) {
	f.gotSchemaID = schemaID
	f.gotUserSub = userSub
	if c := llm.BYOKCredentialsFrom(ctx); c != nil {
		// Copy so the test sees a stable value even if the caller mutates.
		f.gotCreds = &llm.BYOKCredentials{
			Provider: c.Provider,
			APIKey:   c.APIKey,
			Model:    c.Model,
			BaseURL:  c.BaseURL,
		}
	}
	// Return immediately closed channel — handler must not hang.
	ch := make(chan SSEEvent)
	close(ch)
	return ch, nil
}

// ResumeInterrupt is unused by the BYOK tests but required by the
// ChatService interface (HITL resume path added in engine 1.2.0).
func (f *fakeChatService) ResumeInterrupt(_ context.Context, _, _, _, _ string, _ json.RawMessage) (<-chan SSEEvent, error) {
	ch := make(chan SSEEvent)
	close(ch)
	return ch, nil
}

// fakeForwardHeaders returns no header forwarding — focus is BYOK only.
func fakeForwardHeaders(_ context.Context) []string { return nil }

// testSchemaID is the canonical UUID the test resolver returns when handed
// the test schema name. The chat service mock asserts this same value to
// confirm name → UUID resolution flowed through the handler.
const testSchemaID = "11111111-1111-1111-1111-111111111111"

// testSchemaName is the operator-facing handle the engine 1.1.0 routes carry.
// The resolver below maps it to testSchemaID; any other name is reported as
// not found so a typo'd URL surfaces as a 404 instead of leaking through.
const testSchemaName = "byok-test-schema"

// byokTestSchemaResolver returns a static SchemaNameResolver that maps the
// test schema name to testSchemaID and otherwise reports record-not-found.
// Closure variant of fakeSchemaNameResolver from name_resolve_test.go.
func byokTestSchemaResolver() SchemaNameResolver {
	return &fakeSchemaNameResolver{
		fn: func(_ context.Context, name string) (string, error) {
			if name == testSchemaName {
				return testSchemaID, nil
			}
			return "", gorm.ErrRecordNotFound
		},
	}
}

// chatBody returns a JSON chat request with user_sub fallback so the
// handler's userSub gate does not 401 in these BYOK-focused tests.
func chatBody(extra string) *strings.Reader {
	body := `{"message":"hi","user_sub":"test-user","stream":false`
	if extra != "" {
		body += "," + extra
	}
	body += "}"
	return strings.NewReader(body)
}

// TestChatHandler_BYOKHeaders_ReachServiceContext asserts the V2 §5.8
// integration contract: a chat request carrying X-BYOK-* headers passes
// through BYOKMiddleware into the chat handler, which lifts the values
// into llm.BYOKCredentials so the downstream factory can build an
// ad-hoc per-end-user ChatModel.
func TestChatHandler_BYOKHeaders_ReachServiceContext(t *testing.T) {
	svc := &fakeChatService{}
	handler := NewChatHandler(svc, byokTestSchemaResolver(), fakeForwardHeaders)

	mw := NewBYOKMiddleware(BYOKConfig{
		Enabled:          true,
		AllowedProviders: []string{"openai", "anthropic", "openai_compatible"},
	})

	// Wire the route the same way server.go does — BYOK middleware
	// AFTER auth (auth is a no-op in this test) and BEFORE the handler.
	r := chi.NewRouter()
	r.Use(mw.InjectBYOK)
	r.Post("/api/v1/schemas/{name}/chat", handler.Chat)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/schemas/"+testSchemaName+"/chat", chatBody(""))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-BYOK-Provider", "openai_compatible")
	req.Header.Set("X-BYOK-API-Key", "sk-byok-secret")
	req.Header.Set("X-BYOK-Model", "gpt-4o-mini")
	req.Header.Set("X-BYOK-Base-URL", "https://example.com/v1")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, testSchemaID, svc.gotSchemaID)
	assert.Equal(t, "test-user", svc.gotUserSub)
	require.NotNil(t, svc.gotCreds, "BYOK credentials must be attached to chat service ctx")
	assert.Equal(t, "openai_compatible", svc.gotCreds.Provider)
	assert.Equal(t, "sk-byok-secret", svc.gotCreds.APIKey)
	assert.Equal(t, "gpt-4o-mini", svc.gotCreds.Model)
	assert.Equal(t, "https://example.com/v1", svc.gotCreds.BaseURL)
}

// TestChatHandler_DisallowedProvider_Returns403 covers the negative path:
// allowed_providers=["openai"], request comes with provider=anthropic →
// the middleware short-circuits before the chat handler ever runs.
func TestChatHandler_DisallowedProvider_Returns403(t *testing.T) {
	svc := &fakeChatService{}
	handler := NewChatHandler(svc, byokTestSchemaResolver(), fakeForwardHeaders)
	mw := NewBYOKMiddleware(BYOKConfig{
		Enabled:          true,
		AllowedProviders: []string{"openai"},
	})

	r := chi.NewRouter()
	r.Use(mw.InjectBYOK)
	r.Post("/api/v1/schemas/{name}/chat", handler.Chat)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/schemas/"+testSchemaName+"/chat", chatBody(""))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-BYOK-Provider", "anthropic")
	req.Header.Set("X-BYOK-API-Key", "sk-x")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "provider not allowed")
	assert.Nil(t, svc.gotCreds, "service must not be reached when middleware rejects")

	// Sanity: the body is JSON.
	var parsed map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &parsed))
	assert.Contains(t, parsed["error"], "anthropic")
}

// TestChatHandler_MissingKey_Returns400 covers the negative path: BYOK
// enabled, provider header set, but no API key — must reject with 400
// (V2 §5.8 "missing key when required").
func TestChatHandler_MissingKey_Returns400(t *testing.T) {
	svc := &fakeChatService{}
	handler := NewChatHandler(svc, byokTestSchemaResolver(), fakeForwardHeaders)
	mw := NewBYOKMiddleware(BYOKConfig{
		Enabled:          true,
		AllowedProviders: []string{"openai"},
	})

	r := chi.NewRouter()
	r.Use(mw.InjectBYOK)
	r.Post("/api/v1/schemas/{name}/chat", handler.Chat)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/schemas/"+testSchemaName+"/chat", chatBody(""))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-BYOK-Provider", "openai")
	// No X-BYOK-API-Key

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "X-BYOK-API-Key")
	assert.Nil(t, svc.gotCreds, "service must not be reached when middleware rejects")
}

// TestChatHandler_NoBYOK_TenantConfigPath asserts that requests without
// any BYOK headers fall through to the chat service with no credentials
// attached — the tenant-configured model must remain in effect.
func TestChatHandler_NoBYOK_TenantConfigPath(t *testing.T) {
	svc := &fakeChatService{}
	handler := NewChatHandler(svc, byokTestSchemaResolver(), fakeForwardHeaders)
	mw := NewBYOKMiddleware(BYOKConfig{Enabled: true, AllowedProviders: []string{"openai"}})

	r := chi.NewRouter()
	r.Use(mw.InjectBYOK)
	r.Post("/api/v1/schemas/{name}/chat", handler.Chat)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/schemas/"+testSchemaName+"/chat", chatBody(""))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, testSchemaID, svc.gotSchemaID)
	assert.Equal(t, "test-user", svc.gotUserSub)
	assert.Nil(t, svc.gotCreds, "no BYOK headers ⇒ no creds attached ⇒ tenant model used")
}
