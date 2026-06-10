package react

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/internal/domain"

	"github.com/syntheticinc/syntheticbrew/pkg/config"
)

// TestNewAgent_ClampsOutOfRangeMaxTurnDuration is the HIGH-1 defense-in-depth
// guard: an out-of-range max_turn_duration (e.g. a stale row that bypassed API
// validation, or one large enough to overflow time.Duration ns) must be reset to
// the engine default at construction, never flow into the timeout arithmetic.
func TestNewAgent_ClampsOutOfRangeMaxTurnDuration(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 0}, {120, 120}, {86400, 86400}, // in-range survive
		{-1, 0}, {86401, 0}, {9_300_000_000, 0}, {1 << 60, 0}, // out-of-range → default 0
	}
	for _, c := range cases {
		a, err := NewAgent(context.Background(), AgentConfig{
			ChatModel: &toolAwareMock{},
			MaxSteps:  6,
			AgentConfig: &config.AgentConfig{
				MaxTurnDuration: c.in,
				Prompts:         &config.PromptsConfig{SystemPrompt: "x"},
			},
		})
		if err != nil {
			t.Fatalf("NewAgent(maxTurnDuration=%d): %v", c.in, err)
		}
		if a.maxTurnDuration != c.want {
			t.Errorf("maxTurnDuration=%d → clamped to %d, want %d", c.in, a.maxTurnDuration, c.want)
		}
	}
}

// TestNewAgent_OutOfRangeMaxTurnDuration_ModifierDoesNotInsta-SoftLand guards the
// residual leg of HIGH-1: the clamp must run BEFORE the MessageModifier is built,
// else an overflow value flows into the modifier's soft-deadline arithmetic
// (maxTurnDuration*time*9/10 wraps negative) and finalizeDirective fires from the
// first model call, silently tool-disabling the agent.
func TestNewAgent_OutOfRangeMaxTurnDuration_ModifierDoesNotInstaSoftLand(t *testing.T) {
	a, err := NewAgent(context.Background(), AgentConfig{
		ChatModel: &toolAwareMock{},
		MaxSteps:  6,
		AgentConfig: &config.AgentConfig{
			MaxTurnDuration: 9_300_000_000, // overflows time.Duration ns when *1e9
			Prompts:         &config.PromptsConfig{SystemPrompt: "x"},
		},
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if a.messageModifier == nil {
		t.Fatal("expected a MessageModifier to be built for a non-empty system prompt")
	}
	// With the clamp applied (→0 = no time budget), the modifier must NOT soft-land
	// on time at the very start of a turn.
	if a.messageModifier.shouldFinalize(0, time.Now()) {
		t.Error("out-of-range max_turn_duration leaked into the modifier: it soft-lands at t≈0")
	}
}

// hangingStreamMock streams the final answer in chunks that keep coming AFTER the
// bounded drain grace, and does NOT honour ctx — simulating a misbehaving BYOK
// provider that keeps the socket open. The disarm must stop these late chunks
// from racing finalContent or reaching the (returned) response writer.
type hangingStreamMock struct{ hasTools bool }

func (m *hangingStreamMock) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	cp := *m
	cp.hasTools = len(tools) > 0
	return &cp, nil
}
func (m *hangingStreamMock) BindTools([]*schema.ToolInfo) error { return nil }
func (m *hangingStreamMock) GetType() string                    { return "hanging-mock" }
func (m *hangingStreamMock) IsCallbacksEnabled() bool           { return false }
func (m *hangingStreamMock) Generate(ctx context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if countToolMsgs(in) == 0 {
		return charToolCall("c", "lookup", `{"q":"x"}`), nil
	}
	return charText("done"), nil
}
func (m *hangingStreamMock) Stream(ctx context.Context, in []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	sr, sw := schema.Pipe[*schema.Message](16)
	if countToolMsgs(in) == 0 {
		go func() { defer sw.Close(); sw.Send(charToolCall("c", "lookup", `{"q":"x"}`), nil) }()
		return sr, nil
	}
	go func() {
		defer sw.Close()
		// Chunks keep arriving past the (shortened) drain grace, ignoring ctx.
		for i := 1; i <= 6; i++ {
			time.Sleep(40 * time.Millisecond)
			sw.Send(&schema.Message{Role: schema.Assistant, Content: "L" + itoaChar(i)}, nil)
		}
	}()
	return sr, nil
}

// TestStreamOwned_BoundedDrainDisarmsLateGoroutine runs under -race: when the
// stream goroutine outlives the drain grace, streamOwned must disarm it so late
// chunks neither race finalContent nor invoke the chunk callback after return.
func TestStreamOwned_BoundedDrainDisarmsLateGoroutine(t *testing.T) {
	orig := streamDrainGrace
	streamDrainGrace = 60 * time.Millisecond
	defer func() { streamDrainGrace = orig }()

	lookup := &charTool{name: "lookup", run: func(string) string { return `{"ok":true}` }}
	a, err := NewAgent(context.Background(), AgentConfig{
		ChatModel: &hangingStreamMock{},
		Tools:     []einotool.BaseTool{lookup},
		MaxSteps:  6,
		AgentConfig: &config.AgentConfig{
			MaxTurnDuration:               1,
			Prompts:                       &config.PromptsConfig{SystemPrompt: "x"},
			EnableEnhancedToolCallChecker: true,
		},
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	var lateCalls int
	chunkCb := func(string) error { lateCalls++; return nil }
	if err := a.streamOwned(context.Background(), "go", chunkCb, func(*domain.AgentEvent) error { return nil }); err != nil {
		t.Fatalf("streamOwned must return gracefully despite the hung stream: %v", err)
	}

	// Let any late chunks fire; the disarm must have stopped them reaching chunkCb.
	before := lateCalls
	time.Sleep(250 * time.Millisecond)
	if lateCalls != before {
		t.Errorf("chunk callback was invoked %d→%d after the handler returned; disarm failed",
			before, lateCalls)
	}
}
