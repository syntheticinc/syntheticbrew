package react

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
)

// slowSummaryMock calls a tool once (while tools are bound), then on the next
// call streams a long answer slowly — slowly enough that the turn-time wall fires
// mid-stream. It honours ctx cancellation so the test leaves no goroutine behind.
type slowSummaryMock struct {
	hasTools bool
}

func (m *slowSummaryMock) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	cp := *m
	cp.hasTools = len(tools) > 0
	return &cp, nil
}
func (m *slowSummaryMock) BindTools(tools []*schema.ToolInfo) error { return nil }
func (m *slowSummaryMock) GetType() string                          { return "slow-summary-mock" }
func (m *slowSummaryMock) IsCallbacksEnabled() bool                 { return false }

func (m *slowSummaryMock) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if countToolMsgs(in) == 0 {
		return charToolCall("c", "lookup", `{"q":"x"}`), nil
	}
	return charText("S1 S2 S3 S4 S5 S6 S7 S8"), nil
}

func (m *slowSummaryMock) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	sr, sw := schema.Pipe[*schema.Message](16)
	if countToolMsgs(in) == 0 {
		go func() { defer sw.Close(); sw.Send(charToolCall("c", "lookup", `{"q":"x"}`), nil) }()
		return sr, nil
	}
	// Final answer: stream 8 chunks at ~300ms each (~2.4s) — past the 1s wall.
	go func() {
		defer sw.Close()
		for i := 1; i <= 8; i++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(300 * time.Millisecond):
			}
			sw.Send(&schema.Message{Role: schema.Assistant, Content: "S" + itoaChar(i) + " "}, nil)
		}
	}()
	return sr, nil
}

// TestTC3_TimeBudgetMidSummaryPreserved is the partner's TC-3: the turn-time wall
// fires while the model is streaming its summary. The partial that already
// reached the client must be PRESERVED — no assistant_retract erasing it, no
// hardcoded apology replacing it — and the turn ends gracefully.
func TestTC3_TimeBudgetMidSummaryPreserved(t *testing.T) {
	lookup := &charTool{name: "lookup", run: func(args string) string { return `{"ok":true}` }}
	mock := &slowSummaryMock{}

	cfg := &config.AgentConfig{
		MaxSteps:                      6,
		MaxTurnDuration:               1, // 1s wall; the summary stream takes ~2.4s
		MaxStepDuration:               0,
		Prompts:                       &config.PromptsConfig{SystemPrompt: "Look up once, then write a long summary."},
		EnableEnhancedToolCallChecker: true,
	}
	a, err := NewAgent(context.Background(), AgentConfig{
		ChatModel:   mock,
		Tools:       []einotool.BaseTool{lookup},
		MaxSteps:    cfg.MaxSteps,
		SessionID:   "tc3",
		AgentConfig: cfg,
		ModelName:   "slow-summary-mock",
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	cap := &streamCapture{}
	if err := a.streamOwned(context.Background(), "go", cap.onChunk, cap.onEvent); err != nil {
		t.Fatalf("streamOwned must end gracefully on the time wall, got: %v", err)
	}

	t.Logf("typeSeq=%v lastAnswer=%q chunks=%d", cap.typeSeq(), cap.lastAnswer(), len(cap.chunks))

	// 2: no assistant_retract dropping the partial.
	if cap.countType(domain.EventTypeRetractAssistant) != 0 {
		t.Errorf("TC-3: a substantive partial must NOT be retracted, got %d retract events",
			cap.countType(domain.EventTypeRetractAssistant))
	}
	// 1: the partial already streamed is preserved (early chunk present), and it
	// is NOT replaced by the hardcoded apology.
	ans := cap.lastAnswer()
	if !strings.Contains(ans, "S1") {
		t.Errorf("TC-3: the streamed partial summary must be preserved (expected an early chunk), got %q", ans)
	}
	if strings.Contains(ans, "spent the time available") || strings.Contains(ans, "maximum number of steps") {
		t.Errorf("TC-3: partial must not be replaced by the hardcoded apology, got %q", ans)
	}
}
