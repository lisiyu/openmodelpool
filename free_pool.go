package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ============================================================
// Free Pool — Auto-sync free LLM API providers from
// github.com/mnfst/awesome-free-llm-apis
// ============================================================

const (
	freePoolSourceURL    = "https://raw.githubusercontent.com/mnfst/awesome-free-llm-apis/main/data.json"
	freePoolSyncInterval = 24 * time.Hour
	freePoolPriority     = 100 // low priority — tried after paid providers
)

// FreePoolManager manages syncing free LLM API providers.
type FreePoolManager struct {
	mu        sync.RWMutex
	lastSync  time.Time
	sourceURL string
	autoSync  bool
	stats     FreePoolStats
	stopCh    chan struct{} // B11: graceful shutdown
}

// FreePoolStats holds sync status information for the admin UI.
type FreePoolStats struct {
	LastSync        string                 `json:"last_sync"`
	SourceURL       string                 `json:"source_url"`
	SourceUpdated   string                 `json:"source_updated"`
	TotalProviders  int                    `json:"total_providers"`
	TotalModels     int                    `json:"total_models"`
	ActiveProviders int                    `json:"active_providers"`
	AutoSync        bool                   `json:"auto_sync"`
	Providers       []FreePoolProviderInfo `json:"providers"`
}

// FreePoolProviderInfo is a per-provider summary for the admin UI.
type FreePoolProviderInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	BaseURL    string `json:"base_url"`
	Anonymous  bool   `json:"anonymous"`
	ModelCount int    `json:"model_count"`
	Enabled    bool   `json:"enabled"`
	APIKeyURL  string `json:"api_key_url,omitempty"`
	Country    string `json:"country,omitempty"`
}

// ---- data.json structs (awesome-free-llm-apis) ----

type awesomeFreeLLMData struct {
	LastUpdated string            `json:"lastUpdated"`
	Providers   []awesomeProvider `json:"providers"`
}

type awesomeProvider struct {
	Name        string         `json:"name"`
	Category    string         `json:"category"`
	Country     string         `json:"country"`
	Flag        string         `json:"flag"`
	URL         string         `json:"url"`
	BaseURL     string         `json:"baseUrl"`
	Description string         `json:"description"`
	Models      []awesomeModel `json:"models"`
}

type awesomeModel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Context   string `json:"context"`
	MaxOutput string `json:"maxOutput"`
	Modality  string `json:"modality"`
	RateLimit string `json:"rateLimit"`
}

// initFreePool initializes the free pool manager and starts background sync.
func initFreePool() {
	freePool = &FreePoolManager{
		sourceURL: freePoolSourceURL,
		autoSync:  cfg.Get("free_pool_auto_sync", "true") == "true",
		stopCh:    make(chan struct{}),
	}

	// Initial sync on startup (delayed to let other components initialize)
	if freePool.autoSync {
		go func() {
			time.Sleep(10 * time.Second)
			if err := freePool.Sync(); err != nil {
				slog.Warn("free pool initial sync failed", "error", err)
			}
		}()
	}

	// Start periodic sync loop
	go freePool.syncLoop()

	slog.Info("free pool manager initialized", "auto_sync", freePool.autoSync)
}

func (f *FreePoolManager) syncLoop() {
	ticker := time.NewTicker(freePoolSyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			f.mu.RLock()
			auto := f.autoSync
			f.mu.RUnlock()
			if auto {
				if err := f.Sync(); err != nil {
					slog.Warn("free pool periodic sync failed", "error", err)
				}
			}
		case <-f.stopCh:
			return
		}
	}
}

func (f *FreePoolManager) stop() { close(f.stopCh) }

// Sync fetches the latest data.json and updates OMP providers.
func (f *FreePoolManager) Sync() error {
	slog.Info("free pool sync started", "source", f.sourceURL)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(f.sourceURL)
	if err != nil {
		slog.Error("free pool sync: fetch failed", "error", err)
		return fmt.Errorf("fetch data.json: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("free pool sync: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	var data awesomeFreeLLMData
	if err := json.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("parse json: %w", err)
	}

	f.mu.Lock()
	f.lastSync = time.Now()
	f.mu.Unlock()

	totalModels := 0
	activeCount := 0
	var providerInfos []FreePoolProviderInfo

	for _, ap := range data.Providers {
		providerID, models, anonymous, baseURL, skip := mapAwesomeProvider(ap)
		if skip || len(models) == 0 {
			continue
		}

		totalModels += len(models)

		// Check if provider already has API keys configured
		// (free-anonymous is not a real key)
		existing, exists := pm.GetRaw(providerID)
		hasKeys := exists && (existing.APIKey != "" && existing.APIKey != "free-anonymous" || len(existing.APIKeys) > 0)

		// Enabled state: anonymous providers always enabled,
		// key-based providers enabled only if they have keys
		enabled := anonymous || hasKeys
		if enabled {
			activeCount++
		}

		// Build provider
		provider := Provider{
			ID:          providerID,
			Name:        fmt.Sprintf("%s %s (免费)", ap.Flag, ap.Name),
			Type:        "openai_compatible",
			BaseURL:     baseURL,
			Enabled:     enabled,
			Models:      models,
			Priority:    freePoolPriority,
			Description: ap.Description,
			Icon:        "free",
			APIKeyURL:   ap.URL,
			AccessControl: ProviderAccessControl{
				ShareToPool: true,
			},
			UpdatedAt: time.Now().Format(time.RFC3339),
		}

		if anonymous {
			provider.APIKey = "free-anonymous"
		} else if hasKeys {
			// Preserve existing API keys
			provider.APIKey = existing.APIKey
			provider.APIKeys = existing.APIKeys
			provider.CreatedAt = existing.CreatedAt
		}

		// Set rate limit from first model's rate limit string
		if len(ap.Models) > 0 {
			rpm := parseRPM(ap.Models[0].RateLimit)
			if rpm > 0 {
				provider.RateLimitEnabled = true
				provider.RateLimitPerMin = rpm
			}
		}

		// Preserve CreatedAt for existing providers
		if exists {
			provider.CreatedAt = existing.CreatedAt
		}

		pm.Add(provider)

		providerInfos = append(providerInfos, FreePoolProviderInfo{
			ID:         providerID,
			Name:       provider.Name,
			BaseURL:    baseURL,
			Anonymous:  anonymous,
			ModelCount: len(models),
			Enabled:    enabled,
			APIKeyURL:  ap.URL,
			Country:    ap.Country,
		})
	}

	// Persist sync timestamp
	cfg.Set("free_pool_last_sync", f.lastSync.Format(time.RFC3339))

	f.mu.Lock()
	f.stats = FreePoolStats{
		LastSync:        f.lastSync.Format(time.RFC3339),
		SourceURL:       f.sourceURL,
		SourceUpdated:   data.LastUpdated,
		TotalProviders:  len(providerInfos),
		TotalModels:     totalModels,
		ActiveProviders: activeCount,
		AutoSync:        f.autoSync,
		Providers:       providerInfos,
	}
	f.mu.Unlock()

	// Sync real models from anonymous providers (data.json may be outdated)
	go f.syncRealModels()

	slog.Info("free pool sync completed",
		"providers", len(providerInfos),
		"models", totalModels,
		"active", activeCount)
	return nil
}

// mapAwesomeProvider converts an awesome-free-llm-apis provider entry to OMP format.
// Returns: providerID, models, anonymous, baseURL, skip
func mapAwesomeProvider(ap awesomeProvider) (string, []ModelDef, bool, string, bool) {
	// Skip non-OpenAI-compatible providers
	switch ap.Name {
	case "Cohere":
		// Cohere v2 API is not OpenAI-compatible
		return "", nil, false, "", true
	case "Ollama Cloud":
		// Ollama API is not OpenAI SDK-compatible
		return "", nil, false, "", true
	case "Cloudflare Workers AI":
		// Requires account_id in URL path, non-standard
		return "", nil, false, "", true
	}

	providerID := "free-" + slugify(ap.Name)

	// Determine if anonymous (no API key needed)
	anonymous := false
	switch ap.Name {
	case "OVHcloud AI Endpoints", "Kilo Code":
		anonymous = true
	}

	// Adjust base URL for providers with non-standard OpenAI endpoints
	baseURL := ap.BaseURL
	if ap.Name == "Google Gemini" {
		// Google's OpenAI-compatible endpoint
		baseURL = "https://generativelanguage.googleapis.com/v1beta/openai"
	}

	// Map models — skip placeholder entries (id == null)
	var models []ModelDef
	for _, am := range ap.Models {
		if am.ID == "" || am.ID == "null" {
			continue
		}
		models = append(models, ModelDef{
			ID:      am.ID,
			Name:    am.Name,
			Enabled: true,
		})
	}

	if len(models) == 0 {
		return "", nil, false, "", true
	}

	return providerID, models, anonymous, baseURL, false
}

// slugify converts a provider name to a URL-safe slug.
func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, ".", "")
	s = strings.ReplaceAll(s, "(", "")
	s = strings.ReplaceAll(s, ")", "")
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, "_", "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

// parseRPM extracts requests-per-minute from a rate limit string.
// e.g. "30 RPM, 1,000 RPD" → 30
func parseRPM(rateLimit string) int {
	parts := strings.Split(rateLimit, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if strings.Contains(p, "RPM") {
			numStr := strings.TrimSuffix(p, "RPM")
			numStr = strings.TrimSpace(numStr)
			numStr = strings.ReplaceAll(numStr, ",", "")
			var n int
			fmt.Sscanf(numStr, "%d", &n)
			return n
		}
	}
	return 0
}

// GetStats returns the current sync stats (thread-safe).
func (f *FreePoolManager) GetStats() FreePoolStats {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.stats
}

// SetAutoSync toggles auto-sync and persists the setting.
func (f *FreePoolManager) SetAutoSync(enabled bool) {
	f.mu.Lock()
	f.autoSync = enabled
	f.stats.AutoSync = enabled
	f.mu.Unlock()
	cfg.Set("free_pool_auto_sync", fmt.Sprintf("%v", enabled))
}

// syncRealModels fetches actual available models from anonymous providers'
// /v1/models endpoints, replacing potentially outdated data.json model lists.
func (f *FreePoolManager) syncRealModels() {
	f.mu.RLock()
	providerInfos := f.stats.Providers
	f.mu.RUnlock()

	for _, pi := range providerInfos {
		if !pi.Anonymous || !pi.Enabled {
			continue
		}
		// Use SyncModels which calls fetchRemoteModels (already fixed to
		// skip Authorization header for free-anonymous keys)
		count, err := pm.SyncModels(pi.ID)
		if err != nil {
			slog.Warn("free pool: real model sync failed",
				"provider", pi.ID, "error", err)
			continue
		}
		slog.Info("free pool: real models synced",
			"provider", pi.ID, "models", count)

		// Update stats model count
		f.mu.Lock()
		for i := range f.stats.Providers {
			if f.stats.Providers[i].ID == pi.ID {
				f.stats.Providers[i].ModelCount = count
			}
		}
		// Recalculate total models
		total := 0
		for _, p := range f.stats.Providers {
			total += p.ModelCount
		}
		f.stats.TotalModels = total
		f.mu.Unlock()
	}
}
