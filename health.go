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
	for _, p := range pm.EnabledRaw() {
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
	providers := pm.EnabledRaw()
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var healthy bool
	var latencyMS float64
	var failReason string
	var keysTested int
	var keysFailed int

	switch p.Type {
	case "coze":
		// Collect all keys to try for coze provider
		type keyEntry struct {
			alias string
			token string
		}
		var keysToTry []keyEntry
		for _, k := range p.APIKeys {
			if !k.Enabled {
				continue
			}
			decrypted, err := decryptAPIKey(k.Key)
			if err != nil {
				slog.Debug("health check: failed to decrypt coze key", "key_id", k.ID, "error", err)
				continue
			}
			keysToTry = append(keysToTry, keyEntry{alias: k.Alias, token: decrypted})
		}
		// Fallback to legacy single key or global config
		if len(keysToTry) == 0 {
			token := p.APIKey
			if token == "" {
				token = cfg.Get("coze_api_token", "")
			}
			if token != "" {
				if IsEncrypted(token) {
					if decrypted, err := decryptAPIKey(token); err == nil {
						token = decrypted
					}
				}
				keysToTry = append(keysToTry, keyEntry{alias: "default", token: token})
			}
		}
		if len(keysToTry) == 0 {
			failReason = "Coze API token not configured"
			break
		}
		baseURL := p.BaseURL
		if baseURL == "" {
			baseURL = "https://api.coze.cn"
		}
		client := proxyHTTPClient(p, 15*time.Second)
		for _, ke := range keysToTry {
			keysTested++
			reqStart := time.Now()
			req, _ := http.NewRequestWithContext(ctx, "GET", baseURL+"/v1/bots?page_index=0&page_size=1", nil)
			req.Header.Set("Authorization", "Bearer "+ke.token)
			resp, err := client.Do(req)
			if err != nil {
				failReason = ke.alias + ": " + err.Error()
				keysFailed++
				continue
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			latencyMS = float64(time.Since(reqStart).Milliseconds())
			if resp.StatusCode == 200 {
				healthy = true
				failReason = ""
				break
			}
			failReason = ke.alias + ": HTTP " + strconv.Itoa(resp.StatusCode)
			keysFailed++
		}

	default:
		// OpenAI-compatible: try all enabled keys, healthy if any succeeds
		type keyEntry struct {
			alias string
			key   string
		}
		var keysToTry []keyEntry
		for _, k := range p.APIKeys {
			if !k.Enabled {
				continue
			}
			decrypted, err := decryptAPIKey(k.Key)
			if err != nil {
				slog.Debug("health check: failed to decrypt key", "key_id", k.ID, "error", err)
				continue
			}
			keysToTry = append(keysToTry, keyEntry{alias: k.Alias, key: decrypted})
		}
		// Fallback to legacy single key
		if len(keysToTry) == 0 && p.APIKey != "" {
			apiKey := p.APIKey
			if IsEncrypted(apiKey) {
				if decrypted, err := decryptAPIKey(apiKey); err == nil {
					apiKey = decrypted
				}
			}
			keysToTry = append(keysToTry, keyEntry{alias: "default", key: apiKey})
		}
		if len(keysToTry) == 0 {
			failReason = "no API key"
			break
		}
		client := proxyHTTPClient(p, 15*time.Second)

		// Determine health check endpoint
		hcEndpoint := p.HealthCheckEndpoint
		if hcEndpoint == "" {
			hcEndpoint = "/models"
		}

		// Try each key until one succeeds
		for _, ke := range keysToTry {
			keysTested++
			reqStart := time.Now()

			// Primary health check: GET /models
			req, _ := http.NewRequestWithContext(ctx, "GET", p.BaseURL+hcEndpoint, nil)
			req.Header.Set("Authorization", "Bearer "+ke.key)
			resp, err := client.Do(req)
			if err != nil {
				failReason = ke.alias + ": " + err.Error()
				keysFailed++
				continue
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			latencyMS = float64(time.Since(reqStart).Milliseconds())

			if resp.StatusCode == 200 {
				healthy = true
				failReason = ""
				break
			}

			// Fallback: if /models returned error, try /chat/completions probe
			if (resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 404 || resp.StatusCode == 405) && (hcEndpoint == "/models" || hcEndpoint == "") {
				slog.Debug("health check /models non-standard response, falling back to /chat/completions probe", "provider", p.ID, "key", ke.alias)
				var probeModels []string
				for _, m := range p.Models {
					if m.Enabled {
						probeModels = append(probeModels, m.ID)
					}
				}
				probeModels = append(probeModels, "gpt-3.5-turbo", "@cf/meta/llama-3-8b-instruct", "@cf/mistral/mistral-7b-instruct-v0.1", "@cf/tinyllama/tinyllama-1.1b-chat-v1.0")
				probeOK := false
				for _, model := range probeModels {
					probeBody, _ := json.Marshal(map[string]any{
						"model":      model,
						"max_tokens": 1,
						"messages":   []map[string]string{{"role": "user", "content": "hi"}},
					})
					probeReq, _ := http.NewRequestWithContext(ctx, "POST", p.BaseURL+"/chat/completions", bytes.NewReader(probeBody))
					probeReq.Header.Set("Authorization", "Bearer "+ke.key)
					probeReq.Header.Set("Content-Type", "application/json")
					probeStart := time.Now()
					probeResp, err := client.Do(probeReq)
					if err != nil {
						continue
					}
					io.Copy(io.Discard, probeResp.Body)
					probeResp.Body.Close()
					latencyMS = float64(time.Since(probeStart).Milliseconds())
					if probeResp.StatusCode == 200 {
						healthy = true
						failReason = ""
						probeOK = true
						break
					}
					if probeResp.StatusCode == 401 {
						break // key is truly invalid, stop trying models
					}
				}
				if healthy {
					break
				}
				if !probeOK {
					failReason = ke.alias + ": HTTP " + strconv.Itoa(resp.StatusCode)
					keysFailed++
				}
			} else {
				failReason = ke.alias + ": HTTP " + strconv.Itoa(resp.StatusCode)
				keysFailed++
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

	// Log multi-key summary
	if keysTested > 1 {
		slog.Info("multi-key health check", "provider", p.ID, "keys_tested", keysTested, "keys_failed", keysFailed, "healthy", healthy)
	}

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
