package callbacks

import (
	"sync"

	"github.com/cloudwego/eino/components/model"
)

// TokenAccumulator accumulates token usage across multiple model calls within a turn.
// All methods are thread-safe.
type TokenAccumulator struct {
	promptTokens       int
	completionTokens   int
	totalTokens        int
	cachedPromptTokens int
	mu                 sync.Mutex
}

// NewTokenAccumulator creates a new TokenAccumulator.
func NewTokenAccumulator() *TokenAccumulator {
	return &TokenAccumulator{}
}

// Add adds token usage from a single model call.
func (a *TokenAccumulator) Add(usage *model.TokenUsage) {
	if usage == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.promptTokens += usage.PromptTokens
	a.completionTokens += usage.CompletionTokens
	a.totalTokens += usage.TotalTokens
	a.cachedPromptTokens += usage.PromptTokenDetails.CachedTokens
}

// TotalTokens returns the accumulated total token count.
func (a *TokenAccumulator) TotalTokens() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.totalTokens
}

// PromptTokens returns the accumulated prompt token count.
func (a *TokenAccumulator) PromptTokens() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.promptTokens
}

// CompletionTokens returns the accumulated completion token count.
func (a *TokenAccumulator) CompletionTokens() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.completionTokens
}

// CachedPromptTokens returns the accumulated count of prompt tokens served from
// the provider's prompt cache (a subset of PromptTokens). Zero when the provider
// reports no cache hits or omits prompt_tokens_details.
func (a *TokenAccumulator) CachedPromptTokens() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cachedPromptTokens
}
