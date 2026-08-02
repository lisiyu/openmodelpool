package main

import (
	"encoding/json"
	"fmt"
)

// RequestIR is the internal unified intermediate representation for AI model
// requests, per v4 design §4. Enables N+M conversion (N providers + M consumers)
// instead of N×M direct conversion.
type RequestIR struct {
	Model        string     `json:"model"`
	Messages     []MessageIR `json:"messages"`
	SystemPrompt string     `json:"system_prompt,omitempty"`
	Temperature  *float64   `json:"temperature,omitempty"`
	MaxTokens    *int       `json:"max_tokens,omitempty"`
	Stream       bool       `json:"stream"`
	Tools        []ToolIR   `json:"tools,omitempty"`
}

// MessageIR is the unified message representation.
type MessageIR struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ToolIR is the unified tool/function definition.
type ToolIR struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters,omitempty"`
}

// TokenUsage reports token consumption from a response.
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// OpenAIResponse represents a standard OpenAI-format response for normalization.
type OpenAIResponse struct {
	ID      string   `json:"id,omitempty"`
	Object  string   `json:"object,omitempty"`
	Created int64    `json:"created,omitempty"`
	Model   string   `json:"model,omitempty"`
	Choices []any    `json:"choices,omitempty"`
	Usage   *TokenUsage `json:"usage,omitempty"`
}

// OpenAIStreamChunk represents a single streaming chunk in OpenAI format.
type OpenAIStreamChunk struct {
	ID      string `json:"id,omitempty"`
	Object  string `json:"object,omitempty"`
	Created int64  `json:"created,omitempty"`
	Model   string `json:"model,omitempty"`
	Choices []any  `json:"choices,omitempty"`
}

// PlatformAdapter is the conversion interface per v4 design §4.
// Each AI platform implements this to translate between its native format
// and the unified IR.
type PlatformAdapter interface {
	PlatformName() string
	TranslateRequest(ir *RequestIR) ([]byte, error)
	TranslateResponse(raw []byte) (*OpenAIResponse, error)
	TranslateStreamChunk(raw []byte) (*OpenAIStreamChunk, error)
	ExtractUsage(raw []byte) (*TokenUsage, error)
}

// adapterRegistry maps provider types to their PlatformAdapter implementations.
var adapterRegistry = map[string]PlatformAdapter{
	"openai_compatible": &OpenAIAdapter{},
	"anthropic":         &AnthropicAdapter{},
}

// GetAdapter returns the PlatformAdapter for a given provider type.
func GetAdapter(providerType string) PlatformAdapter {
	if a, ok := adapterRegistry[providerType]; ok {
		return a
	}
	return adapterRegistry["openai_compatible"]
}

// RegisterAdapter adds a new PlatformAdapter to the registry.
func RegisterAdapter(providerType string, adapter PlatformAdapter) {
	adapterRegistry[providerType] = adapter
}

// ParseRequestToIR parses an OpenAI-format request body into RequestIR.
func ParseRequestToIR(body []byte) (*RequestIR, error) {
	var raw struct {
		Model       string          `json:"model"`
		Messages    []MessageIR     `json:"messages"`
		Temperature *float64        `json:"temperature"`
		MaxTokens   *int            `json:"max_tokens"`
		Stream      bool            `json:"stream"`
		Tools       json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("cannot parse request: %w", err)
	}

	ir := &RequestIR{
		Model:       raw.Model,
		Messages:    raw.Messages,
		Temperature: raw.Temperature,
		MaxTokens:   raw.MaxTokens,
		Stream:      raw.Stream,
	}

	for _, m := range raw.Messages {
		if m.Role == "system" {
			ir.SystemPrompt = m.Content
		}
	}

	if raw.Tools != nil {
		var tools []ToolIR
		if err := json.Unmarshal(raw.Tools, &tools); err == nil {
			ir.Tools = tools
		}
	}

	return ir, nil
}

// ============================================================
// OpenAI Adapter (native — minimal conversion)
// ============================================================

// OpenAIAdapter handles OpenAI-compatible providers (native format).
type OpenAIAdapter struct{}

func (a *OpenAIAdapter) PlatformName() string { return "openai_compatible" }

func (a *OpenAIAdapter) TranslateRequest(ir *RequestIR) ([]byte, error) {
	req := map[string]any{
		"model":    ir.Model,
		"messages": ir.Messages,
		"stream":   ir.Stream,
	}
	if ir.Temperature != nil {
		req["temperature"] = *ir.Temperature
	}
	if ir.MaxTokens != nil {
		req["max_tokens"] = *ir.MaxTokens
	}
	if len(ir.Tools) > 0 {
		req["tools"] = ir.Tools
	}
	return json.Marshal(req)
}

func (a *OpenAIAdapter) TranslateResponse(raw []byte) (*OpenAIResponse, error) {
	var resp OpenAIResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("cannot parse openai response: %w", err)
	}
	return &resp, nil
}

func (a *OpenAIAdapter) TranslateStreamChunk(raw []byte) (*OpenAIStreamChunk, error) {
	var chunk OpenAIStreamChunk
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return nil, fmt.Errorf("cannot parse openai stream chunk: %w", err)
	}
	return &chunk, nil
}

func (a *OpenAIAdapter) ExtractUsage(raw []byte) (*TokenUsage, error) {
	var resp struct {
		Usage *TokenUsage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("cannot extract usage: %w", err)
	}
	return resp.Usage, nil
}

// ============================================================
// Anthropic Adapter
// ============================================================

// AnthropicAdapter handles Anthropic-specific format conversion.
// Key differences from OpenAI: system prompt is a top-level parameter,
// messages use the same roles but system is extracted separately.
type AnthropicAdapter struct{}

func (a *AnthropicAdapter) PlatformName() string { return "anthropic" }

func (a *AnthropicAdapter) TranslateRequest(ir *RequestIR) ([]byte, error) {
	var messages []MessageIR
	for _, m := range ir.Messages {
		if m.Role == "system" {
			continue
		}
		messages = append(messages, m)
	}

	req := map[string]any{
		"model":    ir.Model,
		"messages": messages,
		"stream":   ir.Stream,
	}
	if ir.SystemPrompt != "" {
		req["system"] = ir.SystemPrompt
	}
	if ir.Temperature != nil {
		req["temperature"] = *ir.Temperature
	}
	if ir.MaxTokens != nil {
		req["max_tokens"] = *ir.MaxTokens
	}
	return json.Marshal(req)
}

func (a *AnthropicAdapter) TranslateResponse(raw []byte) (*OpenAIResponse, error) {
	var resp OpenAIResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("cannot parse anthropic response: %w", err)
	}
	return &resp, nil
}

func (a *AnthropicAdapter) TranslateStreamChunk(raw []byte) (*OpenAIStreamChunk, error) {
	var chunk OpenAIStreamChunk
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return nil, fmt.Errorf("cannot parse anthropic stream chunk: %w", err)
	}
	return &chunk, nil
}

func (a *AnthropicAdapter) ExtractUsage(raw []byte) (*TokenUsage, error) {
	var resp struct {
		Usage *TokenUsage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("cannot extract anthropic usage: %w", err)
	}
	return resp.Usage, nil
}
