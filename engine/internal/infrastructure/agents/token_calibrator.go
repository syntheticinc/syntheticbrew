package agents

import "sync"

// Calibrated-ratio clamps. Empirical chars-per-token outside this band signals a
// measurement anomaly (cached prompt tokens, multimodal payloads, a degenerate
// tiny request) rather than real text, so we fall back to the band edge instead of
// trusting an implausible sample for the budget.
const (
	minCalibratedRatio = 1.5
	maxCalibratedRatio = 6.0
)

// TokenCalibrator derives an empirical chars-per-token ratio from observed
// provider usage so context-size decisions track real tokenization rather than a
// fixed guess. One instance is created per agent (in NewAgent) and refines the
// ratio across the model calls WITHIN a turn: the context rewriter records the
// char size of each request it produces, the model callback records the
// prompt_tokens the provider reported for that request, and CharsPerToken pairs the
// most recent of each. The two writes arrive in order (rewrite → model call →
// usage), so the pair is consistent. A new turn builds a fresh agent, so the first
// model call of a turn falls back to the conservative cold-start default until the
// first usage sample lands.
type TokenCalibrator struct {
	mu             sync.Mutex
	lastChars      int
	lastPromptToks int
}

// NewTokenCalibrator creates an uncalibrated calibrator; CharsPerToken returns the
// conservative default until the first usable sample arrives.
func NewTokenCalibrator() *TokenCalibrator {
	return &TokenCalibrator{}
}

// RecordRequestChars stores the estimated char size of the request just produced.
func (c *TokenCalibrator) RecordRequestChars(chars int) {
	if chars <= 0 {
		return
	}
	c.mu.Lock()
	c.lastChars = chars
	c.mu.Unlock()
}

// RecordPromptTokens stores the real prompt_tokens the provider reported for the
// most recent request.
func (c *TokenCalibrator) RecordPromptTokens(tokens int) {
	if tokens <= 0 {
		return
	}
	c.mu.Lock()
	c.lastPromptToks = tokens
	c.mu.Unlock()
}

// CharsPerToken returns the empirical ratio (clamped to a plausible band), or the
// conservative default before a usable sample exists.
func (c *TokenCalibrator) CharsPerToken() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastChars <= 0 || c.lastPromptToks <= 0 {
		return defaultCharsPerToken
	}
	ratio := float64(c.lastChars) / float64(c.lastPromptToks)
	if ratio < minCalibratedRatio {
		return minCalibratedRatio
	}
	if ratio > maxCalibratedRatio {
		return maxCalibratedRatio
	}
	return ratio
}
