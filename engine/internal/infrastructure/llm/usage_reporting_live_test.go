//go:build live_llm

package llm

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	openaiext "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// TestUsageReporting_LiveStreamingCachedVisible is the load-bearing STREAMING proof for
// task B: with the usageReportingTransport auto-attached by CreateClientFromDBModel for
// an OpenRouter base URL (it injects usage:{include:true}), the engine OBSERVES
// prompt_tokens_details.cached_tokens on the streaming path — not just non-streaming.
// The engine streams every real chat, so this closes the open question that the
// non-streaming append-increment test left. The transport is wired from source here; no
// manual usage injection — only the cache_control modifier marks the prefix, exactly as
// production attaches it on the chat node. Build-tagged live_llm (paid, ~$0.10).
//
//	SYNTHETICBREW_E2E_OPENROUTER_KEY=... go test -tags live_llm \
//	  -run TestUsageReporting_LiveStreamingCachedVisible -v ./internal/infrastructure/llm/
func TestUsageReporting_LiveStreamingCachedVisible(t *testing.T) {
	key := os.Getenv("SYNTHETICBREW_E2E_OPENROUTER_KEY")
	if key == "" {
		t.Skip("SYNTHETICBREW_E2E_OPENROUTER_KEY not set")
	}

	// Production construction: the OpenRouter base URL auto-wires usageReportingTransport.
	client, err := CreateClientFromDBModel(models.LLMProviderModel{
		Type:            "openai_compatible",
		BaseURL:         "https://openrouter.ai/api/v1",
		ModelName:       "qwen/qwen3.7-plus",
		APIKeyEncrypted: key,
	})
	require.NoError(t, err)

	// Same cache-marking modifier the factory builds for a cache-enabled model. No manual
	// usage injection — usageReportingTransport adds usage:{include:true} on the wire.
	mod := NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true, MinPrefixTokens: 1})
	require.NotNil(t, mod)
	cacheOpt := openaiext.WithRequestPayloadModifier(
		func(_ context.Context, _ []*schema.Message, raw []byte) ([]byte, error) { return mod(raw) },
	)

	head := "You are an enterprise IoT support agent. Standing policy:\n" +
		strings.Repeat("Each tool returns JSON with a status and a data field; errors carry an error "+
			"field and a numeric code. Investigate carefully and cite device ids. ", 90)

	// streamCached consumes the whole stream and returns the final reported cached tokens.
	streamCached := func(msgs []*schema.Message) int {
		sr, gerr := client.Stream(context.Background(), msgs, cacheOpt,
			openaiext.WithExtraHeader(map[string]string{"x-session-id": "rit-usage-stream-001"}))
		require.NoError(t, gerr)
		defer sr.Close()
		cached := 0
		for {
			frame, rerr := sr.Recv()
			if rerr == io.EOF {
				break
			}
			require.NoError(t, rerr)
			if frame.ResponseMeta != nil && frame.ResponseMeta.Usage != nil {
				cached = frame.ResponseMeta.Usage.PromptTokenDetails.CachedTokens
			}
		}
		return cached
	}

	transcript := []*schema.Message{schema.SystemMessage(head),
		schema.UserMessage("Investigate device X1 fault thoroughly.")}

	const steps = 5
	var cachedPerStep []int
	for s := 1; s <= steps; s++ {
		c := streamCached(transcript)
		cachedPerStep = append(cachedPerStep, c)
		t.Logf("step %d: streaming cached=%d", s, c)
		// Grow the stable prefix by one frozen exchange.
		transcript = append(transcript,
			schema.AssistantMessage(fmt.Sprintf("Checking source %d.", s), nil),
			schema.UserMessage(fmt.Sprintf("Tool result %d: {\"status\":\"ok\",\"data\":\"%s\"}",
				s, strings.Repeat("payload ", 10))))
	}

	t.Logf("streaming cached-per-step = %v", cachedPerStep)
	maxLate := 0
	for _, x := range cachedPerStep[2:] {
		if x > maxLate {
			maxLate = x
		}
	}
	require.Greater(t, maxLate, 0,
		"usageReportingTransport must surface cached_tokens on the STREAMING path for OpenRouter")
}
