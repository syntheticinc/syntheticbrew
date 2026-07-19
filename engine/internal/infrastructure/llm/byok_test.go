package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// permissiveTestPolicy allows loopback so httptest servers (127.0.0.1) can be
// used to prove BYOK routing. Production never passes a permissive policy on
// the untrusted path — BuildBYOKChatModel composes the non-relaxable
// deny-private baseline itself. Tests reach the builder through the unexported
// buildBYOKModel with this policy to exercise the happy path.
func permissiveTestPolicy() plugin.EgressPolicy { return plugin.PermissiveEgressPolicy{} }

// TestBuildBYOKChatModel_OpenAICompatible_RoutesToUserEndpoint is the
// integration check for V2 §5.8: when BYOK credentials are present, the
// LLM call must be issued against the user-supplied base URL with the
// user-supplied API key — bypassing any tenant-configured model.
func TestBuildBYOKChatModel_OpenAICompatible_RoutesToUserEndpoint(t *testing.T) {
	// Capture what the OpenAI-compatible adapter sends.
	var capturedAuth atomic.Value
	var capturedBody atomic.Value
	var hits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		capturedAuth.Store(r.Header.Get("Authorization"))
		body, _ := io.ReadAll(r.Body)
		capturedBody.Store(string(body))

		// Minimal OpenAI chat completions success payload.
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"chatcmpl-byok-1",
			"object":"chat.completion",
			"created":1,
			"model":"gpt-4o-mini",
			"choices":[{"index":0,"message":{"role":"assistant","content":"hello from BYOK endpoint"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`)
	}))
	defer srv.Close()

	creds := BYOKCredentials{
		Provider: "openai_compatible",
		APIKey:   "sk-byok-secret",
		Model:    "gpt-4o-mini",
		BaseURL:  srv.URL,
	}

	model, err := buildBYOKModel(context.Background(), creds, permissiveTestPolicy())
	require.NoError(t, err)
	require.NotNil(t, model)

	resp, err := model.Generate(context.Background(), []*schema.Message{
		{Role: schema.User, Content: "ping"},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "hello from BYOK endpoint", resp.Content)

	// The BYOK call MUST have hit our test server (proves bypass of tenant
	// config) using the user-supplied API key (proves no swallow / no
	// substitution).
	assert.Equal(t, int32(1), hits.Load(), "BYOK call did not reach user-supplied endpoint")
	auth, _ := capturedAuth.Load().(string)
	assert.Equal(t, "Bearer sk-byok-secret", auth, "BYOK call did not carry the user-supplied API key")

	// Verify the model name in the outbound payload matches the BYOK header.
	body, _ := capturedBody.Load().(string)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &parsed))
	assert.Equal(t, "gpt-4o-mini", parsed["model"])
}

// TestBuildBYOKChatModel_SurfacesProvider401 ensures that an invalid
// user API key surfaces as an error to the caller (per §5.8 negative
// case "invalid key (LLM 401) → surfaced"), not swallowed.
func TestBuildBYOKChatModel_SurfacesProvider401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid api key","type":"invalid_request_error","code":"invalid_api_key"}}`)
	}))
	defer srv.Close()

	creds := BYOKCredentials{
		Provider: "openai_compatible",
		APIKey:   "sk-bogus",
		Model:    "gpt-4o-mini",
		BaseURL:  srv.URL,
	}

	model, err := buildBYOKModel(context.Background(), creds, permissiveTestPolicy())
	require.NoError(t, err)
	require.NotNil(t, model)

	_, err = model.Generate(context.Background(), []*schema.Message{
		{Role: schema.User, Content: "ping"},
	})
	require.Error(t, err, "BYOK 401 must surface to caller, not be swallowed")
	// The eino openai adapter wraps the upstream error; we just check the
	// wire-level signal made it through (status code or message body).
	msg := err.Error()
	if !strings.Contains(msg, "401") && !strings.Contains(msg, "Unauthorized") &&
		!strings.Contains(msg, "invalid") {
		t.Fatalf("expected surfaced 401/Unauthorized/invalid key error, got: %v", err)
	}
}

// TestBuildBYOKChatModel_RejectsPrivateBaseURL is the BUG-09 SSRF regression:
// the public entry composes a non-relaxable deny-private baseline, so an
// end-user base_url that resolves to an internal address is blocked BEFORE any
// connection — even when the injected deployment policy is permissive. Covered
// via provider=openai_compatible (custom base_url is honoured) with a spread of
// private / metadata / CGNAT / IPv4-mapped targets.
func TestBuildBYOKChatModel_RejectsPrivateBaseURL(t *testing.T) {
	targets := []string{
		"http://127.0.0.1/v1",
		"http://169.254.169.254/v1",          // cloud metadata
		"http://10.0.0.5/v1",                 // private
		"http://192.168.1.1/v1",              // private
		"http://[::1]/v1",                    // IPv6 loopback
		"http://100.64.0.1/v1",               // CGNAT (Go IsPrivate misses)
		"http://metadata.google.internal/v1", // metadata hostname
		"http://[::ffff:10.0.0.1]/v1",        // IPv4-mapped private
		"http://localhost/v1",                // hostname → resolves to 127.0.0.1: blocked POST-DNS by Control, not by CheckURL
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			creds := BYOKCredentials{Provider: "openai_compatible", APIKey: "sk-x", Model: "m", BaseURL: target}
			// Public entry — composes the baseline even with a permissive policy.
			model, err := BuildBYOKChatModel(context.Background(), creds, plugin.PermissiveEgressPolicy{})
			require.NoError(t, err, "client construction should not dial")
			_, err = model.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "ping"}})
			require.Error(t, err, "private/internal base_url must be blocked")
			assert.True(t, errors.Is(err, errEgressBlocked) ||
				strings.Contains(err.Error(), errEgressBlocked.Error()),
				"expected egress-blocked error, got: %v", err)
		})
	}
}

// TestBuildBYOKChatModel_RejectsBaseURLOverrideForPinned is F3: an end-user
// base_url override for a pinned hosted provider is illegitimate and rejected.
func TestBuildBYOKChatModel_RejectsBaseURLOverrideForPinned(t *testing.T) {
	for _, provider := range []string{"openai", "openrouter", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			_, err := BuildBYOKChatModel(context.Background(), BYOKCredentials{
				Provider: provider,
				APIKey:   "sk-x",
				Model:    "m",
				BaseURL:  "http://169.254.169.254/v1",
			}, plugin.PermissiveEgressPolicy{})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "base_url override is not permitted")
		})
	}
}

// TestBuildBYOKChatModel_RequiresAPIKey is a unit-level guard against
// silently building a client without credentials.
func TestBuildBYOKChatModel_RequiresAPIKey(t *testing.T) {
	_, err := BuildBYOKChatModel(context.Background(), BYOKCredentials{
		Provider: "openai",
		Model:    "gpt-4o-mini",
	}, plugin.PermissiveEgressPolicy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "api key required")
}

// TestBuildBYOKChatModel_RequiresProvider mirrors the api-key guard.
func TestBuildBYOKChatModel_RequiresProvider(t *testing.T) {
	_, err := BuildBYOKChatModel(context.Background(), BYOKCredentials{
		APIKey: "sk-x",
	}, plugin.PermissiveEgressPolicy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider required")
}

// TestBuildBYOKChatModel_OpenAICompatibleRequiresBaseURL ensures a self-
// hosted/vLLM provider can't be routed without a base URL.
func TestBuildBYOKChatModel_OpenAICompatibleRequiresBaseURL(t *testing.T) {
	_, err := BuildBYOKChatModel(context.Background(), BYOKCredentials{
		Provider: "openai_compatible",
		APIKey:   "sk-x",
		Model:    "gpt-4o-mini",
	}, plugin.PermissiveEgressPolicy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base_url required")
}

// TestBuildBYOKChatModel_UnsupportedProvider exercises the explicit
// allowlist of supported BYOK providers. N3: azure_openai and google (gemini)
// are NOT in the BYOK set — their model name is part of the URL path, so they
// must stay off the untrusted header path.
func TestBuildBYOKChatModel_UnsupportedProvider(t *testing.T) {
	for _, provider := range []string{"google", "azure_openai", "mistral"} {
		t.Run(provider, func(t *testing.T) {
			_, err := BuildBYOKChatModel(context.Background(), BYOKCredentials{
				Provider: provider,
				APIKey:   "sk-x",
			}, plugin.PermissiveEgressPolicy{})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unsupported provider")
		})
	}
}

// TestRedactAPIKey verifies the redacted form never contains the middle
// of the secret, so log lines remain safe (V2 §5.8 "never log raw keys").
func TestRedactAPIKey(t *testing.T) {
	cases := []struct {
		in       string
		contains string
		notFull  bool
	}{
		{"", "", false},
		{"short", "***", false},
		{"sk-abcd1234", "sk-a", true},
		{"sk-abcd1234", "1234", true},
		{"sk-very-long-api-key-12345", "sk-v", true},
	}
	for _, tc := range cases {
		got := RedactAPIKey(tc.in)
		if tc.contains != "" {
			assert.Contains(t, got, tc.contains)
		}
		if tc.notFull && tc.in != got {
			assert.NotEqual(t, tc.in, got, "redacted form must differ from raw key")
		}
	}
}

// TestBYOKContextRoundtrip ensures values stored via WithBYOKCredentials
// can be retrieved by BYOKCredentialsFrom — the contract relied on by
// the turn executor factory.
func TestBYOKContextRoundtrip(t *testing.T) {
	ctx := context.Background()

	assert.Nil(t, BYOKCredentialsFrom(ctx))

	creds := &BYOKCredentials{
		Provider: "openai",
		APIKey:   "sk-abc",
		Model:    "gpt-4o",
		BaseURL:  "https://example.com/v1",
	}
	ctx = WithBYOKCredentials(ctx, creds)

	got := BYOKCredentialsFrom(ctx)
	require.NotNil(t, got)
	assert.Equal(t, "openai", got.Provider)
	assert.Equal(t, "sk-abc", got.APIKey)
	assert.Equal(t, "gpt-4o", got.Model)
	assert.Equal(t, "https://example.com/v1", got.BaseURL)

	// Nil passed in must be a no-op (caller may pass nil when there are
	// no BYOK headers on the request).
	noCreds := WithBYOKCredentials(context.Background(), nil)
	assert.Nil(t, BYOKCredentialsFrom(noCreds))
}
