package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// HealthChecker periodically probes enabled providers and tracks their status.
type HealthChecker struct {
	mu       sync.RWMutex
	statuses map[string]*ProviderHealth
	interval time.Duration
	stopCh   chan struct{}
}

var healthChecker *HealthChecker

func initHealthChecker(interval time.Duration) {
	healthChecker = &HealthChecker{
		statuses: make(map[string]*ProviderHealth),
		interval: interval,
		stopCh:   make(chan struct{}),
	}
	// Initialize statuses for all providers
	for _, p := range pm.Enabled() {
		healthChecker.statuses[p.ID] = &ProviderHealth{
			ProviderID:   p.ID,
			ProviderName: p.Name,
			Status:       "unknown",
		}
	}
	go healthChecker.run()
	slog.Info("health checker started", "interval", interval)
}

func (h *HealthChecker) run() {
	// Run immediately on start
	h.checkAll()

	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			h.checkAll()
		case <-h.stopCh:
			return
		}
	}
}

func (h *HealthChecker) checkAll() {
	providers := pm.Enabled()
	// Update statuses map for new providers
	h.mu.Lock()
	for _, p := range providers {
		if _, ok := h.statuses[p.ID]; !ok {
			h.statuses[p.ID] = &ProviderHealth{
				ProviderID:   p.ID,
				ProviderName: p.Name,
				Status:       "unknown",
			}
		}
	}
	h.mu.Unlock()

	var wg sync.WaitGroup
	for _, p := range providers {
		wg.Add(1)
		go func(p Provider) {
			defer wg.Done()
			h.checkProvider(p)
		}(p)
	}
	wg.Wait()
}

func (h *HealthChecker) checkProvider(p Provider) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	start := time.Now()
	var healthy bool
	var latencyMS float64
	var failReason string

	switch p.Type {
	case "coze":
		// Check Coze API token
		token := cfg.Get("coze_api_token", "")
		if token == "" {
			failReason = "Coze API token not configured"
			break
		}
		baseURL := p.BaseURL
		if baseURL == "" {
			baseURL = "https://api.coze.cn"
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", baseURL+"/v1/bots?page_index=0&page_size=1", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		client := proxyHTTPClient(p, 15*time.Second)
		resp, err := client.Do(req)
		if err != nil {
			failReason = err.Error()
			break
		}
		resp.Body.Close()
		latencyMS = float64(time.Since(start).Milliseconds())
		healthy = resp.StatusCode == 200
		if !healthy {
			failReason = "HTTP " + strconv.Itoa(resp.StatusCode)
		}

	default:
		// OpenAI-compatible: check via /models or fallback endpoint
		if p.APIKey == "" {
			failReason = "no API key"
			break
		}
		client := proxyHTTPClient(p, 15*time.Second)

		// Determine health check endpoint
		hcEndpoint := p.HealthCheckEndpoint
		if hcEndpoint == "" {
			hcEndpoint = "/models"
		}

		// Primary health check
		req, _ := http.NewRequestWithContext(ctx, "GET", p.BaseURL+hcEndpoint, nil)
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
		resp, err := client.Do(req)
		if err != nil {
			failReason = err.Error()
			break
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		latencyMS = float64(time.Since(start).Milliseconds())
		healthy = resp.StatusCode == 200
		if !healthy {
			failReason = "HTTP " + strconv.Itoa(resp.StatusCode)
		}

		// Fallback: if /models returned 404, try lightweight /chat/completions probe
		// This handles providers like TokenHub that don't support /models
		if !healthy && resp.StatusCode == 404 && (hcEndpoint == "/models" || hcEndpoint == "") {
			slog.Debug("health check /models 404, falling back to /chat/completions probe", "provider", p.ID)
			probeBody, _ := json.Marshal(map[string]any{
				"model":       "gpt-4o-mini",
				"max_tokens":  1,
				"messages":    []map[string]string{{"role": "user", "content": "hi"}},
			})
			probeReq, _ := http.NewRequestWithContext(ctx, "POST", p.BaseURL+"/chat/completions", bytes.NewReader(probeBody))
			probeReq.Header.Set("Authorization", "Bearer "+p.APIKey)
			probeReq.Header.Set("Content-Type", "application/json")
			start = time.Now()
			probeResp, err := client.Do(probeReq)
			if err != nil {
				failReason = err.Error()
				break
			}
			io.Copy(io.Discard, probeResp.Body)
			probeResp.Body.Close()
			latencyMS = float64(time.Since(start).Milliseconds())
			// 200 from chat/completions means provider is healthy
			healthy = probeResp.StatusCode == 200
			if healthy {
				failReason = ""
			} else {
				failReason = "HTTP " + strconv.Itoa(probeResp.StatusCode)
			}
		}
	}

	now := time.Now().Format(time.RFC3339)
	h.mu.Lock()
	defer h.mu.Unlock()

	hs, ok := h.statuses[p.ID]
	if !ok {
		hs = &ProviderHealth{ProviderID: p.ID, ProviderName: p.Name}
		h.statuses[p.ID] = hs
	}

	hs.LastCheck = now
	hs.LatencyMS = latencyMS

	if healthy {
		hs.Status = "healthy"
		hs.ConsecutiveFails = 0
		hs.LastSuccess = now
		hs.FailureReason = ""
	} else {
		hs.ConsecutiveFails++
		if hs.ConsecutiveFails >= 3 {
			hs.Status = "down"
		} else {
			hs.Status = "degraded"
		}
		hs.LastFailure = now
		hs.FailureReason = failReason
		slog.Warn("provider health check failed", "provider", p.ID, "reason", failReason, "consecutive_fails", hs.ConsecutiveFails)
	}
}

// GetHealth returns a snapshot of all provider health statuses.
func (h *HealthChecker) GetHealth() []ProviderHealth {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make([]ProviderHealth, 0, len(h.statuses))
	for _, hs := range h.statuses {
		result = append(result, *hs)
	}
	return result
}

// IsHealthy returns whether a provider is currently healthy.
func (h *HealthChecker) IsHealthy(providerID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	hs, ok := h.statuses[providerID]
	if !ok {
		return true // unknown → assume healthy
	}
	return hs.Status != "down"
}

func (h *HealthChecker) stop() {
	close(h.stopCh)
}
