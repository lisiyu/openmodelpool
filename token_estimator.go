package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"unicode"
)

// ============================================================
// §6.1A Token Precise Estimation — Dual-Strategy
// ============================================================
//
// Token estimation uses a dual strategy:
//   1. Upstream usage (preferred): extract from upstream API response usage field
//   2. Local Tokenizer (fallback): estimate via character counting when upstream unavailable
//   3. No request-id → not counted toward contribution points

// TokenEstimate holds the result of token estimation.
type TokenEstimate struct {
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens      int    `json:"total_tokens"`
	Source           string `json:"source"`         // "upstream" / "estimated"
	HasRequestID     bool   `json:"has_request_id"` // whether request carried a valid request-id
}

// EstimateFromUpstream extracts token counts from upstream API response usage field.
// Returns nil if usage is nil or doesn't contain valid token data.
func EstimateFromUpstream(usage map[string]interface{}) *TokenEstimate {
	if usage == nil {
		return nil
	}

	promptTokens := jsonIntField(usage, "prompt_tokens")
	completionTokens := jsonIntField(usage, "completion_tokens")
	totalTokens := jsonIntField(usage, "total_tokens")

	// At minimum, we need some non-zero token data to consider this valid
	if promptTokens == 0 && completionTokens == 0 && totalTokens == 0 {
		return nil
	}

	// Derive total if not provided
	if totalTokens == 0 {
		totalTokens = promptTokens + completionTokens
	}

	return &TokenEstimate{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
		Source:           "upstream",
		HasRequestID:     true, // caller should set this explicitly
	}
}

// EstimateLocally performs a local token estimation using a simplified character-based heuristic.
// Rule of thumb: ~1 token per 4 characters for English; ~1 token per 2 characters for CJK.
// This is a conservative over-estimate to avoid under-charging.
func EstimateLocally(prompt string, model string) *TokenEstimate {
	promptTokens := estimateTokenCount(prompt)
	return &TokenEstimate{
		PromptTokens:     promptTokens,
		CompletionTokens: 0, // can't know completion before the request
		TotalTokens:      promptTokens,
		Source:           "estimated",
		HasRequestID:     false, // caller should set this explicitly
	}
}

// ShouldCountTowardContribution returns whether this estimate should count
// toward the contribution/credits system. Calls without a request-id are
// still forwarded to users but do not generate a valid contribution ticket.
func (t *TokenEstimate) ShouldCountTowardContribution() bool {
	if t == nil {
		return false
	}
	return t.HasRequestID && t.TotalTokens > 0
}

// ResolveTokenEstimate implements the dual-strategy priority:
//  1. Try upstream usage first
//  2. Fall back to local estimation
//  3. Check for request-id presence
func ResolveTokenEstimate(usage map[string]interface{}, prompt string, model string, requestID string) *TokenEstimate {
	// Strategy 1: Upstream usage (preferred)
	if est := EstimateFromUpstream(usage); est != nil {
		est.HasRequestID = requestID != ""
		slog.Debug("token estimate from upstream",
			"prompt_tokens", est.PromptTokens,
			"completion_tokens", est.CompletionTokens,
			"total_tokens", est.TotalTokens,
			"has_request_id", est.HasRequestID,
		)
		return est
	}

	// Strategy 2: Local estimation (fallback)
	est := EstimateLocally(prompt, model)
	est.HasRequestID = requestID != ""

	slog.Debug("token estimate locally",
		"prompt_tokens", est.PromptTokens,
		"model", model,
		"has_request_id", est.HasRequestID,
	)
	return est
}

// ============================================================
// Internal helpers
// ============================================================

// estimateTokenCount uses a heuristic to estimate token count from text.
// English: ~4 chars per token; CJK: ~2 chars per token.
// This is intentionally conservative (over-estimates) to avoid under-charging.
func estimateTokenCount(text string) int {
	if text == "" {
		return 0
	}

	cjkChars := 0
	asciiChars := 0
	for _, r := range text {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hangul, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hiragana, r) {
			cjkChars++
		} else {
			asciiChars++
		}
	}

	// CJK: ~2 chars per token; ASCII: ~4 chars per token
	cjkTokens := (cjkChars + 1) / 2
	asciiTokens := (asciiChars + 3) / 4

	return cjkTokens + asciiTokens
}

// jsonIntField safely extracts an int from a map[string]interface{}.
func jsonIntField(m map[string]interface{}, key string) int {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0
		}
		return int(i)
	case string:
		var parsed int
		if _, err := fmt.Sscanf(n, "%d", &parsed); err == nil {
			return parsed
		}
	}
	return 0
}

// ExtractPromptFromMessages concatenates chat messages into a single prompt string
// for local token estimation when upstream usage is unavailable.
func ExtractPromptFromMessages(messages []ChatMessage) string {
	if len(messages) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, msg := range messages {
		sb.WriteString(msg.Role)
		sb.WriteString(": ")
		sb.WriteString(msg.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}
