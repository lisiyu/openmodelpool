package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DiscoveredPlatform represents a platform found during scanning.
type DiscoveredPlatform struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	BaseURL      string   `json:"base_url"`
	Description  string   `json:"description"`
	APIKeyURL    string   `json:"api_key_url"`
	Models       []string `json:"models,omitempty"`
	Source       string   `json:"source"`
	Status       string   `json:"status"` // "new", "reviewed", "dismissed", "added"
	DiscoveredAt string   `json:"discovered_at"`
}

var (
	discoveredMu        sync.RWMutex
	discoveredPlatforms []DiscoveredPlatform
	discoveryRunning    bool
)

const discoveredPlatformsFile = "data/discovered_platforms.json"

func loadDiscoveredPlatforms() []DiscoveredPlatform {
	discoveredMu.RLock()
	if discoveredPlatforms != nil {
		result := discoveredPlatforms
		discoveredMu.RUnlock()
		return result
	}
	discoveredMu.RUnlock()

	discoveredMu.Lock()
	defer discoveredMu.Unlock()

	data, err := os.ReadFile(discoveredPlatformsFile)
	if err != nil {
		discoveredPlatforms = []DiscoveredPlatform{}
		os.WriteFile(discoveredPlatformsFile, []byte("[]"), 0644)
		return discoveredPlatforms
	}

	var platforms []DiscoveredPlatform
	if err := json.Unmarshal(data, &platforms); err != nil {
		slog.Warn("failed to parse discovered platforms", "error", err)
		discoveredPlatforms = []DiscoveredPlatform{}
		return discoveredPlatforms
	}
	discoveredPlatforms = platforms
	return discoveredPlatforms
}

func saveDiscoveredPlatforms() error {
	discoveredMu.RLock()
	data, err := json.MarshalIndent(discoveredPlatforms, "", "  ")
	discoveredMu.RUnlock()
	if err != nil {
		return fmt.Errorf("marshal discovered platforms: %w", err)
	}

	dir := filepath.Dir(discoveredPlatformsFile)
	os.MkdirAll(dir, 0755)
	return os.WriteFile(discoveredPlatformsFile, data, 0644)
}

// handleGetDiscoveredPlatforms returns all discovered platforms.
func handleGetDiscoveredPlatforms(w http.ResponseWriter, r *http.Request) {
	platforms := loadDiscoveredPlatforms()
	// Build set of existing provider IDs (configured + presets)
	existingIDs := make(map[string]bool)
	for _, p := range presetProviders {
		existingIDs[p.ID] = true
	}
	for _, p := range pm.GetAllRaw() {
		existingIDs[p.ID] = true
	}
	// Filter out already-added platforms (exact + partial ID match)
	filtered := make([]DiscoveredPlatform, 0)
	for _, p := range platforms {
		if p.Status == "added" || existingIDs[p.ID] {
			if p.Status != "added" {
				p.Status = "added"
			}
			continue
		}
		// Partial match: e.g. "openrouter-free" should be filtered if "openrouter" exists
		baseID := p.ID
		if idx := strings.LastIndex(baseID, "-"); idx > 0 {
			baseID = baseID[:idx]
		}
		skip := false
		for eid := range existingIDs {
			if eid == baseID || strings.HasPrefix(p.ID, eid+"-") || strings.HasPrefix(eid, baseID) {
				skip = true
				break
			}
		}
		if skip {
			if p.Status != "added" {
				p.Status = "added"
			}
			continue
		}
		filtered = append(filtered, p)
	}
	writeJSON(w, 200, map[string]any{
		"platforms": filtered,
		"count":     len(filtered),
	})
}

// handleUpdateDiscoveredPlatform updates the status of a discovered platform.
func handleUpdateDiscoveredPlatform(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/discovered-platforms/")
	id := strings.TrimSpace(path)
	if id == "" {
		writeError(w, 400, "platform ID required")
		return
	}

	var req struct {
		Status string `json:"status"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	validStatuses := map[string]bool{"reviewed": true, "dismissed": true, "added": true}
	if !validStatuses[req.Status] {
		writeError(w, 400, "status must be one of: reviewed, dismissed, added")
		return
	}

	platforms := loadDiscoveredPlatforms()
	found := false
	for i, p := range platforms {
		if p.ID == id {
			platforms[i].Status = req.Status
			found = true
			break
		}
	}
	if !found {
		writeError(w, 404, "platform not found")
		return
	}

	discoveredMu.Lock()
	discoveredPlatforms = platforms
	discoveredMu.Unlock()

	if err := saveDiscoveredPlatforms(); err != nil {
		slog.Error("failed to save discovered platforms", "error", err)
		writeError(w, 500, "failed to save")
		return
	}

	writeJSON(w, 200, map[string]any{"status": "ok", "id": id, "new_status": req.Status})
}

// handleTriggerDiscovery triggers a background scan for new platforms.
func handleTriggerDiscovery(w http.ResponseWriter, r *http.Request) {
	if discoveryRunning {
		writeJSON(w, 200, map[string]any{"status": "already_running", "message": "\u626b\u63cf\u5df2\u5728\u8fdb\u884c\u4e2d"})
		return
	}

	go runDiscovery()
	writeJSON(w, 200, map[string]any{"status": "started", "message": "\u626b\u63cf\u5df2\u542f\u52a8\uff0c\u5b8c\u6210\u540e\u5c06\u81ea\u52a8\u4fdd\u5b58"})
}

// runDiscovery scans known sources for free AI platforms.
func runDiscovery() {
	discoveryRunning = true
	defer func() { discoveryRunning = false }()

	slog.Info("platform discovery scan started")
	start := time.Now()

	var newPlatforms []DiscoveredPlatform
	newPlatforms = append(newPlatforms, getKnownFreePlatforms()...)
	newPlatforms = append(newPlatforms, fetchGitHubLists()...)

	existingIDs := make(map[string]bool)
	for _, p := range presetProviders {
		existingIDs[p.ID] = true
	}
	for _, p := range loadDiscoveredPlatforms() {
		existingIDs[p.ID] = true
	}
	// Also check actual configured providers - don't re-discover what's already added
	if pm != nil {
		for _, p := range pm.GetAllRaw() {
			existingIDs[p.ID] = true
		}
	}

	var filtered []DiscoveredPlatform
	for _, p := range newPlatforms {
		if !existingIDs[p.ID] {
			existingIDs[p.ID] = true
			filtered = append(filtered, p)
		}
	}

	if len(filtered) > 0 {
		platforms := loadDiscoveredPlatforms()
		platforms = append(platforms, filtered...)
		discoveredMu.Lock()
		discoveredPlatforms = platforms
		discoveredMu.Unlock()

		if err := saveDiscoveredPlatforms(); err != nil {
			slog.Error("failed to save discovered platforms", "error", err)
		}
	}

	slog.Info("platform discovery scan completed",
		"new_found", len(filtered),
		"total_scanned", len(newPlatforms),
		"duration", time.Since(start))
}

// getKnownFreePlatforms returns a hardcoded list of known free AI platforms.
func getKnownFreePlatforms() []DiscoveredPlatform {
	now := time.Now().Format(time.RFC3339)
	return []DiscoveredPlatform{
		{
			ID: "cloudflare-workers-ai", Name: "Cloudflare Workers AI",
			BaseURL:     "https://api.cloudflare.com/client/v4/accounts/{ACCOUNT_ID}/ai/v1",
			Description: "Cloudflare \u63d0\u4f9b\u7684 AI \u63a8\u7406\u670d\u52a1\uff0c\u652f\u6301\u591a\u79cd\u5f00\u6e90\u6a21\u578b\uff0c\u6bcf\u5929\u6709\u514d\u8d39\u989d\u5ea6",
			APIKeyURL:   "https://dash.cloudflare.com/ai",
			Models:      []string{"@cf/meta/llama-3.1-8b-instruct", "@cf/meta/llama-3.1-70b-instruct"},
			Source: "github_list", Status: "new", DiscoveredAt: now,
		},
		{
			ID: "huggingface-inference", Name: "HuggingFace Inference API",
			BaseURL:     "https://api-inference.huggingface.co/models",
			Description: "HuggingFace \u514d\u8d39\u63a8\u7406 API\uff0c\u53ef\u8c03\u7528\u6570\u5343\u4e2a\u5f00\u6e90\u6a21\u578b",
			APIKeyURL:   "https://huggingface.co/settings/tokens",
			Models:      []string{"meta-llama/Llama-3.1-8B-Instruct", "mistralai/Mistral-7B-Instruct-v0.3"},
			Source: "github_list", Status: "new", DiscoveredAt: now,
		},
		{
			ID: "openrouter-free", Name: "OpenRouter (\u514d\u8d39\u6a21\u578b)",
			BaseURL:     "https://openrouter.ai/api/v1",
			Description: "OpenRouter \u4e0a\u6807\u8bb0\u4e3a\u514d\u8d39\u7684\u6a21\u578b\uff0c\u65e0\u9700\u4ed8\u8d39\u5373\u53ef\u8c03\u7528",
			APIKeyURL:   "https://openrouter.ai/keys",
			Models:      []string{"meta-llama/llama-3.1-8b-instruct:free", "google/gemini-2.0-flash-exp:free"},
			Source: "github_list", Status: "new", DiscoveredAt: now,
		},
		{
			ID: "aihubmix", Name: "AIHubMix",
			BaseURL:     "https://aihubmix.com/v1",
			Description: "\u805a\u5408\u5e73\u53f0\uff0c\u63d0\u4f9b\u514d\u8d39\u989d\u5ea6\uff0cOpenAI \u517c\u5bb9 API",
			APIKeyURL:   "https://aihubmix.com/",
			Models:      []string{"gpt-4o", "claude-3.5-sonnet", "gemini-2.0-flash"},
			Source: "github_list", Status: "new", DiscoveredAt: now,
		},
		{
			ID: "chutes-ai", Name: "Chutes AI",
			BaseURL:     "https://chutes.ai/v1",
			Description: "\u514d\u8d39\u5f00\u6e90\u6a21\u578b\u63a8\u7406\u5e73\u53f0\uff0c\u65e0\u9700\u4fe1\u7528\u5361\u6ce8\u518c",
			APIKeyURL:   "https://chutes.ai/",
			Models:      []string{"deepseek-ai/DeepSeek-V3", "deepseek-ai/DeepSeek-R1"},
			Source: "github_list", Status: "new", DiscoveredAt: now,
		},
		{
			ID: "lmstudio-local", Name: "LM Studio (\u672c\u5730)",
			BaseURL:     "http://localhost:1234/v1",
			Description: "LM Studio \u672c\u5730\u6a21\u578b\u63a8\u7406\uff0cOpenAI \u517c\u5bb9 API",
			APIKeyURL:   "https://lmstudio.ai/",
			Models:      []string{"local-model"},
			Source: "manual", Status: "new", DiscoveredAt: now,
		},
		{
			ID: "vllm-local", Name: "vLLM (\u672c\u5730/\u81ea\u5efa)",
			BaseURL:     "http://localhost:8000/v1",
			Description: "vLLM \u9ad8\u6027\u80fd\u63a8\u7406\u5f15\u64ce\uff0c\u81ea\u5efa\u90e8\u7f72\u540e OpenAI \u517c\u5bb9",
			APIKeyURL:   "https://docs.vllm.ai/",
			Models:      []string{},
			Source: "manual", Status: "new", DiscoveredAt: now,
		},
		{
			ID: "coze-intl", Name: "Coze \u56fd\u9645\u7248",
			BaseURL:     "https://api.coze.com",
			Description: "Coze \u56fd\u9645\u7248\uff0c\u652f\u6301 Bot API \u8c03\u7528\uff0cOpenAI \u517c\u5bb9",
			APIKeyURL:   "https://www.coze.com",
			Models:      []string{},
			Source: "manual", Status: "new", DiscoveredAt: now,
		},
	}
}

// fetchGitHubLists attempts to fetch platform lists from known GitHub repositories.
func fetchGitHubLists() []DiscoveredPlatform {
	var platforms []DiscoveredPlatform
	client := GetSharedHTTPClient()

	sources := []string{
		"https://raw.githubusercontent.com/zukixa/cool-ai-stuff/main/README.md",
	}

	for _, srcURL := range sources {
		req, err := http.NewRequest("GET", srcURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Accept", "text/plain")

		resp, err := client.Do(req)
		if err != nil {
			slog.Debug("discovery: failed to fetch", "url", srcURL, "error", err)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 {
			continue
		}

		content := string(body)
		found := parseMarkdownForPlatforms(content, "github_list")
		platforms = append(platforms, found...)
	}

	return platforms
}

// parseMarkdownForPlatforms extracts platform info from markdown content.
func parseMarkdownForPlatforms(content string, source string) []DiscoveredPlatform {
	var platforms []DiscoveredPlatform
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "api.") && strings.Contains(line, "http") {
			for _, word := range strings.Fields(line) {
				if strings.HasPrefix(word, "http") && strings.Contains(word, "api") {
					url := strings.TrimRight(word, ")>,.`\"'")
					if len(url) > 10 {
						parts := strings.Split(url, "/")
						id := ""
						if len(parts) >= 3 {
							id = strings.ToLower(strings.ReplaceAll(parts[2], ".", "-"))
							id = strings.Split(id, ".")[0]
						}
						if id != "" && len(id) < 30 {
							platforms = append(platforms, DiscoveredPlatform{
								ID:           id,
								Name:         parts[2],
								BaseURL:      url,
								Description:  "\u4ece GitHub \u5217\u8868\u81ea\u52a8\u53d1\u73b0",
								APIKeyURL:    url,
								Source:       source,
								Status:       "new",
								DiscoveredAt: time.Now().Format(time.RFC3339),
							})
						}
					}
					break
				}
			}
		}
	}

	if len(platforms) > 20 {
		platforms = platforms[:20]
	}
	return platforms
}

// handleCheckDiscoveredPlatform checks if a discovered platform is OpenAI API compatible.
func handleCheckDiscoveredPlatform(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/discovered-platforms/")
	id := strings.TrimSuffix(path, "/check")
	id = strings.TrimSpace(id)
	if id == "" {
		writeError(w, 400, "platform ID required")
		return
	}

	platforms := loadDiscoveredPlatforms()
	var found *DiscoveredPlatform
	for i, p := range platforms {
		if p.ID == id {
			found = &platforms[i]
			break
		}
	}
	if found == nil {
		writeError(w, 404, "platform not found")
		return
	}

	baseURL := strings.TrimRight(found.BaseURL, "/")
	// Template variables like {ACCOUNT_ID} - skip check, assume compatible
	if strings.Contains(baseURL, "{") || strings.Contains(baseURL, "}") {
		writeJSON(w, 200, map[string]any{
			"compatible": true,
			"message":    "URL 包含模板变量，跳过自动检测",
			"skipped":    true,
		})
		return
	}

	modelsURL := baseURL + "/v1/models"
	if strings.HasSuffix(baseURL, "/v1") {
		modelsURL = baseURL + "/models"
	}

	client := &http.Client{Timeout: 8 * time.Second}
	req, _ := http.NewRequest("GET", modelsURL, nil)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		// Network error / timeout / connection refused = unreachable, NOT incompatible
		writeJSON(w, 200, map[string]any{
			"compatible": true,
			"message":    "无法访问端点，跳过检测",
			"skipped":    true,
			"warning":    err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode == 200 {
		var result map[string]any
		if err := json.Unmarshal(body, &result); err == nil {
			if _, ok := result["data"]; ok {
				writeJSON(w, 200, map[string]any{"compatible": true, "message": "OpenAI 兼容 API"})
				return
			}
		}
		// 200 but response is NOT OpenAI format
		writeJSON(w, 200, map[string]any{
			"compatible": false,
			"error":      "API 响应格式不兼容 OpenAI 标准",
		})
		return
	}

	// 401/403/404 = endpoint exists, likely compatible
	if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 404 {
		writeJSON(w, 200, map[string]any{
			"compatible": true,
			"message":    fmt.Sprintf("端点响应 HTTP %d，兼容 OpenAI 格式", resp.StatusCode),
		})
		return
	}

	// Other HTTP errors - likely incompatible
	writeJSON(w, 200, map[string]any{
		"compatible": false,
		"error":      fmt.Sprintf("HTTP %d", resp.StatusCode),
	})
}
