package main

import (
	"strings"
	"testing"
)

// ============================================================
// TokenEstimate tests
// ============================================================

func TestTokenEstimate_ShouldCountTowardContribution(t *testing.T) {
	tests := []struct {
		name     string
		est      *TokenEstimate
		expected bool
	}{
		{"nil returns false", nil, false},
		{"no request id returns false", &TokenEstimate{TotalTokens: 100, HasRequestID: false}, false},
		{"zero tokens returns false", &TokenEstimate{TotalTokens: 0, HasRequestID: true}, false},
		{"valid returns true", &TokenEstimate{TotalTokens: 100, HasRequestID: true}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.est.ShouldCountTowardContribution()
			if got != tt.expected {
				t.Errorf("ShouldCountTowardContribution() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// ============================================================
// EstimateFromUpstream tests
// ============================================================

func TestEstimateFromUpstream(t *testing.T) {
	t.Run("nil usage", func(t *testing.T) {
		if est := EstimateFromUpstream(nil); est != nil {
			t.Errorf("expected nil for nil usage, got %v", est)
		}
	})

	t.Run("empty usage", func(t *testing.T) {
		if est := EstimateFromUpstream(map[string]interface{}{}); est != nil {
			t.Errorf("expected nil for empty usage, got %v", est)
		}
	})

	t.Run("valid upstream data", func(t *testing.T) {
		est := EstimateFromUpstream(map[string]interface{}{
			"prompt_tokens":     float64(100),
			"completion_tokens": float64(50),
			"total_tokens":      float64(150),
		})
		if est == nil {
			t.Fatal("expected non-nil estimate")
		}
		if est.PromptTokens != 100 {
			t.Errorf("PromptTokens = %d, want 100", est.PromptTokens)
		}
		if est.CompletionTokens != 50 {
			t.Errorf("CompletionTokens = %d, want 50", est.CompletionTokens)
		}
		if est.TotalTokens != 150 {
			t.Errorf("TotalTokens = %d, want 150", est.TotalTokens)
		}
		if est.Source != "upstream" {
			t.Errorf("Source = %s, want upstream", est.Source)
		}
	})

	t.Run("derives total when missing", func(t *testing.T) {
		est := EstimateFromUpstream(map[string]interface{}{
			"prompt_tokens":     float64(100),
			"completion_tokens": float64(50),
		})
		if est == nil {
			t.Fatal("expected non-nil estimate")
		}
		if est.TotalTokens != 150 {
			t.Errorf("TotalTokens = %d, want derived 150", est.TotalTokens)
		}
	})
}

// ============================================================
// EstimateLocally tests
// ============================================================

func TestEstimateLocally(t *testing.T) {
	t.Run("empty prompt", func(t *testing.T) {
		est := EstimateLocally("", "gpt-4")
		if est == nil {
			t.Fatal("expected non-nil estimate")
		}
		if est.PromptTokens != 0 {
			t.Errorf("PromptTokens = %d, want 0", est.PromptTokens)
		}
		if est.Source != "estimated" {
			t.Errorf("Source = %s, want estimated", est.Source)
		}
	})

	t.Run("english text", func(t *testing.T) {
		est := EstimateLocally("Hello world, this is a test", "gpt-4")
		if est == nil {
			t.Fatal("expected non-nil estimate")
		}
		if est.PromptTokens <= 0 {
			t.Errorf("PromptTokens should be positive for non-empty text, got %d", est.PromptTokens)
		}
		if est.CompletionTokens != 0 {
			t.Errorf("CompletionTokens = %d, want 0 (unknown before request)", est.CompletionTokens)
		}
	})

	t.Run("cjk text", func(t *testing.T) {
		est := EstimateLocally("你好世界这是一个测试", "gpt-4")
		if est == nil {
			t.Fatal("expected non-nil estimate")
		}
		if est.PromptTokens <= 0 {
			t.Errorf("PromptTokens should be positive for CJK text, got %d", est.PromptTokens)
		}
	})
}

// ============================================================
// estimateTokenCount tests (internal helper)
// ============================================================

func TestEstimateTokenCount(t *testing.T) {
	tests := []struct {
		name string
		text string
		min  int
		max  int
	}{
		{"empty string", "", 0, 0},
		{"short english", "hello", 1, 3},
		{"long english", strings.Repeat("hello world ", 100), 200, 500},
		{"pure cjk", "你好世界", 1, 4},
		{"mixed content", "hello世界test测试", 3, 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := estimateTokenCount(tt.text)
			if got < tt.min || got > tt.max {
				t.Errorf("estimateTokenCount(%q) = %d, want between %d and %d", tt.text, got, tt.min, tt.max)
			}
		})
	}
}

// ============================================================
// jsonIntField tests
// ============================================================

func TestJSONIntField(t *testing.T) {
	tests := []struct {
		name     string
		m        map[string]interface{}
		key      string
		expected int
	}{
		{"missing key", map[string]interface{}{}, "nope", 0},
		{"float64", map[string]interface{}{"val": float64(42)}, "val", 42},
		{"int", map[string]interface{}{"val": 42}, "val", 42},
		{"int64", map[string]interface{}{"val": int64(100)}, "val", 100},
		{"string number", map[string]interface{}{"val": "123"}, "val", 123},
		{"invalid string", map[string]interface{}{"val": "abc"}, "val", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := jsonIntField(tt.m, tt.key)
			if got != tt.expected {
				t.Errorf("jsonIntField(%v, %q) = %d, want %d", tt.m, tt.key, got, tt.expected)
			}
		})
	}
}

// ============================================================
// ExtractPromptFromMessages tests
// ============================================================

func TestExtractPromptFromMessages(t *testing.T) {
	t.Run("empty messages", func(t *testing.T) {
		got := ExtractPromptFromMessages(nil)
		if got != "" {
			t.Errorf("expected empty string for nil, got %q", got)
		}
		got = ExtractPromptFromMessages([]ChatMessage{})
		if got != "" {
			t.Errorf("expected empty string for empty slice, got %q", got)
		}
	})

	t.Run("single message", func(t *testing.T) {
		messages := []ChatMessage{
			{Role: "user", Content: "Hello"},
		}
		got := ExtractPromptFromMessages(messages)
		if !strings.Contains(got, "user") || !strings.Contains(got, "Hello") {
			t.Errorf("expected prompt to contain role and content, got %q", got)
		}
	})

	t.Run("multiple messages", func(t *testing.T) {
		messages := []ChatMessage{
			{Role: "system", Content: "Be helpful"},
			{Role: "user", Content: "Hi"},
			{Role: "assistant", Content: "Hello!"},
		}
		got := ExtractPromptFromMessages(messages)
		if !strings.Contains(got, "system") || !strings.Contains(got, "user") || !strings.Contains(got, "assistant") {
			t.Errorf("expected all roles in prompt, got %q", got)
		}
	})
}

// ============================================================
// ResolveTokenEstimate tests
// ============================================================

func TestResolveTokenEstimate_UpstreamPreferred(t *testing.T) {
	usage := map[string]interface{}{
		"prompt_tokens":     float64(200),
		"completion_tokens": float64(100),
	}

	est := ResolveTokenEstimate(usage, "some prompt text", "gpt-4", "req-123")
	if est == nil {
		t.Fatal("expected non-nil estimate")
	}
	if est.Source != "upstream" {
		t.Errorf("Source = %s, want upstream (preferred strategy)", est.Source)
	}
	if est.HasRequestID != true {
		t.Error("HasRequestID should be true when requestID provided")
	}
}

func TestResolveTokenEstimate_FallbackToLocal(t *testing.T) {
	est := ResolveTokenEstimate(nil, "fallback prompt", "gpt-4", "")
	if est == nil {
		t.Fatal("expected non-nil estimate")
	}
	if est.Source != "estimated" {
		t.Errorf("Source = %s, want estimated (fallback strategy)", est.Source)
	}
	if est.HasRequestID != false {
		t.Error("HasRequestID should be false when no requestID")
	}
}
