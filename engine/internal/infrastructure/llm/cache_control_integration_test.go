package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openaiext "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// TestCacheControl_EndToEndWire is the L2 integration proof: a real eino-ext
// openai client (built exactly as production via CreateClientFromDBModel) sends a
// chat completion through the WithRequestPayloadModifier seam, and we assert (a)
// the bytes ON THE WIRE carry cache_control on the stable prefix, and (b) the
// provider's prompt_tokens_details.cached_tokens flows back through eino into the
// response usage. This exercises the same seam the owned graph attaches to the
// chat node.
func TestCacheControl_EndToEndWire(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl-1",
			"object": "chat.completion",
			"created": 1,
			"model": "test-model",
			"choices": [{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage": {"prompt_tokens":2000,"completion_tokens":5,"total_tokens":2005,"prompt_tokens_details":{"cached_tokens":1800}}
		}`))
	}))
	defer srv.Close()

	// Production model construction — openai_compatible, pointed at the fake.
	client, err := CreateClientFromDBModel(models.LLMProviderModel{
		Type:            "openai_compatible",
		BaseURL:         srv.URL,
		ModelName:       "test-model",
		APIKeyEncrypted: "test-key",
	})
	require.NoError(t, err)

	// Same modifier the factory builds for a cache-enabled model.
	mod := NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true, MinPrefixTokens: 1})
	require.NotNil(t, mod)
	opt := openaiext.WithRequestPayloadModifier(
		func(_ context.Context, _ []*schema.Message, rawBody []byte) ([]byte, error) {
			return mod(rawBody)
		},
	)

	big := strings.Repeat("context line that makes the prefix exceed the cache gate. ", 80)
	msgs := []*schema.Message{
		schema.SystemMessage("system instructions: " + big),
		schema.UserMessage("user question: " + big),
	}

	out, err := client.Generate(context.Background(), msgs, opt)
	require.NoError(t, err)
	require.NotNil(t, out)

	// (a) Wire body carries cache_control on the stable prefix.
	require.NotEmpty(t, capturedBody, "the fake provider must have received a request")
	var wire struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(capturedBody, &wire))
	require.Len(t, wire.Messages, 2)
	assert.Contains(t, string(wire.Messages[0].Content), "ephemeral",
		"system message on the wire must carry cache_control:{ephemeral}")
	assert.Contains(t, string(wire.Messages[1].Content), "ephemeral",
		"last (history) message on the wire must carry cache_control:{ephemeral}")
	// Original text survived the rewrite.
	assert.Contains(t, string(wire.Messages[0].Content), "system instructions")

	// (b) cached_tokens flowed back through eino into the response usage.
	require.NotNil(t, out.ResponseMeta)
	require.NotNil(t, out.ResponseMeta.Usage)
	assert.Equal(t, 1800, out.ResponseMeta.Usage.PromptTokenDetails.CachedTokens,
		"provider cached_tokens must reach the engine via schema.TokenUsage")
}

// TestCacheControl_DisabledWireUnchanged proves TC-CACHE-3 at the wire level: with
// no modifier attached (caching off), the request body has no cache_control and
// content stays a plain string — byte-shape identical to pre-feature behaviour.
func TestCacheControl_DisabledWireUnchanged(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c","object":"chat.completion","created":1,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`))
	}))
	defer srv.Close()

	client, err := CreateClientFromDBModel(models.LLMProviderModel{
		Type:            "openai_compatible",
		BaseURL:         srv.URL,
		ModelName:       "test-model",
		APIKeyEncrypted: "test-key",
	})
	require.NoError(t, err)

	// Caching off → factory builds a nil modifier → no option attached.
	require.Nil(t, NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: false}))

	_, err = client.Generate(context.Background(),
		[]*schema.Message{schema.SystemMessage("hi"), schema.UserMessage("q")})
	require.NoError(t, err)

	require.NotEmpty(t, capturedBody)
	assert.NotContains(t, string(capturedBody), "cache_control",
		"with caching off the wire body must not carry cache_control")
}
