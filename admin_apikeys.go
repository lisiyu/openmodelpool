package main

import (
	"fmt"
	"net/http"
)

// ============================================================
// Multi API Key Management Handlers
// ============================================================

// handleListAPIKeys returns all API keys for a provider (masked).
// GET /api/providers/{id}/keys
func handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := checkProviderAccess(r, id); !ok {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}

	keys, err := pm.GetAPIKeys(id)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"keys": keys, "count": len(keys)})
}

// handleAddAPIKey adds a new API key to a provider.
// POST /api/providers/{id}/keys
func handleAddAPIKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := checkProviderAccess(r, id); !ok {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}

	var key APIKeyConfig
	if err := readJSON(w, r, &key); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if key.Key == "" {
		writeError(w, 400, "API key value required")
		return
	}

	if err := pm.AddAPIKey(id, key); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "message": "API key added"})
}

// handleUpdateAPIKey updates an existing API key.
// PUT /api/providers/{id}/keys/{key_id}
func handleUpdateAPIKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	keyID := r.PathValue("key_id")
	if _, ok := checkProviderAccess(r, id); !ok {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}

	var updates map[string]any
	if err := readJSON(w, r, &updates); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	if err := pm.UpdateAPIKey(id, keyID, updates); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "message": "API key updated"})
}

// handleDeleteAPIKey removes an API key from a provider.
// DELETE /api/providers/{id}/keys/{key_id}
func handleDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	keyID := r.PathValue("key_id")
	if _, ok := checkProviderAccess(r, id); !ok {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}

	if err := pm.DeleteAPIKey(id, keyID); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "message": "API key deleted"})
}

// handleResetKeyQuota resets the used quota for an API key.
// POST /api/providers/{id}/keys/{key_id}/reset-quota
func handleResetKeyQuota(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	keyID := r.PathValue("key_id")
	if _, ok := checkProviderAccess(r, id); !ok {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}

	if err := pm.ResetKeyQuota(id, keyID); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "message": "quota reset"})
}