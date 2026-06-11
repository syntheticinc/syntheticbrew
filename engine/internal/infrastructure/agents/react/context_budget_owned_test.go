package react

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/pkg/config"
)

// TestStreamOwned_EnforcesContextBudget is the end-to-end regression for the
// max_context_size under-count through the real owned graph: a long history that
// blows past a tight max_context_size must reach the model COMPRESSED. Before the
// fix the chars/4 guard under-counted and shipped the whole history; now the model
// sees a bounded slice. The mock model captures exactly what reached it after
// rewriter+modifier.
func TestStreamOwned_EnforcesContextBudget(t *testing.T) {
	var history []*schema.Message
	for i := 0; i < 60; i++ {
		history = append(history,
			&schema.Message{Role: schema.User, Content: fmt.Sprintf("Q%d %s", i, strings.Repeat("x", 100))},
			&schema.Message{Role: schema.Assistant, Content: fmt.Sprintf("A%d %s", i, strings.Repeat("y", 300))},
		)
	}

	var mu sync.Mutex
	maxSeen := 0
	model := historyChatModel(func(input []*schema.Message) *schema.Message {
		mu.Lock()
		if len(input) > maxSeen {
			maxSeen = len(input)
		}
		mu.Unlock()
		return charText("done")
	})

	cfg := charAgentConfig(func(c *config.AgentConfig) {
		c.MaxContextSize = 2000 // tight: budget = 2000 * 0.9 * 2.5 = 4500 chars
	})

	a, err := NewAgent(context.Background(), AgentConfig{
		ChatModel:       model,
		MaxSteps:        cfg.MaxSteps,
		SessionID:       "ctx-budget-session",
		AgentConfig:     cfg,
		ModelName:       "mock",
		HistoryMessages: history,
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	cap := &streamCapture{}
	if err := a.streamOwned(context.Background(), "go", cap.onChunk, cap.onEvent); err != nil {
		t.Fatalf("streamOwned: %v", err)
	}

	// History alone is 120 messages (+ current user + system). The compressed
	// request must be far smaller; without enforcement the model would see ~122.
	full := len(history) + 1
	if maxSeen == 0 {
		t.Fatal("model was never called")
	}
	if maxSeen >= full {
		t.Errorf("context not enforced: model saw %d messages, full history+input was %d", maxSeen, full)
	}
	if maxSeen > 60 {
		t.Errorf("compression too weak under a tight budget: model saw %d messages", maxSeen)
	}
	t.Logf("model saw %d messages (full would be %d)", maxSeen, full)
}

// TestStreamOwned_FinalizeCompressesContext guards that the budget-wall finalize
// node also runs the rewriter: a turn that diverts to finalize with a large
// transcript must feed the finalize model a COMPRESSED context, not the whole
// history. Without the rewriter call in finalizePreHandle the forced summary would
// itself overflow.
func TestStreamOwned_FinalizeCompressesContext(t *testing.T) {
	var history []*schema.Message
	for i := 0; i < 50; i++ {
		history = append(history,
			&schema.Message{Role: schema.User, Content: fmt.Sprintf("Q%d %s", i, strings.Repeat("x", 100))},
			&schema.Message{Role: schema.Assistant, Content: fmt.Sprintf("A%d %s", i, strings.Repeat("y", 300))},
		)
	}

	finalizeLen := -1
	tool := &charTool{name: "scan", run: func(string) string { return `{"ok":true}` }}
	model := historyChatModel(func(input []*schema.Message) *schema.Message {
		if sawFinalizeDirective(input) {
			finalizeLen = len(input)
			return charText("done")
		}
		return charToolCall("c", "scan", `{"n":1}`)
	})

	cfg := charAgentConfig(func(c *config.AgentConfig) {
		c.MaxSteps = 1          // hit the step-budget wall fast → divert to finalize
		c.MaxContextSize = 2000 // tight
	})

	a, err := NewAgent(context.Background(), AgentConfig{
		ChatModel:       model,
		Tools:           []einotool.BaseTool{tool},
		MaxSteps:        cfg.MaxSteps,
		SessionID:       "finalize-budget-session",
		AgentConfig:     cfg,
		ModelName:       "mock",
		HistoryMessages: history,
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	cap := &streamCapture{}
	if err := a.streamOwned(context.Background(), "go", cap.onChunk, cap.onEvent); err != nil {
		t.Fatalf("streamOwned: %v", err)
	}

	if finalizeLen < 0 {
		t.Fatal("finalize node never ran — the budget wall did not divert as expected")
	}
	if finalizeLen >= len(history) {
		t.Errorf("finalize received an uncompressed transcript: %d messages (history is %d) — rewriter did not run in finalizePreHandle",
			finalizeLen, len(history))
	}
	t.Logf("finalize saw %d messages (history %d)", finalizeLen, len(history))
}
