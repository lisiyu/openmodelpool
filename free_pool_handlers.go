package main

import (
	"strings"
	"encoding/json"
	"log/slog"
	"net/http"
)

// ============================================================
// Free Pool — Admin API handlers
// ============================================================

// handleFreePoolStatus returns the current sync status and provider list.
func handleFreePoolStatus(w http.ResponseWriter, r *http.Request) {
	if freePool == nil {
		writeJSON(w, 200, FreePoolStats{
			AutoSync:  false,
			SourceURL: freePoolSourceURL,
		})
		return
	}
	writeJSON(w, 200, freePool.GetStats())
}

// handleFreePoolSync triggers a manual sync.
func handleFreePoolSync(w http.ResponseWriter, r *http.Request) {
	if freePool == nil {
		writeError(w, 500, "free pool manager not initialized")
		return
	}

	// Run sync in background to avoid HTTP timeout
	go func() {
		if err := freePool.Sync(); err != nil {
			slog.Error("manual free pool sync failed", "error", err)
		}
	}()

	writeJSON(w, 200, map[string]any{
		"message": "sync started in background",
		"status":  "running",
	})
}

// handleFreePoolConfig updates free pool configuration (auto-sync toggle).
func handleFreePoolConfig(w http.ResponseWriter, r *http.Request) {
	if freePool == nil {
		writeError(w, 500, "free pool manager not initialized")
		return
	}

	var req struct {
		AutoSync *bool `json:"auto_sync"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	if req.AutoSync != nil {
		freePool.SetAutoSync(*req.AutoSync)
		slog.Info("free pool auto-sync toggled", "enabled", *req.AutoSync)
	}

	writeJSON(w, 200, map[string]any{
		"auto_sync": freePool.GetStats().AutoSync,
	})
}

// handleFreePoolSetKey sets an API key for a free pool provider,
// enables it, and triggers a model sync.
func handleFreePoolSetKey(w http.ResponseWriter, r *http.Request) {
	if freePool == nil {
		writeError(w, 500, "free pool manager not initialized")
		return
	}

	providerID := r.PathValue("providerId")
	if providerID == "" {
		writeError(w, 400, "providerId is required")
		return
	}

	var req struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	if req.APIKey == "" || req.APIKey == "free-anonymous" {
		writeError(w, 400, "API key cannot be empty")
		return
	}

	// Get existing provider
	existing, ok := pm.GetRaw(providerID)
	if !ok {
		writeError(w, 404, "provider not found: "+providerID)
		return
	}

	// Verify it's a free pool provider
	if !strings.HasPrefix(providerID, "free-") {
		writeError(w, 400, "not a free pool provider")
		return
	}

	// Update API key and enable
	existing.APIKey = req.APIKey
	existing.APIKeys = nil // clear multi-key array, use single key
	existing.Enabled = true

	// Preserve free pool settings
	existing.AccessControl.ShareToPool = true
	if existing.Priority == 0 {
		existing.Priority = freePoolPriority
	}

	pm.Add(existing)
	slog.Info("free pool provider key set", "provider", providerID)

	// Sync models from the provider to get real model list
	go func() {
		count, err := pm.SyncModels(providerID)
		if err != nil {
			slog.Warn("free pool: model sync after key set failed",
				"provider", providerID, "error", err)
			return
		}
		slog.Info("free pool: models synced after key set",
			"provider", providerID, "models", count)

		// Update stats
		freePool.mu.Lock()
		for i := range freePool.stats.Providers {
			if freePool.stats.Providers[i].ID == providerID {
				freePool.stats.Providers[i].ModelCount = count
				freePool.stats.Providers[i].Enabled = true
			}
		}
		total := 0
		active := 0
		for _, p := range freePool.stats.Providers {
			total += p.ModelCount
			if p.Enabled {
				active++
			}
		}
		freePool.stats.TotalModels = total
		freePool.stats.ActiveProviders = active
		freePool.mu.Unlock()
	}()

	writeJSON(w, 200, map[string]any{
		"message":  "API key set, provider enabled and model sync started",
		"provider": providerID,
	})
}

// handleFreePoolRemoveKey removes the API key from a free pool provider
// and disables it.
func handleFreePoolRemoveKey(w http.ResponseWriter, r *http.Request) {
	if freePool == nil {
		writeError(w, 500, "free pool manager not initialized")
		return
	}

	providerID := r.PathValue("providerId")
	if providerID == "" {
		writeError(w, 400, "providerId is required")
		return
	}

	existing, ok := pm.GetRaw(providerID)
	if !ok {
		writeError(w, 404, "provider not found: "+providerID)
		return
	}

	existing.APIKey = ""
	existing.APIKeys = nil
	existing.Enabled = false
	pm.Add(existing)

	// Update stats
	freePool.mu.Lock()
	for i := range freePool.stats.Providers {
		if freePool.stats.Providers[i].ID == providerID {
			freePool.stats.Providers[i].Enabled = false
		}
	}
	active := 0
	for _, p := range freePool.stats.Providers {
		if p.Enabled {
			active++
		}
	}
	freePool.stats.ActiveProviders = active
	freePool.mu.Unlock()

	slog.Info("free pool provider key removed", "provider", providerID)
	writeJSON(w, 200, map[string]any{
		"message":  "API key removed, provider disabled",
		"provider": providerID,
	})
}
