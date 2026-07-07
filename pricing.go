package main

import "strings"

// Model pricing per million tokens (USD).
// PLATFORM_PRICING: "provider:model" → Price (highest priority)
// MODEL_PRICING: "model" → Price (fallback)

var platformPricing = map[string]Price{
	// Coze (subscription, estimated)
	"coze:gpt-4o":        {0, 0},
	"coze:deepseek-chat": {0, 0},
	// Sider (subscription)
	"sider:gpt-4o":            {0, 0},
	"sider:claude-3-5-sonnet": {0, 0},
	"sider:gemini-2.5-pro":    {0, 0},
	// Tencent Hunyuan TokenHub
	"hunyuan:hy3-preview":           {0.80, 3.20},
	"hunyuan:hunyuan-turbos-latest": {0.30, 1.20},
	"hunyuan:deepseek-v3":           {0.27, 1.10},
	"hunyuan:deepseek-r1":           {0.55, 2.19},
	"hunyuan:hunyuan-lite":          {0, 0},
}

var modelPricing = map[string]Price{
	// OpenAI
	"gpt-4o":            {2.50, 10.00},
	"gpt-4o-mini":       {0.15, 0.60},
	"gpt-4o-2024-08-06": {2.50, 10.00},
	"gpt-4o-2024-11-20": {2.50, 10.00},
	"o1":                {15.00, 60.00},
	"o1-preview":        {15.00, 60.00},
	"o1-mini":           {3.00, 12.00},
	"o3":                {2.00, 8.00},
	"o3-mini":           {1.10, 4.40},
	"o4-mini":           {1.10, 4.40},
	// Anthropic
	"claude-3-5-sonnet": {3.00, 15.00},
	"claude-3-opus":     {15.00, 75.00},
	"claude-3-haiku":    {0.25, 1.25},
	// DeepSeek
	"deepseek-chat":     {0.27, 1.10},
	"deepseek-reasoner": {0.55, 2.19},
	// Qwen
	"qwen-turbo": {0.30, 0.60},
	"qwen-plus":  {0.80, 2.00},
	"qwen-max":   {2.00, 6.00},
	"qwen-long":  {0.50, 2.00},
	// Zhipu
	"glm-4-plus":  {5.00, 5.00},
	"glm-4":       {10.00, 10.00},
	"glm-4-flash": {0, 0},
	"glm-4v-plus": {10.00, 10.00},
	// Moonshot
	"moonshot-v1-8k":   {12.00, 12.00},
	"moonshot-v1-32k":  {24.00, 24.00},
	"moonshot-v1-128k": {60.00, 60.00},
	// Yi
	"yi-large":       {3.00, 3.00},
	"yi-medium":      {1.00, 1.00},
	"yi-vision":      {6.00, 6.00},
	"yi-large-turbo": {2.00, 2.00},
	// MiniMax
	"MiniMax-Text-01": {1.00, 1.00},
	"abab6.5s-chat":   {1.00, 1.00},
	// SiliconFlow
	"Qwen/Qwen2.5-72B-Instruct":         {0.40, 0.40},
	"deepseek-ai/DeepSeek-V3":           {0.27, 1.10},
	"deepseek-ai/DeepSeek-R1":           {0.55, 2.19},
	"meta-llama/Llama-3.3-70B-Instruct": {0.40, 0.40},
	// Groq
	"llama-3.3-70b-versatile": {0.59, 0.79},
	"llama-3.1-8b-instant":    {0.05, 0.08},
	"mixtral-8x7b-32768":      {0.24, 0.24},
	"gemma2-9b-it":            {0.20, 0.20},
	// xAI
	"grok-2-latest": {2.00, 10.00},
	"grok-3":        {3.00, 15.00},
	"grok-3-mini":   {0.30, 0.50},
	// Together
	"meta-llama/Llama-3.3-70B-Instruct-Turbo": {0.88, 0.88},
	// Mistral
	"mistral-large-latest": {2.00, 6.00},
	"mistral-small-latest": {0.20, 0.60},
	"codestral-latest":     {0.30, 0.90},
	"open-mistral-nemo":    {0.30, 0.30},
	// Doubao
	"doubao-pro-32k":  {0.80, 2.00},
	"doubao-lite-32k": {0.30, 0.60},
	// Spark
	"general":    {0, 0},
	"generalv3.5": {0, 0},
	"generalv4":  {0, 0},
	// Gemini
	"gemini-2.5-pro":        {1.25, 10.00},
	"gemini-2.5-flash":      {0.15, 0.60},
	"gemini-2.0-flash":      {0.10, 0.40},
	"gemini-2.0-flash-lite": {0.075, 0.30},
	"gemini-1.5-pro":        {1.25, 5.00},
	"gemini-1.5-flash":      {0.075, 0.30},
	// Hunyuan
	"hy3-preview":           {0.80, 3.20},
	"hunyuan-turbos-latest": {0.30, 1.20},
	"hunyuan-lite":          {0, 0},
}

// Default weights for auto routing mode (4 dimensions).
var defaultWeights = map[string]float64{
	"priority": 0.4,
	"cost":     0.25,
	"latency":  0.2,
	"tokens":   0.15,
}

// getPricing returns pricing for a model+provider combo.
func getPricing(model, providerID string) Price {
	// 1. Platform-specific
	if providerID != "" {
		if p, ok := platformPricing[providerID+":"+model]; ok {
			return p
		}
	}
	// 2. Model-level
	if p, ok := modelPricing[model]; ok {
		return p
	}
	// 3. Fuzzy match (strip version suffix)
	if idx := strings.LastIndex(model, "-"); idx > 0 {
		base := model[:idx]
		for k, v := range modelPricing {
			if strings.HasPrefix(k, base) || strings.HasPrefix(base, k) {
				return v
			}
		}
	}
	return Price{}
}

// estimateCost calculates USD cost for a request.
func estimateCost(model string, promptTokens, completionTokens int, providerID string) float64 {
	p := getPricing(model, providerID)
	input := float64(promptTokens) / 1e6 * p.Input
	output := float64(completionTokens) / 1e6 * p.Output
	return roundTo6(input + output)
}

func roundTo6(f float64) float64 {
	return float64(int(f*1e6+0.5)) / 1e6
}
