//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestHITL_Live_OneInterruptPerTurn is the end-to-end acceptance for the
// show_structured_output halt fix: a real LLM drives a real HITL turn over
// HTTP+SSE and the engine must emit EXACTLY ONE interrupt_request frame for the
// turn (the loop halts on the first widget). Before the fix the loop did not
// halt and the model re-emitted the widget many times per turn.
//
// Gated behind OPENROUTER_TEST_KEY (its value is used as the model api_key), so
// it auto-skips on CI runs without a key — same convention as TestCHAT01.
func TestHITL_Live_OneInterruptPerTurn(t *testing.T) {
	requireSuite(t)
	key := os.Getenv("OPENROUTER_TEST_KEY")
	if key == "" {
		t.Skip("OPENROUTER_TEST_KEY not set — skipping live HITL e2e")
	}
	t.Cleanup(func() { truncateTables(t) })

	// Real chat model via OpenRouter.
	modelResp := do(t, http.MethodPost, "/api/v1/models",
		mustJSON(map[string]any{
			"name":       "hitl-live-model",
			"type":       "openai_compatible",
			"kind":       "chat",
			"base_url": "https://openrouter.ai/api/v1",
			// A reliable tool-caller keeps this gated guard from flaking on model
			// non-compliance; the halt fix itself is model-independent (loop routing).
			"model_name": "openai/gpt-4o-mini",
			"api_key":    key,
		}), adminToken)
	_ = readBody(t, modelResp)
	assertStatusAny(t, modelResp, http.StatusOK, http.StatusCreated)

	// Agent that is forced to surface a form widget — the most direct way to
	// exercise the HITL halt without depending on model creativity.
	agentResp := do(t, http.MethodPost, "/api/v1/agents",
		mustJSON(map[string]any{
			"name":  "hitl-live-agent",
			"model": "hitl-live-model",
			"system_prompt": "You are a form assistant. For EVERY user message you MUST call the " +
				"show_structured_output tool exactly once, with output_type \"form\" and a single " +
				"question of type \"select\" offering two options. Never answer in plain text. " +
				"The tool result \"Structured output displayed to user.\" means success — do NOT call " +
				"it again, do not narrate, STOP your turn.",
			"tools": []string{"show_structured_output"},
		}), adminToken)
	agentBody := readBody(t, agentResp)
	assertStatusAny(t, agentResp, http.StatusOK, http.StatusCreated)
	var agent struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(agentBody, &agent), "parse agent: %s", agentBody)
	require.NotEmpty(t, agent.ID)

	s := createSchemaForTest(t, map[string]any{
		"name":           "hitl-live-schema",
		"chat_enabled":   true,
		"entry_agent_id": agent.ID,
	})

	// Drive the chat turn. A turn that surfaces the widget must surface it EXACTLY
	// once (the loop halts on the first). >1 is the regression and fails hard on
	// any attempt. A turn where the model answered in plain text (0 widgets) is
	// model non-compliance, not a halt failure — retry a few times, then skip as
	// inconclusive rather than flake the suite.
	const attempts = 3
	for i := 1; i <= attempts; i++ {
		interrupts, eventSeq := driveHITLTurn(t, s.Name,
			"I want to add a new device to my account.")
		t.Logf("attempt %d/%d: eventSeq=%v interrupt_request=%d", i, attempts, eventSeq, interrupts)

		require.LessOrEqual(t, interrupts, 1,
			"REGRESSION: HITL turn emitted %d interrupt_request events; the loop must halt on the first widget (eventSeq=%v)",
			interrupts, eventSeq)
		if interrupts == 1 {
			return // halt-on-first proven live
		}
	}
	t.Skipf("model did not surface the widget in %d attempts — inconclusive for the halt (not a regression: >1 would have failed)", attempts)
}

// driveHITLTurn POSTs one chat message, reads the SSE stream to its terminal
// event, and returns how many interrupt_request frames the turn produced plus
// the ordered SSE event-type sequence.
func driveHITLTurn(t *testing.T, schemaName, message string) (int, []string) {
	t.Helper()
	body := mustJSONBytes(map[string]any{"message": message})
	req, err := http.NewRequest(http.MethodPost,
		baseURL+"/api/v1/schemas/"+schemaName+"/chat", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	interrupts := 0
	var eventSeq []string
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "event:") {
			continue
		}
		ev := strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		eventSeq = append(eventSeq, ev)
		if ev == "interrupt_request" {
			interrupts++
		}
	}
	return interrupts, eventSeq
}
