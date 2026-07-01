//go:build live_llm

// Empirical placement probe (live qwen3.7-plus). Reproduces the partner's cross-turn
// miss with a LARGE history and finds where the per-turn volatile block (CURRENT TASK +
// reminders) must sit so the whole append-only history caches cross-turn — not just the
// system prompt. Answers whether a trailing SYSTEM message is hoisted to the front by
// DashScope's template (which would defeat a tail placement) by comparing tail-system vs
// tail-user vs the no-volatile ceiling.
//
//	SYNTHETICBREW_E2E_OPENROUTER_KEY=... go test -tags live_llm \
//	  -run TestCachePlacementProbe -v -timeout 900s ./internal/infrastructure/agents/react/
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
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// placementPost sends the cc-marked body under a per-placement sticky session and returns
// (prompt_tokens, cached_tokens).
func placementPost(t *testing.T, key, sessionID string, msgs []*schema.Message, cc func([]byte) ([]byte, error)) (int, int) {
	t.Helper()
	body, err := cc(crossturnOpenAIBody(msgs))
	require.NoError(t, err)
	req, _ := http.NewRequest("POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-session-id", sessionID)
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
	require.NoError(t, json.Unmarshal(raw, &d), "decode OR response: %s", string(raw))
	return d.Usage.PromptTokens, d.Usage.PromptTokensDetails.CachedTokens
}

func TestCachePlacementProbe(t *testing.T) {
	key := os.Getenv("SYNTHETICBREW_E2E_OPENROUTER_KEY")
	if key == "" {
		t.Skip("SYNTHETICBREW_E2E_OPENROUTER_KEY not set")
	}
	cc := llm.NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true})
	require.NotNil(t, cc)

	// Stable head (cc-marked): the big, turn-invariant system prompt (~2k tokens).
	stable := schema.SystemMessage(strings.Repeat("You are an IoT provisioning assistant. Follow the policy; cite device ids; never invent data. ", 200))
	// The conversation history's bulk: one huge tool result (~30k tokens), like the partner's
	// 39k-token list_use_case output. Byte-identical across turns (frozen history).
	bigTool := &schema.Message{Role: schema.Tool, ToolCallID: "t1",
		Content: "[TOOL OUTPUT] " + strings.Repeat(`{"id":"dev","desc":"a device automation scenario record with fields and details"},`, 2000)}

	q1, q2 := "What can I automate on my account?", "Now delete the device named Car_Main"
	vol := func(q string) string {
		return `**CURRENT TASK:** Answer the user's question: "` + q + `"` + "\nDo NOT get distracted - answer THIS question!"
	}

	// H1 = turn-1 conversation (becomes turn-2's byte-stable history prefix).
	h1 := []*schema.Message{
		schema.UserMessage(q1),
		bigTool,
		schema.AssistantMessage("Based on your profile, here are the options.", nil),
	}
	// turn-2 conversation = H1 + the new question.
	h2 := append(append([]*schema.Message{}, h1...), schema.UserMessage(q2))

	sys := func(s string) *schema.Message { return schema.SystemMessage(s) }
	usr := func(s string) *schema.Message { return schema.UserMessage(s) }

	// Four placements of the per-turn volatile block.
	build := map[string]struct{ turn1, turn2 []*schema.Message }{
		"front-system": { // current 1.10.1: volatile at idx 1, before the history
			turn1: append([]*schema.Message{stable, sys(vol(q1))}, h1...),
			turn2: append([]*schema.Message{stable, sys(vol(q2))}, h2...),
		},
		"tail-system": { // volatile as a trailing system message (may be hoisted)
			turn1: append(append([]*schema.Message{stable}, h1...), sys(vol(q1))),
			turn2: append(append([]*schema.Message{stable}, h2...), sys(vol(q2))),
		},
		"tail-user": { // volatile as a trailing user message (no hoisting)
			turn1: append(append([]*schema.Message{stable}, h1...), usr(vol(q1))),
			turn2: append(append([]*schema.Message{stable}, h2...), usr(vol(q2))),
		},
		"none": { // no volatile at all — the ceiling (max cacheable history)
			turn1: append([]*schema.Message{stable}, h1...),
			turn2: append([]*schema.Message{stable}, h2...),
		},
	}

	ctx := context.Background()
	_ = ctx
	for _, name := range []string{"front-system", "tail-system", "tail-user", "none"} {
		b := build[name]
		sid := "placement-probe-" + name
		placementPost(t, key, sid, b.turn1, cc) // warm turn 1
		time.Sleep(15 * time.Second)
		prompt, cached := placementPost(t, key, sid, b.turn2, cc) // changed-question turn 2
		pct := 0.0
		if prompt > 0 {
			pct = 100 * float64(cached) / float64(prompt)
		}
		t.Logf("placement=%-12s  turn2 prompt=%6d cached=%6d  (%.0f%% cached)", name, prompt, cached, pct)
		time.Sleep(15 * time.Second)
	}
	t.Log("READ: front-system should cache ~only the system prompt (bug); the winning tail placement should cache ~= none (the whole history)")
}
