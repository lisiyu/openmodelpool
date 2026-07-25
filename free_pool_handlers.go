package main

import (
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
