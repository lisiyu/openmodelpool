package main

import (
	"fmt"
	"log/slog"
	"net/http"
)

// ============================================================
// Provider model sync handler
// ============================================================

func handleSyncModels(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := checkProviderWriteAccess(r, id) // B8-2
	if !ok {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}
	_ = p

	count, err := pm.SyncModels(id)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "models_synced": count})
}

// ============================================================
// Provider Access Control handlers
// ============================================================

// handleGetProviderAccessControl returns the access control settings for a provider.
// GET /api/providers/{id}/access-control
func handleGetProviderAccessControl(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := pm.GetRaw(id)
	if !ok {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}
	writeJSON(w, 200, p.AccessControl)
}

// handleUpdateProviderAccessControl updates the access control settings for a provider.
// PUT /api/providers/{id}/access-control
func handleUpdateProviderAccessControl(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := pm.GetRaw(id)
	if !ok {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}

	var ac ProviderAccessControl
	if err := readJSON(w, r, &ac); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	// ShareToPool is managed by the admin UI/API; no normalization needed.

	p.AccessControl = ac
	pm.Add(p)

	slog.Info("provider access control updated", "provider", id, "share_to_pool", ac.ShareToPool)
	writeJSON(w, 200, map[string]any{"success": true, "access_control": ac})
}

// ============================================================
// Sider handlers
// ============================================================

func handleSiderStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, siderMon.GetStatus())
}

func handleSiderTest(w http.ResponseWriter, r *http.Request) {
	p, ok := pm.GetRaw("sider")
	if !ok {
		writeError(w, 404, "Sider not configured")
		return
	}
	// v4.6.56 fix: also check the multi-key APIKeys array, not just the
	// legacy APIKey field. handleTestProvider/testConnection already does
	// this via GetEffectiveAPIKey, but handleSiderTest rejected providers
	// that only have keys in the array with "Sider token not configured".
	if p.APIKey == "" && len(p.APIKeys) > 0 {
		p.APIKey = p.GetEffectiveAPIKey()
	}
	if p.APIKey == "" {
		writeJSON(w, 200, map[string]any{"valid": false, "message": "Sider token not configured"})
		return
	}
	result := testConnection(p)
	if result["success"].(bool) {
		siderMon.RecordSuccess()
		writeJSON(w, 200, map[string]any{"valid": true, "message": "Token valid"})
	} else {
		errMsg, _ := result["error"].(string)
		siderMon.RecordFailure(0, errMsg)
		writeJSON(w, 200, map[string]any{"valid": false, "message": errMsg})
	}
}
