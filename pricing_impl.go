package main

import (
	"math"
	"strings"
)

// Pricing holds per-token (per 1M tokens) input/output prices in USD.
type Pricing struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

// platformPricing maps provider -> model -> {input, output} price (USD / 1M tokens).
var platformPricing = map[string]map[string][2]float64{
	"anthropic": {
		"claude-sonnet-4-20250514": {3.00, 15.00},
		"claude-3-5-sonnet":        {3.00, 15.00},
		"claude-3-haiku-20240307":  {0.25, 1.25},
	},
	"deepseek": {
		"deepseek-chat":    {0.27, 1.10},
		"deepseek-reasoner": {0.55, 2.19},
	},
	"openai": {
		"gpt-4o": {2.50, 10.00},
	},
	"coze": {
		"gpt-4o":       {2.50, 10.00},
		"deepseek-chat": {0.27, 1.10},
	},
	"nvidia": {
		"meta/llama-4-maverick-17b-128e-instruct": {0.0009, 0.0009},
	},
	"qianfan": {
		"ernie-4.5-turbo-32k": {0.0008, 0.0008},
	},
	// Subscription platforms are free.
	"sider": {},
}

// modelPricing is the model-level fallback used when no platform match is found.
var modelPricing = map[string][2]float64{
	"gpt-4o":            {2.50, 10.00},
	"claude-3-5-sonnet": {3.00, 15.00},
	"deepseek-chat":     {0.27, 1.10},
	"deepseek-reasoner": {0.55, 2.19},
}

// getPricing returns the pricing for a model on a given platform. Subscription
// platforms are free; unknown models return zero pricing.
func getPricing(model, provider string) Pricing {
	if pm, ok := platformPricing[provider]; ok {
		if p, ok := pm[model]; ok {
			return Pricing{Input: p[0], Output: p[1]}
		}
		// fuzzy match by model prefix within this provider
		for m, p := range pm {
			if strings.HasPrefix(model, m) {
				return Pricing{Input: p[0], Output: p[1]}
			}
		}
	}
	if provider == "sider" || provider == "coze" {
		return Pricing{Input: 0, Output: 0}
	}
	if p, ok := modelPricing[model]; ok {
		return Pricing{Input: p[0], Output: p[1]}
	}
	for m, p := range modelPricing {
		if strings.HasPrefix(model, m) {
			return Pricing{Input: p[0], Output: p[1]}
		}
	}
	return Pricing{Input: 0, Output: 0}
}

// roundTo6 rounds a float to 6 decimal places.
func roundTo6(x float64) float64 {
	return math.Round(x*1e6) / 1e6
}

// estimateCost estimates the USD cost of a request.
func estimateCost(model string, promptTokens, completionTokens int, provider string) float64 {
	p := getPricing(model, provider)
	if p.Input == 0 && p.Output == 0 {
		return 0
	}
	in := float64(promptTokens) / 1e6 * p.Input
	out := float64(completionTokens) / 1e6 * p.Output
	return roundTo6(in + out)
}

// defaultWeights are the default composite routing weights (priority/cost/latency/tokens).
var defaultWeights = map[string]float64{
	"priority": 0.40,
	"cost":     0.25,
	"latency":  0.20,
	"tokens":   0.15,
}
