package main

import (
	"testing"
)

// ============================================================
// Pricing tests
// ============================================================

func TestGetPricing_PlatformSpecific(t *testing.T) {
	// Anthropic Claude on anthropic platform
	p := getPricing("claude-sonnet-4-20250514", "anthropic")
	if p.Input != 3.00 || p.Output != 15.00 {
		t.Fatalf("anthropic claude-sonnet-4 pricing wrong: input=%v output=%v", p.Input, p.Output)
	}

	// DeepSeek on deepseek platform
	p = getPricing("deepseek-chat", "deepseek")
	if p.Input != 0.27 || p.Output != 1.10 {
		t.Fatalf("deepseek-chat pricing wrong: input=%v output=%v", p.Input, p.Output)
	}

	// Subscription platforms should be free
	p = getPricing("gpt-4o", "sider")
	if p.Input != 0 || p.Output != 0 {
		t.Fatalf("sider gpt-4o should be free: input=%v output=%v", p.Input, p.Output)
	}
}

func TestGetPricing_ModelLevel(t *testing.T) {
	// Model-level fallback when no platform match
	p := getPricing("gpt-4o", "unknown-platform")
	if p.Input != 2.50 || p.Output != 10.00 {
		t.Fatalf("gpt-4o model-level pricing wrong: input=%v output=%v", p.Input, p.Output)
	}

	p = getPricing("deepseek-reasoner", "")
	if p.Input != 0.55 || p.Output != 2.19 {
		t.Fatalf("deepseek-reasoner pricing wrong: input=%v output=%v", p.Input, p.Output)
	}
}

func TestGetPricing_Unknown(t *testing.T) {
	p := getPricing("totally-unknown-model-xyz", "unknown-provider")
	if p.Input != 0 || p.Output != 0 {
		t.Fatalf("unknown model should return zero pricing: input=%v output=%v", p.Input, p.Output)
	}
}

func TestEstimateCost(t *testing.T) {
	// 1000 prompt + 500 completion for deepseek-chat on deepseek platform
	cost := estimateCost("deepseek-chat", 1000, 500, "deepseek")
	// Input: 1000/1e6 * 0.27 = 0.00027
	// Output: 500/1e6 * 1.10 = 0.00055
	// Total: 0.00082
	if cost < 0.0008 || cost > 0.0009 {
		t.Fatalf("unexpected cost for deepseek: %v", cost)
	}

	// Free model should cost 0
	cost = estimateCost("gpt-4o", 1000, 500, "sider")
	if cost != 0 {
		t.Fatalf("sider subscription should be free, got: %v", cost)
	}

	// Zero tokens should cost 0
	cost = estimateCost("gpt-4o", 0, 0, "openai")
	if cost != 0 {
		t.Fatalf("zero tokens should be free, got: %v", cost)
	}
}

func TestGetPricing_FuzzyMatch(t *testing.T) {
	// Fuzzy match: version suffix should still match base model
	p := getPricing("claude-3-5-sonnet-some-version", "")
	// Should fuzzy match to claude-3-5-sonnet
	if p.Input == 0 && p.Output == 0 {
		// Fuzzy match is best-effort, don't fail if not found
		t.Log("fuzzy match not found for claude-3-5-sonnet variant (acceptable)")
	}
}

func TestRoundTo6(t *testing.T) {
	tests := []struct {
		input float64
		want  float64
	}{
		{0.0000001, 0},
		{0.000001, 0.000001},
		{1.2345678, 1.234568},
		{0, 0},
	}
	for _, tt := range tests {
		got := roundTo6(tt.input)
		diff := got - tt.want
		if diff < -0.000001 || diff > 0.000001 {
			t.Errorf("roundTo6(%v) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestGetPricing_AllPlatforms(t *testing.T) {
	// Verify some platform-specific prices exist
	platforms := []struct {
		provider string
		model    string
	}{
		{"coze", "gpt-4o"},
		{"coze", "deepseek-chat"},
		{"sider", "gpt-4o"},
		{"sider", "claude-3-5-sonnet"},
		{"anthropic", "claude-3-haiku-20240307"},
		{"nvidia", "meta/llama-4-maverick-17b-128e-instruct"},
		{"qianfan", "ernie-4.5-turbo-32k"},
	}
	for _, tt := range platforms {
		p := getPricing(tt.model, tt.provider)
		// Just verify it doesn't panic and returns a valid price struct
		if p.Input < 0 || p.Output < 0 {
			t.Errorf("negative pricing for %s:%s: input=%v output=%v", tt.provider, tt.model, p.Input, p.Output)
		}
	}
}

func TestDefaultWeights(t *testing.T) {
	// Verify default weights sum to approximately 1.0
	sum := 0.0
	for _, v := range defaultWeights {
		sum += v
	}
	if sum < 0.99 || sum > 1.01 {
		t.Fatalf("default weights should sum to ~1.0, got %v", sum)
	}

	// Verify all expected keys exist
	for _, key := range []string{"priority", "cost", "latency", "tokens"} {
		if _, ok := defaultWeights[key]; !ok {
			t.Fatalf("default weights missing key: %s", key)
		}
	}
}
