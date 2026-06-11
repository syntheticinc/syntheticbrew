package callbacks

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agents"
)

func newCalibrationTestHandler(recorder func(int)) *ModelEventHandler {
	emitter := NewEventEmitter(func(*domain.AgentEvent) error { return nil }, "test-agent")
	h := NewModelEventHandler(emitter, NewStepCounter(), agents.NewReasoningExtractor(), nil, NewTokenAccumulator(), NewActivityClock())
	if recorder != nil {
		h.SetPromptTokensRecorder(recorder)
	}
	return h
}

// TestModelEventHandler_FeedsCalibratorPromptTokens verifies the wiring seam the
// context-size fix depends on: a model call's real prompt_tokens must reach the
// TokenCalibrator so the rewriter budget tracks real tokenization.
func TestModelEventHandler_FeedsCalibratorPromptTokens(t *testing.T) {
	cal := agents.NewTokenCalibrator()
	h := newCalibrationTestHandler(cal.RecordPromptTokens)

	h.OnModelEnd(context.Background(), nil, &model.CallbackOutput{
		Message:    &schema.Message{Role: schema.Assistant, Content: "ok"},
		TokenUsage: &model.TokenUsage{PromptTokens: 1000},
	})

	// The rewriter records the request char size; pair it with the observed tokens.
	cal.RecordRequestChars(2700)
	if got := cal.CharsPerToken(); got != 2.7 {
		t.Fatalf("OnModelEnd prompt_tokens must reach the calibrator; ratio = %v, want 2.7", got)
	}
}

// TestModelEventHandler_NilUsageAndNoRecorderAreSafe guards the optional wiring:
// no recorder set, or a nil usage, must not panic.
func TestModelEventHandler_NilUsageAndNoRecorderAreSafe(t *testing.T) {
	h := newCalibrationTestHandler(nil) // no recorder
	h.OnModelEnd(context.Background(), nil, &model.CallbackOutput{
		Message:    &schema.Message{Role: schema.Assistant, Content: "ok"},
		TokenUsage: &model.TokenUsage{PromptTokens: 1000},
	})

	called := false
	h2 := newCalibrationTestHandler(func(int) { called = true })
	h2.OnModelEnd(context.Background(), nil, &model.CallbackOutput{
		Message:    &schema.Message{Role: schema.Assistant, Content: "ok"},
		TokenUsage: nil, // no usage on this call
	})
	if called {
		t.Error("recorder must not fire when usage is nil")
	}
}
