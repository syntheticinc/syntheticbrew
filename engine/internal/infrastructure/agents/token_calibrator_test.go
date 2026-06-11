package agents

import "testing"

func TestTokenCalibrator_DefaultBeforeSample(t *testing.T) {
	c := NewTokenCalibrator()
	if got := c.CharsPerToken(); got != defaultCharsPerToken {
		t.Errorf("uncalibrated ratio = %v, want default %v", got, defaultCharsPerToken)
	}
	// A request size with no token sample yet still returns the default.
	c.RecordRequestChars(2700)
	if got := c.CharsPerToken(); got != defaultCharsPerToken {
		t.Errorf("ratio with chars but no tokens = %v, want default %v", got, defaultCharsPerToken)
	}
}

func TestTokenCalibrator_EmpiricalRatio(t *testing.T) {
	c := NewTokenCalibrator()
	c.RecordRequestChars(2700)
	c.RecordPromptTokens(1000)
	if got := c.CharsPerToken(); got != 2.7 {
		t.Errorf("empirical ratio = %v, want 2.7", got)
	}
}

func TestTokenCalibrator_ClampsImplausibleSamples(t *testing.T) {
	low := NewTokenCalibrator()
	low.RecordRequestChars(100)
	low.RecordPromptTokens(1000) // ratio 0.1 -> clamp up
	if got := low.CharsPerToken(); got != minCalibratedRatio {
		t.Errorf("low ratio = %v, want clamp %v", got, minCalibratedRatio)
	}

	high := NewTokenCalibrator()
	high.RecordRequestChars(10000)
	high.RecordPromptTokens(100) // ratio 100 -> clamp down
	if got := high.CharsPerToken(); got != maxCalibratedRatio {
		t.Errorf("high ratio = %v, want clamp %v", got, maxCalibratedRatio)
	}
}

func TestTokenCalibrator_IgnoresNonPositive(t *testing.T) {
	c := NewTokenCalibrator()
	c.RecordRequestChars(2700)
	c.RecordPromptTokens(1000) // good sample -> 2.7

	c.RecordPromptTokens(0)  // ignored
	c.RecordPromptTokens(-5) // ignored
	c.RecordRequestChars(0)  // ignored
	if got := c.CharsPerToken(); got != 2.7 {
		t.Errorf("non-positive updates must be ignored, ratio = %v, want 2.7", got)
	}
}
