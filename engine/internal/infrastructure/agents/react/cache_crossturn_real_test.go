//go:build live_llm

// Cross-turn prompt-cache regression guard (live). Builds two consecutive turns'
// request bodies with the ACTUAL MessageModifier (two-message frozen head) + the ACTUAL
// NewCacheControlModifier, sends them to the real qwen3.7-plus via OpenRouter, and proves
// the cache-marked stable head is reused across turns even when the user's question (and
// thus the volatile head) changes. Before the head split this returned cached=0 on a
// changed-question turn (the whole system prompt re-billed every turn).
//
//	SYNTHETICBREW_E2E_OPENROUTER_KEY=... go test -tags live_llm -run TestCrossTurnCacheReal -v ./internal/infrastructure/agents/react/
package react

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

func crossturnOpenAIBody(msgs []*schema.Message) []byte {
	type m struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	out := make([]m, 0, len(msgs))
	for _, x := range msgs {
		out = append(out, m{Role: string(x.Role), Content: x.Content})
	}
	b, _ := json.Marshal(map[string]any{
		"model": "qwen/qwen3.7-plus", "messages": out, "max_tokens": 5, "temperature": 0,
	})
	return b
}

func crossturnPost(t *testing.T, key string, body []byte) (promptTokens, cached int) {
	t.Helper()
	req, _ := http.NewRequest("POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-session-id", "crossturn-real-probe-001")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var d struct {
		Usage struct {
			PromptTokens        int `json:"prompt_tokens"`
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	require.NoError(t, json.Unmarshal(raw, &d), "decode OR response")
	return d.Usage.PromptTokens, d.Usage.PromptTokensDetails.CachedTokens
}

func TestCrossTurnCacheReal(t *testing.T) {
	key := os.Getenv("SYNTHETICBREW_E2E_OPENROUTER_KEY")
	if key == "" {
		t.Skip("SYNTHETICBREW_E2E_OPENROUTER_KEY not set")
	}
	ctx := context.Background()
	sysPrompt := strings.Repeat("Ты ассистент по подключению IoT LoRaWAN устройств. Соблюдай протоколы провижининга. ", 400)
	tools := []string{"show_structured_output", "device_provision_lorawan", "connection_list"}
	cc := llm.NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true})
	require.NotNil(t, cc, "cc modifier must be honored for openai_compatible")

	// One turn = a FRESH MessageModifier (production builds a new agent per turn), so the
	// frozen head is rebuilt with that turn's CURRENT TASK.
	buildBody := func(input []*schema.Message) ([]byte, string) {
		mm := NewMessageModifier(MessageModifierConfig{SystemPrompt: sysPrompt, ToolNames: tools, SessionID: "s1"})
		msgs := mm.Modify(ctx, input)
		ccBody, err := cc(crossturnOpenAIBody(msgs))
		require.NoError(t, err)
		return ccBody, msgs[0].Content // msgs[0] = the cache-marked STABLE head
	}

	q1, q2 := "Подключи устройство Alpha по LoRaWAN", "Теперь подключи устройство Beta с другим DevEUI"

	// Turn 1 (warm the cache).
	b1, stable1 := buildBody([]*schema.Message{schema.UserMessage(q1)})
	_, c1 := crossturnPost(t, key, b1)

	// Turn 2 — a DIFFERENT question (real multi-turn). The volatile head changes, but the
	// cache-marked stable head must be byte-identical, so the provider reuses its cache.
	b2, stable2 := buildBody([]*schema.Message{schema.UserMessage(q1), schema.AssistantMessage("Готово.", nil), schema.UserMessage(q2)})
	p2, c2 := crossturnPost(t, key, b2)

	t.Logf("turn1 cached=%d ; turn2(changed question) prompt=%d cached=%d", c1, p2, c2)

	// Mechanism: the cache-marked stable head is turn-invariant despite the new question.
	require.Equal(t, stable1, stable2,
		"the cache-marked stable head must be byte-identical across turns (CURRENT TASK lives in the volatile head)")
	// Effect: a changed-question turn still reads the stable head from cache (was 0 before the fix).
	require.Greater(t, c2, p2/2,
		"cross-turn: the stable head (>half the prompt) must be served from cache on a changed-question turn; got cached=%d of prompt=%d", c2, p2)
}
