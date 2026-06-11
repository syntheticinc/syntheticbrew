package react

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

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
