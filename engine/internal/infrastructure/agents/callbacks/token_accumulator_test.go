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

func TestTokenAccumulator_AccumulatesCachedPromptTokens(t *testing.T) {
	acc := NewTokenAccumulator()

	// Step 1: cold prefix, nothing cached.
	acc.Add(&model.TokenUsage{
		PromptTokens:     2000,
		CompletionTokens: 40,
		TotalTokens:      2040,
	})
	// Step 2+: provider reports a cache hit on the stable prefix.
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

	assert.Equal(t, 3700, acc.CachedPromptTokens(), "cached tokens accumulate across steps")
	assert.Equal(t, 6300, acc.PromptTokens())
}

func TestTokenAccumulator_AddNilIsNoop(t *testing.T) {
	acc := NewTokenAccumulator()
	acc.Add(nil)
	assert.Equal(t, 0, acc.TotalTokens())
	assert.Equal(t, 0, acc.CachedPromptTokens())
}
