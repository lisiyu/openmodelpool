package main

import (
	"encoding/json"
	"errors"
	"net/http"
)

// Admin handlers for certified education/public-welfare quota grants (Phase 3
// "免费池增强"). These are management + audit surfaces, intentionally small:
// the grant store itself (grant_quota.go) is the single source of truth.

func handleAdminLedgerGrantsList(w http.ResponseWriter, r *http.Request) {
	if grantQuota == nil {
		writeJSON(w, 503, map[string]any{"error": "grant quota manager not initialized"})
		return
	}
	grants := grantQuota.GetGrants()
	active := 0
	for _, g := range grants {
		if !g.Revoked {
			active++
		}
	}
	writeJSON(w, 200, map[string]any{
		"grants": grants,
		"active": active,
		"total":  len(grants),
	})
}

func handleAdminLedgerGrantsGrant(w http.ResponseWriter, r *http.Request) {
	if grantQuota == nil {
		writeJSON(w, 503, map[string]any{"error": "grant quota manager not initialized"})
		return
	}
	var body struct {
		Holder      string `json:"holder"`
		KeyID       string `json:"key_id"`
		DailyTokens int64  `json:"daily_tokens"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid JSON body"})
		return
	}
	entry, err := grantQuota.Grant(body.Holder, body.KeyID, body.DailyTokens)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errActiveGrantExists) {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return
	}
	auditRecord(r, "grant_quota.create", entry.ID, entry.Holder+"/"+entry.KeyID, true)
	writeJSON(w, 201, map[string]any{"grant": entry})
}

func handleAdminLedgerGrantsRevoke(w http.ResponseWriter, r *http.Request) {
	if grantQuota == nil {
		writeJSON(w, 503, map[string]any{"error": "grant quota manager not initialized"})
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		writeJSON(w, 400, map[string]any{"error": "missing ?id="})
		return
	}
	if err := grantQuota.Revoke(id); err != nil {
		status := http.StatusNotFound
		if err.Error() == "grant already revoked" {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return
	}
	auditRecord(r, "grant_quota.revoke", id, "", true)
	writeJSON(w, 200, map[string]any{"revoked": id})
}
