package callbacks

import (
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/stretchr/testify/assert"
)

func TestTokenAccumulator_AddSumsAcrossCalls(t *testing.T) {
	acc := NewTokenAccumulator()

	acc.Add(&model.TokenUsage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120})
	acc.Add(&model.TokenUsage{PromptTokens: 150, CompletionTokens: 30, TotalTokens: 180})

	assert.Equal(t, 250, acc.PromptTokens())
	assert.Equal(t, 50, acc.CompletionTokens())
	assert.Equal(t, 300, acc.TotalTokens())
	assert.Equal(t, 0, acc.CachedPromptTokens(), "no cache details reported → zero")
}

func TestTokenAccumulator_CachedTracksLastCall_PromptSums(t *testing.T) {
	acc := NewTokenAccumulator()

	// Step 1: cold prefix, nothing cached.
	acc.Add(&model.TokenUsage{
		PromptTokens:     2000,
		CompletionTokens: 40,
		TotalTokens:      2040,
	})
	// Step 2+: provider reports a cache hit on the stable prefix (grows per step).
	acc.Add(&model.TokenUsage{
		PromptTokens:       2100,
		CompletionTokens:   55,
		TotalTokens:        2155,
		PromptTokenDetails: model.PromptTokenDetails{CachedTokens: 1800},
	})
	acc.Add(&model.TokenUsage{
		PromptTokens:       2200,
		CompletionTokens:   60,
		TotalTokens:        2260,
		PromptTokenDetails: model.PromptTokenDetails{CachedTokens: 1900},
	})

	// prompt/completion/total still sum across the turn (cost totals); cached is
	// the current reused prefix = the last call's value.
	assert.Equal(t, 1900, acc.CachedPromptTokens(), "cached = current reused prefix (last call)")
	assert.Equal(t, 6300, acc.PromptTokens())
}

// RED repro for the prod bug: the chat footer showed "cached" larger than the
// context/used tokens. Cached is documented as "a subset of PromptTokens" — it
// can never exceed the prompt of the call it describes. Summing per-call cached
// across a multi-step turn (each call re-reporting its full reused prefix)
// violates that invariant: with a growing prefix the sum balloons past any
// single call's prompt. The context-usage bar shows the CURRENT reused prefix,
// so cached must track the last call's value, not the cross-step sum.
func TestTokenAccumulator_CachedReflectsCurrentPrefix_NotSum(t *testing.T) {
	acc := NewTokenAccumulator()

	// Staircase cache growth across a 3-step tool loop (real qwen3.7-plus shape).
	acc.Add(&model.TokenUsage{PromptTokens: 2000, TotalTokens: 2040})
	acc.Add(&model.TokenUsage{
		PromptTokens:       2100,
		TotalTokens:        2155,
		PromptTokenDetails: model.PromptTokenDetails{CachedTokens: 1800},
	})
	acc.Add(&model.TokenUsage{
		PromptTokens:       2200,
		TotalTokens:        2260,
		PromptTokenDetails: model.PromptTokenDetails{CachedTokens: 1900},
	})

	// The current reused prefix is the last call's cached (1900), never the sum.
	assert.Equal(t, 1900, acc.CachedPromptTokens(),
		"cached must reflect the current reused prefix, not the cross-step sum")
	// Invariant: cached can never exceed the largest single-call prompt.
	assert.LessOrEqual(t, acc.CachedPromptTokens(), 2200,
		"cached is a subset of a prompt and cannot exceed any single call's prompt")
}

func TestTokenAccumulator_AddNilIsNoop(t *testing.T) {
	acc := NewTokenAccumulator()
	acc.Add(nil)
	assert.Equal(t, 0, acc.TotalTokens())
	assert.Equal(t, 0, acc.CachedPromptTokens())
}
