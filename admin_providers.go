package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// ============================================================
// Provider handlers
// ============================================================

func handleListProviders(w http.ResponseWriter, r *http.Request) {
	owner := getRequestOwner(r)
	providers := pm.GetVisible(owner)
	// Lite mode: exclude models to reduce payload (used by add-platform page)
	if r.URL.Query().Get("lite") == "true" {
		lite := make([]map[string]any, 0, len(providers))
		for _, p := range providers {
			lite = append(lite, map[string]any{
				"id":      p.ID,
				"name":    p.Name,
				"enabled": p.Enabled,
			})
		}
		writeJSON(w, 200, map[string]any{"providers": lite})
		return
	}
	writeJSON(w, 200, map[string]any{"providers": providers})
}

func handleGetPresets(w http.ResponseWriter, r *http.Request) {
	var presets []map[string]any
	for _, p := range presetProviders {
		item := map[string]any{
			"id": p.ID, "name": p.Name, "type": p.Type,
			"base_url": p.BaseURL, "description": p.Description,
			"icon": p.Icon, "default_models": p.Models,
			"api_key_url": p.APIKeyURL,
		}
		if p.WebSession != nil {
			item["web_session"] = p.WebSession
		}
		presets = append(presets, item)
	}
	writeJSON(w, 200, map[string]any{"presets": presets})
}

func handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := readJSON(w, r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	// Extract ID
	id, _ := body["id"].(string)
	if id == "" {
		writeError(w, 400, "provider ID required")
		return
	}
	// B156: Validate provider ID length and format
	if len(id) > 64 || !isValidID(id) {
		writeError(w, 400, "provider ID must be 1-64 chars, alphanumeric/dash/underscore only")
		return
	}
	// B151: Limit provider count to prevent memory exhaustion
	if len(pm.GetAll()) >= 100 {
		writeError(w, 429, "maximum provider count (100) reached")
		return
	}

	// Extract models before unmarshaling (accept both string[] and ModelDef[])
	var models []ModelDef
	if rawModels, ok := body["models"]; ok {
		switch v := rawModels.(type) {
		case []any:
			for _, item := range v {
				switch mv := item.(type) {
				case string:
					models = append(models, ModelDef{ID: mv, Enabled: true})
				case map[string]any:
					md := ModelDef{Enabled: true}
					if mid, ok := mv["id"].(string); ok {
						md.ID = mid
					}
					if mname, ok := mv["name"].(string); ok {
						md.Name = mname
					}
					if menabled, ok := mv["enabled"].(bool); ok {
						md.Enabled = menabled
					}
					models = append(models, md)
				}
			}
		}
	}
	// Remove models from body to avoid unmarshal type conflict
	delete(body, "models")

	// Unmarshal remaining fields into Provider
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		writeError(w, 400, "invalid provider data: cannot marshal request body")
		return
	}
	var p Provider
	if err := json.Unmarshal(bodyJSON, &p); err != nil {
		writeError(w, 400, "invalid provider data: "+err.Error())
		return
	}
	p.ID = id
	if len(models) > 0 {
		p.Models = models
	}

	// B157: Validate provider name length
	if len(p.Name) > 128 {
		writeError(w, 400, "provider name must be at most 128 characters")
		return
	}
	// B157: Validate provider type length
	if len(p.Type) > 64 {
		writeError(w, 400, "provider type must be at most 64 characters")
		return
	}

	// B25: Validate BaseURL format to prevent SSRF
	if p.BaseURL != "" {
		u, err := url.Parse(p.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			writeError(w, 400, "invalid BaseURL: must be a valid http/https URL")
			return
		}
		// P2-3: Block private/loopback IPs to prevent SSRF
		if isLocalOrPrivateIP(u.Hostname()) {
			writeError(w, 400, "invalid BaseURL: must not point to a private or loopback address")
			return
		}
	}

	// Set owner
	owner := getRequestOwner(r)
	p.Owner = owner

	// If provider already exists, merge fields
	if existing, ok := pm.GetRaw(p.ID); ok {
		if p.APIKey == "" || strings.Contains(p.APIKey, "...") {
			p.APIKey = existing.APIKey
		}
		if p.Proxy == "" || p.Proxy == "vmess://***" {
			p.Proxy = existing.Proxy
		}
		p.AccessControl.ShareToPool = existing.AccessControl.ShareToPool
		if len(p.Models) == 0 {
			p.Models = existing.Models
		}
		if len(p.APIKeys) == 0 {
			p.APIKeys = existing.APIKeys
		}
	}

	// Auto-migrate: if api_key is set but APIKeys is empty, create first key entry
	if p.APIKey != "" && p.APIKey != "your-api-key-here" && len(p.APIKeys) == 0 {
		p.APIKeys = []APIKeyConfig{
			{
				ID:            "key-" + p.ID + "-1",
				Key:           p.APIKey,
				Alias:         "默认 Key",
				AccessControl: "private",
				Priority:      1,
				Enabled:       true,
			},
		}
	}

	// For new providers: only enable the latest few models by default
	isNew := false
	if _, exists := pm.GetRaw(p.ID); !exists {
		isNew = true
	}
	if isNew && len(p.Models) > 0 {
		p.Models = enableLatestModels(p.Models)
	}

	// Validate VMess proxy link format
	if strings.HasPrefix(p.Proxy, "vmess://") {
		if _, err := ParseVMessLink(p.Proxy); err != nil {
			writeError(w, 400, "Invalid VMess link: "+err.Error())
			return
		}
		slog.Info("VMess proxy link saved, will start on first use", "provider", p.ID)
	}

	result := pm.Add(p)
	safeCheckProviderNow(p.ID)
	writeJSON(w, 200, map[string]any{"success": true, "data": result})
}

// checkProviderAccess verifies the caller can access a provider.
// Returns the raw provider and true if access is allowed.
func checkProviderAccess(r *http.Request, id string) (Provider, bool) {
	p, ok := pm.GetRaw(id)
	if !ok {
		return Provider{}, false
	}
	owner := getRequestOwner(r)
	if owner == "" {
		return p, true // admin has access to all
	}
	// Consumer can only access their own providers or system presets
	if p.Owner != "" && p.Owner != owner {
		return Provider{}, false
	}
	return p, true
}

func handleGetProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := checkProviderAccess(r, id)
	if !ok {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}
	writeJSON(w, 200, p.Safe())
}

func handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, ok := checkProviderAccess(r, id)
	if !ok {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}
	var updates map[string]any
	if err := readJSON(w, r, &updates); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	// Remove masked API key from updates to prevent overwriting real key
	if apiKey, ok := updates["api_key"]; ok {
		if keyStr, isStr := apiKey.(string); isStr && strings.Contains(keyStr, "...") {
			delete(updates, "api_key")
		}
	}

	// Remove masked VMess proxy to prevent overwriting real link
	if proxy, ok := updates["proxy"]; ok {
		if proxyStr, isStr := proxy.(string); isStr && proxyStr == "vmess://***" {
			delete(updates, "proxy")
		}
	}

	b, err := json.Marshal(existing)
	if err != nil {
		writeError(w, 500, "failed to marshal provider")
		return
	}
	var merged Provider
	if err := json.Unmarshal(b, &merged); err != nil {
		writeError(w, 500, "failed to unmarshal provider")
		return
	}
	// Apply updates via re-serialization
	b2, err2 := json.Marshal(updates)
	if err2 != nil {
		writeError(w, 400, "invalid update data")
		return
	}
	if err := json.Unmarshal(b2, &merged); err != nil {
		writeError(w, 400, "invalid update data: "+err.Error())
		return
	}
	merged.ID = id
	// Preserve ownership — consumer cannot change owner
	merged.Owner = existing.Owner
	// Preserve special provider type (e.g. "coze") — frontend may default to "openai_compatible" on edit
	if existing.Type != "openai_compatible" && existing.Type != "web_session" && merged.Type == "openai_compatible" {
		merged.Type = existing.Type
	}

	// Validate VMess proxy link if changed (lazy start — proxy starts on first use)
	if merged.Proxy != "" && merged.Proxy != existing.Proxy {
		if strings.HasPrefix(merged.Proxy, "vmess://") {
			if _, err := ParseVMessLink(merged.Proxy); err != nil {
				writeError(w, 400, "Invalid VMess link: "+err.Error())
				return
			}
			slog.Info("VMess proxy link saved, will start on first use", "provider", id)
		}
	}

	// B25: Validate BaseURL format to prevent SSRF
	if merged.BaseURL != "" {
		u, err := url.Parse(merged.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			writeError(w, 400, "invalid BaseURL: must be a valid http/https URL")
			return
		}
	}

	// Auto-migrate: if api_key is set but APIKeys is empty, create first key entry
	if merged.APIKey != "" && merged.APIKey != "your-api-key-here" && len(merged.APIKeys) == 0 {
		merged.APIKeys = []APIKeyConfig{
			{
				ID:            "key-" + merged.ID + "-1",
				Key:           merged.APIKey,
				Alias:         "默认 Key",
				AccessControl: "private",
				Priority:      1,
				Enabled:       true,
			},
		}
	}

	result := pm.Add(merged)
	safeCheckProviderNow(id)
	writeJSON(w, 200, map[string]any{"success": true, "data": result})
}

func handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := checkProviderAccess(r, id); !ok {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}
	if !pm.Delete(id) {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}
	writeJSON(w, 200, map[string]bool{"success": true})
}

func handleTestProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := checkProviderAccess(r, id); !ok {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}
	p, _ := pm.GetRaw(id)

	// Check if testing a specific key by key_id query parameter
	keyID := r.URL.Query().Get("key_id")
	if keyID != "" {
		// Find the specific key
		var targetKey *APIKeyConfig
		for i := range p.APIKeys {
			if p.APIKeys[i].ID == keyID {
				targetKey = &p.APIKeys[i]
				break
			}
		}
		if targetKey == nil {
			writeError(w, 404, fmt.Sprintf("key '%s' not found", keyID))
			return
		}
		// Decrypt the key for testing
		decryptedKey, err := decryptAPIKey(targetKey.Key)
		if err != nil {
			writeError(w, 500, "failed to decrypt key")
			return
		}
		result := testConnectionWithKey(p, decryptedKey)
		// Sanitize error messages
		if errMsg, ok := result["error"].(string); ok && errMsg != "" {
			result["error"] = "upstream error"
		}
		result["key_id"] = keyID
		result["key_alias"] = targetKey.Alias
		writeJSON(w, 200, result)
		return
	}

	// Default: test with effective key
	result := testConnection(p)
	// Sanitize error messages but keep HTTP status for debugging
	if errMsg, ok := result["error"].(string); ok && errMsg != "" {
		// Keep the error if it contains HTTP status info, otherwise generic
		if !strings.Contains(errMsg, "HTTP ") {
			result["error"] = "上游服务错误"
		}
	}
	writeJSON(w, 200, result)
}

// handleTestAllKeys tests all API keys for a provider and returns individual results
func handleTestAllKeys(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := checkProviderAccess(r, id); !ok {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}
	p, _ := pm.GetRaw(id)

	if len(p.APIKeys) == 0 {
		writeJSON(w, 200, map[string]any{
			"success": false,
			"error":   "no API keys configured",
			"results": []any{},
		})
		return
	}

	results := make([]map[string]any, 0, len(p.APIKeys))
	allSuccess := true

	for i, key := range p.APIKeys {
		keyResult := map[string]any{
			"index":   i + 1,
			"key_id":  key.ID,
			"alias":   key.Alias,
			"enabled": key.Enabled,
		}

		if !key.Enabled {
			keyResult["success"] = false
			keyResult["error"] = "key is disabled"
			allSuccess = false
			results = append(results, keyResult)
			continue
		}

		// Decrypt the key for testing
		decryptedKey, err := decryptAPIKey(key.Key)
		if err != nil {
			keyResult["success"] = false
			keyResult["error"] = "failed to decrypt key"
			allSuccess = false
			results = append(results, keyResult)
			continue
		}

		// Test this specific key
		testResult := testConnectionWithKey(p, decryptedKey)
		keyResult["success"] = testResult["success"]
		if errMsg, ok := testResult["error"].(string); ok && errMsg != "" {
			keyResult["error"] = "upstream error"
			allSuccess = false
		}
		if msg, ok := testResult["message"].(string); ok {
			keyResult["message"] = msg
		}

		// Query upstream balance if test succeeded
		if testResult["success"] == true && p.Type == "openai_compatible" {
			balance := queryKeyBalance(p.BaseURL, decryptedKey)
			if avail, ok := balance["available"].(bool); ok && avail {
				keyResult["balance"] = balance
				// If upstream reports a dollar limit, convert to approximate tokens
				// and update local quota if upstream is authoritative
				var limitUSD float64
				if v, ok := balance["hard_limit_usd"].(float64); ok {
					limitUSD = v
				} else if v, ok := balance["total_granted_usd"].(float64); ok {
					limitUSD = v
				}
				if limitUSD > 0 {
					// Estimate: $1 ≈ 1M tokens (rough average for GPT-4 class models)
					estimatedTokens := int64(limitUSD * 1_000_000)
					// Update quota_monthly (upstream billing is usually monthly)
					if key.QuotaMonthly == 0 || key.QuotaMonthly != estimatedTokens {
						pm.UpdateAPIKey(id, key.ID, map[string]any{"quota_monthly": float64(estimatedTokens)})
						keyResult["quota_updated"] = true
						keyResult["new_quota"] = estimatedTokens
						keyResult["quota_period"] = "monthly"
					}
				}
			}
		}

		results = append(results, keyResult)
	}

	response := map[string]any{
		"success": allSuccess,
		"results": results,
		"total":   len(results),
	}

	failedCount := 0
	for _, r := range results {
		if s, ok := r["success"].(bool); !ok || !s {
			failedCount++
		}
	}
	response["failed_count"] = failedCount

	// Update health status with actual failed key count from manual test
	safeSetFailedKeyCount(id, failedCount)

	writeJSON(w, 200, response)
}

func handleGetProviderModels(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := pm.GetRaw(id)
	if !ok {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}

	// Try fetching from remote first
	models := fetchRemoteModels(p)

	// Fallback to cached/stored models if remote fetch returned nothing
	if len(models) == 0 && len(p.Models) > 0 {
		models = make([]map[string]string, 0, len(p.Models))
		for _, md := range p.Models {
			if md.Enabled {
				models = append(models, map[string]string{"id": md.ID, "name": md.Name})
			}
		}
		slog.Debug("using cached models", "provider", id, "count", len(models))
	}

	if models == nil {
		models = []map[string]string{}
	}

	writeJSON(w, 200, map[string]any{"models": models, "count": len(models)})
}