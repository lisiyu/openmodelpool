package main

import (
	"crypto/tls"
	"encoding/json"
	"golang.org/x/crypto/bcrypt"
	"io"
	"log/slog"
	"fmt"
	"net/smtp"
	"strings"
	"net/http"
	"strconv"
	"time"
	"os"
	"os/exec"
)

// ============================================================
// Auth handlers
// ============================================================

func handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]bool{"initialized": auth.Initialized()})
}

func handleSetup(w http.ResponseWriter, r *http.Request) {
	if auth.Initialized() {
		writeError(w, 400, "admin already initialized")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Email    string `json:"email"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if err := auth.SetupAdmin(body.Username, body.Password, body.Email); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "data": auth.AdminInfo()})
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Remember bool   `json:"remember"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if body.Username == "" || body.Password == "" {
		writeError(w, 400, "username and password required")
		return
	}
	if !auth.VerifyCredentials(body.Username, body.Password) {
		writeError(w, 401, "invalid credentials")
		return
	}
	accessToken, refreshToken := auth.CreateToken(body.Username, body.Remember)
	maxAge := 86400
	if body.Remember {
		maxAge = 7 * 86400
	}
	// Determine if Secure flag should be set
	isHTTPS := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	c := &http.Cookie{
		Name:     "admin_token",
		Path:     "/",
		Value:    accessToken,
		HttpOnly: true,
		MaxAge:   maxAge,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS,
	}
	http.SetCookie(w, c)
	writeJSON(w, 200, map[string]string{"access_token": accessToken, "refresh_token": refreshToken, "token_type": "bearer"})
}

func handleVerifyAuth(w http.ResponseWriter, r *http.Request) {
	token := extractToken(r)
	if token == "" {
		writeJSON(w, 401, map[string]any{"valid": false, "error": "no token provided"})
		return
	}
	username, err := auth.VerifyToken(token)
	if err != nil {
		// P0-1: Properly reject invalid tokens instead of always returning true
		writeJSON(w, 401, map[string]any{"valid": false, "error": "invalid or expired token"})
		return
	}
	writeJSON(w, 200, map[string]any{"valid": true, "username": username})
}

func handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	if !auth.Initialized() {
		writeError(w, 400, "system not initialized")
		return
	}
	var body struct{ Email string `json:"email"` }
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	// Verify email matches admin email
	if body.Email == "" || body.Email != auth.GetEmail() {
		// Always return success to prevent email enumeration
		writeJSON(w, 200, map[string]any{"success": true, "message": "如果邮箱已配置，重置链接已发送"})
		return
	}

	// Check if SMTP is configured
	if !auth.IsSMTPConfigured() {
		writeError(w, 400, "邮件服务未配置，无法发送重置链接。请使用「重置密码」功能（通过 Proxy API Key）")
		return
	}

	// Generate reset token
	token := auth.CreateResetToken()

	// Send email with reset link
	s := auth.GetSMTP()
	adminEmail := auth.GetEmail()

	// Build reset URL from request
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	resetURL := fmt.Sprintf("%s://%s/forgot-password", scheme, r.Host)

	subject := "OpenModelPool Agent 密码重置"
	// S-6: Token is included in email body, NOT in URL. User copies token to reset page.
	msgBody := fmt.Sprintf("Subject: %s\r\nFrom: %s\r\nTo: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n"+
		"<h3>OpenModelPool Agent 密码重置</h3>"+
		"<p>点击下方链接进入密码重置页面（30 分钟内有效）：</p>"+
		`<p><a href="%s" style="padding:10px 20px;background:#6c63ff;color:white;text-decoration:none;border-radius:6px;">重置密码</a></p>`+
		"<p>或复制以下重置令牌粘贴到重置页面：</p>"+
		"<p style='font-size:18px;font-weight:bold;letter-spacing:2px;color:#333;'>%s</p>"+
		"<p style='word-break:break-all;color:#666;'>%s</p>"+
		"<p style='color:#999;font-size:12px;'>如非本人操作，请忽略此邮件。</p>",
		subject, s.FromEmail, adminEmail, resetURL, token, resetURL)

	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	var smtpAuth smtp.Auth
	if s.Username != "" {
		smtpAuth = smtp.PlainAuth("", s.Username, s.Password, s.Host)
	}

	var err error
	if s.UseTLS && s.Port == 465 {
		err = sendMailTLS(addr, smtpAuth, s.FromEmail, []string{adminEmail}, []byte(msgBody))
	} else {
		err = smtp.SendMail(addr, smtpAuth, s.FromEmail, []string{adminEmail}, []byte(msgBody))
	}

	if err != nil {
		slog.Error("failed to send reset email", "error", err)
		writeError(w, 500, "发送重置邮件失败: "+err.Error())
		return
	}

	slog.Info("password reset email sent", "email", adminEmail)
	writeJSON(w, 200, map[string]any{"success": true, "message": "重置链接已发送到你的邮箱"})
}

func handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if err := auth.ResetPassword(body.Token, body.NewPassword); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "message": "password reset"})
}

func handleVerifyResetToken(w http.ResponseWriter, r *http.Request) {
	var body struct{ Token string `json:"token"` }
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if !auth.VerifyResetToken(body.Token) {
		writeError(w, 400, "invalid or expired reset token")
		return
	}
	writeJSON(w, 200, map[string]bool{"valid": true})
}

// ============================================================
// Admin handlers
// ============================================================

func handleAdminInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, auth.AdminInfo())
}


// maskKey masks an API key: shows first 4 and last 4 chars.
func maskKey(key string) string {
	if len(key) <= 8 {
		return "***"
	}
	return key[:4] + "***" + key[len(key)-4:]
}

// handleShareInfo returns all data needed for the Share Center UI.
func handleShareInfo(w http.ResponseWriter, r *http.Request) {
	proxyURL := cfg.Get("service_host", "")
	if proxyURL == "" {
		// Build from request
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		proxyURL = scheme + "://" + r.Host
	}
	proxyURL += "/v1"

	tunnelURL := cfg.Get("tunnel_url", "")
	proxyAPIKey := cfg.Get("proxy_api_key", "")
	// Mask proxy API key for security - show only first 6 and last 4 chars
	maskedKey := proxyAPIKey
	if len(proxyAPIKey) > 10 {
		maskedKey = proxyAPIKey[:6] + "****" + proxyAPIKey[len(proxyAPIKey)-4:]
	}

	info := map[string]any{
		"proxy_api_url": proxyURL,
		"proxy_api_key": maskedKey,
		"tunnel_url":    tunnelURL,
		"genesis":       GenesisInfo(),
		"seed_nodes":    []string{},
	}

	// Collect seed nodes from federation trust pool
	if fed != nil {
		pool := fed.GetTrustPool()
		var seeds []string
		for _, n := range pool.Nodes {
			if n.SeedNode && n.Endpoint != "" {
				seeds = append(seeds, n.Endpoint)
			}
		}
		if len(seeds) > 0 {
			info["seed_nodes"] = seeds
		}
	}

	// Public API URL priority: tunnel URL > public IP > request host
	if tunnelURL != "" {
		if !strings.HasSuffix(tunnelURL, "/v1") {
			info["public_api_url"] = tunnelURL + "/v1"
		} else {
			info["public_api_url"] = tunnelURL
		}
	} else {
		// Try to use public IP
		publicIP := detectPublicIP()
		if publicIP != "" {
			port := cfg.Get("service_port", "8000")
			info["public_api_url"] = "http://" + publicIP + ":" + port + "/v1"
		} else {
			info["public_api_url"] = proxyURL
		}
	}

	writeJSON(w, 200, info)
}

func handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if err := auth.ChangePassword(body.OldPassword, body.NewPassword); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "message": "password changed"})
}

func handleUpdateEmail(w http.ResponseWriter, r *http.Request) {
	var body struct{ Email string `json:"email"` }
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	auth.UpdateEmail(body.Email)
	writeJSON(w, 200, map[string]any{"success": true, "message": "email updated"})
}

func handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, cfg.Masked())
}


func mapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if err := readJSON(r, &body); err != nil {
		slog.Error("handleSaveConfig: readJSON failed", "error", err)
		writeError(w, 400, "invalid JSON body: "+err.Error())
		return
	}
	slog.Info("handleSaveConfig: received body", "keys", fmt.Sprintf("%v", mapKeys(body)))
	update := make(map[string]any)
	if v, ok := body["coze_api_token"]; ok && v != "" {
		update["coze_api_token"] = v
	}
	if v, ok := body["coze_bot_id"]; ok && v != "" {
		update["coze_bot_id"] = v
	}
	if v, ok := body["proxy_api_key"]; ok {
		if v == "" {
			// Clear the proxy API key
			cfg.mu.Lock()
			delete(cfg.data, "proxy_api_key")
			cfg.data["updated_at"] = time.Now().Format(time.RFC3339)
			cfg.mu.Unlock()
			cfg.save()
		} else {
			update["proxy_api_key"] = v
		}
	}
	// Allow generic keys to be set (public_url, service_port, etc.)
	genericKeys := []string{"public_url", "service_port", "node_name", "region"}
	for _, k := range genericKeys {
		if v, ok := body[k]; ok {
			update[k] = v
		}
	}
	if len(update) == 0 && body["proxy_api_key"] == "" {
		// Only proxy_api_key clear was sent, already handled
		writeJSON(w, 200, cfg.Masked())
		return
	}
	if len(update) == 0 {
		writeError(w, 400, "at least one config field required")
		return
	}
	// Invalidate cached self addresses when public_url or service_port changes
	if _, ok := update["public_url"]; ok {
		cachedSelfAddresses = nil
	}
	if _, ok := update["service_port"]; ok {
		cachedSelfAddresses = nil
	}
	cfg.SetMany(update)
	writeJSON(w, 200, cfg.Masked())
}

// ============================================================
// Provider handlers
// ============================================================

func handleListProviders(w http.ResponseWriter, r *http.Request) {
	owner := getRequestOwner(r)
	writeJSON(w, 200, map[string]any{"providers": pm.GetVisible(owner)})
}

func handleGetPresets(w http.ResponseWriter, r *http.Request) {
	var presets []map[string]any
	for _, p := range presetProviders {
		presets = append(presets, map[string]any{
			"id": p.ID, "name": p.Name, "type": p.Type,
			"base_url": p.BaseURL, "description": p.Description,
			"icon": p.Icon, "default_models": p.Models,
			"api_key_url": p.APIKeyURL,
		})
	}
	writeJSON(w, 200, map[string]any{"presets": presets})
}

func handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	// Extract ID
	id, _ := body["id"].(string)
	if id == "" {
		writeError(w, 400, "provider ID required")
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
					if mid, ok := mv["id"].(string); ok { md.ID = mid }
					if mname, ok := mv["name"].(string); ok { md.Name = mname }
					if menabled, ok := mv["enabled"].(bool); ok { md.Enabled = menabled }
					models = append(models, md)
				}
			}
		}
	}
	// Remove models from body to avoid unmarshal type conflict
	delete(body, "models")

	// Unmarshal remaining fields into Provider
	bodyJSON, _ := json.Marshal(body)
	var p Provider
	if err := json.Unmarshal(bodyJSON, &p); err != nil {
		writeError(w, 400, "invalid provider data: "+err.Error())
		return
	}
	p.ID = id
	if len(models) > 0 {
		p.Models = models
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
		p.AccessControl.AllowGuest = existing.AccessControl.AllowGuest
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
	healthChecker.CheckProviderNow(p.ID)
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
	if err := readJSON(r, &updates); err != nil {
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

	b, _ := json.Marshal(existing)
	var merged Provider
	json.Unmarshal(b, &merged)
	// Apply updates via re-serialization
	b2, _ := json.Marshal(updates)
	json.Unmarshal(b2, &merged)
	merged.ID = id
	// Preserve ownership — consumer cannot change owner
	merged.Owner = existing.Owner

	// Validate VMess proxy link if changed
	if merged.Proxy != "" && merged.Proxy != existing.Proxy {
		if strings.HasPrefix(merged.Proxy, "vmess://") {
			if _, err := ParseVMessLink(merged.Proxy); err != nil {
				writeError(w, 400, "Invalid VMess link: "+err.Error())
				return
			}
			if _, err := ResolveProxy(id, merged.Proxy); err != nil {
				slog.Warn("failed to start VMess proxy", "provider", id, "error", err)
				writeError(w, 400, "VMess proxy failed: "+err.Error())
				return
			}
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
	healthChecker.CheckProviderNow(id)
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
			"index":    i + 1,
			"key_id":   key.ID,
			"alias":    key.Alias,
			"enabled":  key.Enabled,
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
		
		results = append(results, keyResult)
	}
	
	response := map[string]any{
		"success": allSuccess,
		"results": results,
		"total":   len(results),
	}
	
	if !allSuccess {
		failedCount := 0
		for _, r := range results {
			if s, ok := r["success"].(bool); !ok || !s {
				failedCount++
			}
		}
		response["failed_count"] = failedCount
	}
	
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

// ============================================================
// Provider model sync handler
// ============================================================

func handleSyncModels(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := pm.GetRaw(id)
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
	if err := readJSON(r, &ac); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	// Normalize: if both false, default to guest+shared
	if !ac.AllowGuest && !ac.ShareToPool {
		ac = DefaultAccessControl()
	}

	p.AccessControl = ac
	pm.Add(p)

	slog.Info("provider access control updated", "provider", id, "allow_guest", ac.AllowGuest, "share_to_pool", ac.ShareToPool)
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

// ============================================================
// Usage & Routing handlers
// ============================================================

func handleUsageSummary(w http.ResponseWriter, r *http.Request) {
	stats30 := tracker.ProviderStats(30)
	stats1 := tracker.ProviderStats(1)
	totalReqs30, totalTok30, totalCost30 := 0, 0, 0.0
	totalReqs1, totalTok1, totalCost1 := 0, 0, 0.0
	for _, s := range stats30 {
		totalReqs30 += s["request_count"].(int)
		totalTok30 += s["total_tokens"].(int)
		totalCost30 += s["total_cost_usd"].(float64)
	}
	for _, s := range stats1 {
		totalReqs1 += s["request_count"].(int)
		totalTok1 += s["total_tokens"].(int)
		totalCost1 += s["total_cost_usd"].(float64)
	}
	writeJSON(w, 200, map[string]any{
		"today_requests":      totalReqs1,
		"today_tokens":        totalTok1,
		"today_cost_usd":      round4(totalCost1),
		"total_requests_30d":  totalReqs30,
		"total_tokens_30d":    totalTok30,
		"total_cost_usd_30d":  round4(totalCost30),
		"providers_active":    len(stats30),
		"total_records":       len(tracker.records),
	})
}

func handleUsageProviders(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days == 0 {
		days = 30
	}
	writeJSON(w, 200, tracker.ProviderStats(days))
}

func handleUsageRecords(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit == 0 || limit > 500 {
		limit = 100
	}
	tracker.mu.Lock()
	recs := tracker.records
	if len(recs) > limit {
		recs = recs[len(recs)-limit:]
	}
	tracker.mu.Unlock()
	if recs == nil {
		recs = make([]UsageRecord, 0)
	}
	writeJSON(w, 200, map[string]any{"records": recs})
}

func handleUsageReset(w http.ResponseWriter, r *http.Request) {
	tracker.Reset()
	writeJSON(w, 200, map[string]any{"success": true, "message": "usage records cleared"})
}

func handleGetRoutingMode(w http.ResponseWriter, r *http.Request) {
	mode := cfg.Get("routing_mode", "priority")
	modes := map[string]map[string]string{
		"priority": {"id": "priority", "name": "🎯 优先级优先", "desc": "按预设优先级选择 Provider"},
		"cheapest": {"id": "cheapest", "name": "💰 成本最低", "desc": "按平台×模型定价选择最便宜的平台"},
		"fastest":  {"id": "fastest", "name": "⚡ 速度最快", "desc": "根据 EWMA 历史响应时间选择最快的平台"},
		"auto":     {"id": "auto", "name": "🧠 综合权重", "desc": "加权融合优先级+成本+延迟+剩余token"},
	}
	current := modes[mode]
	if current == nil {
		current = modes["priority"]
	}
	var available []map[string]string
	for _, m := range []string{"priority", "cheapest", "fastest", "auto"} {
		available = append(available, modes[m])
	}
	writeJSON(w, 200, map[string]any{"current": current, "available": available})
}

func handleSetRoutingMode(w http.ResponseWriter, r *http.Request) {
	var body struct{ Mode string `json:"mode"` }
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	valid := map[string]bool{"priority": true, "cheapest": true, "fastest": true, "auto": true}
	if !valid[body.Mode] {
		writeError(w, 400, "invalid routing mode")
		return
	}
	cfg.Set("routing_mode", body.Mode)
	writeJSON(w, 200, map[string]any{"success": true, "mode": body.Mode})
}

func handleGetRoutingWeights(w http.ResponseWriter, r *http.Request) {
	weights := pm.getWeights()
	writeJSON(w, 200, weights)
}


func handleSetRoutingWeights(w http.ResponseWriter, r *http.Request) {
	var body map[string]float64
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	weights := map[string]float64{
		"priority": clamp(body["priority"], 0, 1),
		"cost":     clamp(body["cost"], 0, 1),
		"latency":  clamp(body["latency"], 0, 1),
		"tokens":   clamp(body["tokens"], 0, 1),
	}
	b, _ := json.Marshal(weights)
	cfg.Set("routing_weights", string(b))
	writeJSON(w, 200, map[string]any{"success": true, "weights": weights})
}

func handleRoutingAdvice(w http.ResponseWriter, r *http.Request) {
	model := r.PathValue("model")
	advice := pm.RoutingAdvice(model)
	writeJSON(w, 200, map[string]any{"model": model, "candidates": advice, "count": len(advice)})
}

// ============================================================
// SMTP handlers
// ============================================================

func handleSMTPStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]bool{"configured": auth.IsSMTPConfigured()})
}

func handleGetSMTPConfig(w http.ResponseWriter, r *http.Request) {
	s := auth.GetSMTP()
	if s.Password != "" {
		s.Password = "****"
	}
	writeJSON(w, 200, s)
}

func handleSaveSMTPConfig(w http.ResponseWriter, r *http.Request) {
	var s SMTPConfig
	if err := readJSON(r, &s); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if s.Port == 0 {
		s.Port = 587
	}
	auth.UpdateSMTP(s)
	writeJSON(w, 200, map[string]any{"success": true, "message": "SMTP config saved"})
}

// ============================================================
// Request Logs & Health
// ============================================================

func handleRequestLogs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	logs := tracker.GetRequestLog(limit)
	writeJSON(w, 200, map[string]any{"logs": logs, "count": len(logs)})
}

func handleHealthStatus(w http.ResponseWriter, r *http.Request) {
	health := healthChecker.GetHealth()

	// Get today's usage stats
	todayStats := tracker.ProviderStats(1)

	type EnrichedHealth struct {
		ProviderID       string  `json:"provider_id"`
		ProviderName     string  `json:"provider_name"`
		Type             string  `json:"type"`
		Status           string  `json:"status"`
		LatencyMS        float64 `json:"latency_ms"`
		ConsecutiveFails int     `json:"consecutive_fails"`
		FailureReason    string  `json:"failure_reason,omitempty"`
		ModelCount        int     `json:"model_count"`
		TotalModelCount   int     `json:"total_model_count"`
		PrivateModelCount int     `json:"private_model_count"`
		SharedModelCount  int     `json:"shared_model_count"`
		TokenLimit       int64   `json:"token_limit"`
		TokenUsed        int64   `json:"token_used"`
		TodayRequests    int     `json:"today_requests"`
		TodayTokens      int     `json:"today_tokens"`
		KeyCount         int     `json:"key_count"`
		PrivateKeyCount  int     `json:"private_key_count"`
		SharedKeyCount   int     `json:"shared_key_count"`
		Enabled          bool    `json:"enabled"`
		Priority         int     `json:"priority"`
		IsShared         bool    `json:"is_shared"`
		SuccessRate      *float64 `json:"success_rate"`
		Models           []ModelDef             `json:"models"`
		AccessControl    *ProviderAccessControl `json:"access_control"`
		// Quota fields (placeholder — zero until quota tracking is implemented)
		QuotaPrivateUsed  int64 `json:"quota_private_used"`
		QuotaPrivateTotal int64 `json:"quota_private_total"`
		QuotaPublicUsed   int64 `json:"quota_public_used"`
		QuotaPublicTotal  int64 `json:"quota_public_total"`
		QuotaGuestUsed    int64 `json:"quota_guest_used"`
		QuotaGuestTotal   int64 `json:"quota_guest_total"`
		// Per-pool today stats (placeholder)
		TodayReqsPrivate   int `json:"today_reqs_private"`
		TodayTokensPrivate int `json:"today_tokens_private"`
		TodayReqsPublic    int `json:"today_reqs_public"`
		TodayTokensPublic  int `json:"today_tokens_public"`
		TodayReqsGuest     int `json:"today_reqs_guest"`
		TodayTokensGuest   int `json:"today_tokens_guest"`
		// Connection tracking
		ActiveConns int `json:"active_conns"`
	}

	// Build health lookup from checker results
	healthMap := make(map[string]ProviderHealth)
	for _, h := range health {
		healthMap[h.ProviderID] = h
	}

	// Iterate all configured providers that have keys
	configured := pm.GetConfigured()
	enriched := make([]EnrichedHealth, 0, len(configured))
	for _, p := range configured {
		hasKey := (p.APIKey != "" && p.APIKey != "your-api-key-here") || len(p.APIKeys) > 0
		if !hasKey {
			continue
		}

		h, hasHealth := healthMap[p.ID]

		// Get today's usage from stats
		todayReqs := 0
		todayTokens := 0
		if stats, ok := todayStats[p.ID]; ok {
			if count, ok := stats["request_count"].(int); ok {
				todayReqs = count
			}
			if tokens, ok := stats["total_tokens"].(int); ok {
				todayTokens = tokens
			}
		}

		// Get total usage
		totalUsed := tracker.TotalTokensByProvider()[p.ID]

		keyCount := len(p.APIKeys)
		if keyCount == 0 && p.APIKey != "" && p.APIKey != "your-api-key-here" {
			keyCount = 1
		}

		// Count private vs shared keys
		privateKeyCount := 0
		sharedKeyCount := 0
		for _, k := range p.APIKeys {
			if k.AccessControl == "shared" {
				sharedKeyCount++
			} else {
				privateKeyCount++
			}
		}
		// Legacy single key counts as private
		if len(p.APIKeys) == 0 && p.APIKey != "" && p.APIKey != "your-api-key-here" {
			privateKeyCount = 1
		}

		// Count only enabled models
		enabledModelCount := 0
		for _, m := range p.Models {
			if m.Enabled {
				enabledModelCount++
			}
		}
		// Private/shared model counts: based on per-key access_control and EnabledByKeys matrix
		privateModelCount := 0
		sharedModelCount := 0
		// Build keyID -> accessControl map
		keyAccessMap := make(map[string]string)
		for _, k := range p.APIKeys {
			if k.Enabled {
				keyAccessMap[k.ID] = k.AccessControl
			}
		}
		// Count models based on which keys have them enabled
		for _, m := range p.Models {
			if len(m.EnabledByKeys) == 0 {
				// Legacy: no per-key config, use overall Enabled flag
				if m.Enabled {
					if privateKeyCount > 0 {
						privateModelCount++
					}
					if sharedKeyCount > 0 {
						sharedModelCount++
					}
				}
				continue
			}
			isPrivate := false
			isShared := false
			for keyID, enabled := range m.EnabledByKeys {
				if !enabled {
					continue
				}
				access := keyAccessMap[keyID]
				if access == "private" {
					isPrivate = true
				} else if access == "shared" {
					isShared = true
				}
			}
			if isPrivate {
				privateModelCount++
			}
			if isShared {
				sharedModelCount++
			}
		}

		// IsShared: provider is shared if share_to_pool is true
		isShared := p.AccessControl.ShareToPool

		// Access control (pass through for frontend guest_pool_percent etc.)
		ac := p.AccessControl

		// Aggregate per-key quota by access type (private / shared)
		var quotaPrivUsed, quotaPrivTotal int64
		var quotaPubUsed, quotaPubTotal int64
		for _, k := range p.APIKeys {
			if !k.Enabled {
				continue
			}
			switch k.AccessControl {
			case "private":
				if k.Quota > 0 {
					quotaPrivTotal += k.Quota
				}
				quotaPrivUsed += k.Used
			case "shared":
				if k.Quota > 0 {
					quotaPubTotal += k.Quota
				}
				quotaPubUsed += k.Used
			}
		}
		// Legacy single key (no APIKeys) counts as private
		if len(p.APIKeys) == 0 && p.APIKey != "" && p.APIKey != "your-api-key-here" {
			if p.TokenLimit > 0 {
				quotaPrivTotal = p.TokenLimit
			}
			quotaPrivUsed = totalUsed
		}

		enriched = append(enriched, EnrichedHealth{
			ProviderID:       p.ID,
			ProviderName:     p.Name,
			Type:             p.Type,
			Status:           func() string { if hasHealth { return h.Status }; return "pending" }(),
			LatencyMS:        func() float64 { if hasHealth { return h.LatencyMS }; return 0 }(),
			ConsecutiveFails: func() int { if hasHealth { return h.ConsecutiveFails }; return 0 }(),
			FailureReason:    func() string { if hasHealth { return h.FailureReason }; return "" }(),
			ModelCount:        enabledModelCount,
			TotalModelCount:   len(p.Models),
			PrivateModelCount: privateModelCount,
			SharedModelCount:  sharedModelCount,
			TokenLimit:       p.TokenLimit,
			TokenUsed:        totalUsed,
			TodayRequests:    todayReqs,
			TodayTokens:      todayTokens,
			KeyCount:         keyCount,
			PrivateKeyCount:  privateKeyCount,
			SharedKeyCount:   sharedKeyCount,
			Enabled:          p.Enabled,
			Priority:         p.Priority,
			IsShared:         isShared,
			SuccessRate:      nil, // placeholder: not yet tracked
			Models:           p.Models,
			AccessControl:       &ac,
			ActiveConns:         GetProviderConns(p.ID),
			QuotaPrivateUsed:    quotaPrivUsed,
			QuotaPrivateTotal:   quotaPrivTotal,
			QuotaPublicUsed:     quotaPubUsed,
			QuotaPublicTotal:    quotaPubTotal,
		})
	}

	// Aggregate node_stats from enriched providers
	var totalProviders, onlineProviders, totalKeys, privateKeyCount, sharedKeyCount int
	var totalModels, enabledModels, privateModels, sharedModels int
	var totalLatency float64
	var latencyCount, successCount int
	var successSum float64
	var todayReqsPrivate, todayTokensPrivate int
	var todayReqsPublic, todayTokensPublic int
	var todayReqsGuest, todayTokensGuest int
	var quotaPrivUsed, quotaPrivTotal, quotaPubUsed, quotaPubTotal, quotaGuestUsed, quotaGuestTotal int64

	for _, ep := range enriched {
		totalProviders++
		if ep.Status == "healthy" || ep.Status == "degraded" {
			onlineProviders++
		}
		totalKeys += ep.KeyCount
		privateKeyCount += ep.PrivateKeyCount
		sharedKeyCount += ep.SharedKeyCount
		totalModels += ep.TotalModelCount
		enabledModels += ep.ModelCount
		privateModels += ep.PrivateModelCount
		sharedModels += ep.SharedModelCount
		if ep.LatencyMS > 0 {
			totalLatency += ep.LatencyMS
			latencyCount++
		}
		if ep.SuccessRate != nil {
			successSum += *ep.SuccessRate
			successCount++
		}
		todayReqsPrivate += ep.TodayReqsPrivate
		todayTokensPrivate += ep.TodayTokensPrivate
		todayReqsPublic += ep.TodayReqsPublic
		todayTokensPublic += ep.TodayTokensPublic
		todayReqsGuest += ep.TodayReqsGuest
		todayTokensGuest += ep.TodayTokensGuest
		quotaPrivUsed += ep.QuotaPrivateUsed
		quotaPrivTotal += ep.QuotaPrivateTotal
		quotaPubUsed += ep.QuotaPublicUsed
		quotaPubTotal += ep.QuotaPublicTotal
		quotaGuestUsed += ep.QuotaGuestUsed
		quotaGuestTotal += ep.QuotaGuestTotal
	}

	var avgLatency *float64
	if latencyCount > 0 {
		v := totalLatency / float64(latencyCount)
		avgLatency = &v
	}
	var avgSuccessRate *float64
	if successCount > 0 {
		v := successSum / float64(successCount)
		avgSuccessRate = &v
	}

	var totalConns, connsPrivate, connsPublic, connsGuest int
	for _, ep := range enriched {
		totalConns += ep.ActiveConns
		if ep.IsShared {
			connsPublic += ep.ActiveConns
		} else {
			connsPrivate += ep.ActiveConns
		}
	}
	connsGuest = 0 // placeholder: no per-guest connection tracking yet

	nodeStats := map[string]any{
		"provider_total":    totalProviders,
		"provider_online":   onlineProviders,
		"key_total":         totalKeys,
		"private_key_count": privateKeyCount,
		"shared_key_count":  sharedKeyCount,
		"model_total":       totalModels,
		"model_enabled":     enabledModels,
		"private_models":    privateModels,
		"shared_models":     sharedModels,
		"avg_latency":       avgLatency,
		"success_rate":      avgSuccessRate,
		"today_reqs_private":  todayReqsPrivate,
		"today_tokens_private": todayTokensPrivate,
		"today_reqs_public":   todayReqsPublic,
		"today_tokens_public":  todayTokensPublic,
		"today_reqs_guest":    todayReqsGuest,
		"today_tokens_guest":   todayTokensGuest,
		"quota_private_used":  quotaPrivUsed,
		"quota_private_total": quotaPrivTotal,
		"quota_public_used":   quotaPubUsed,
		"quota_public_total":  quotaPubTotal,
		"quota_guest_used":    quotaGuestUsed,
		"quota_guest_total":   quotaGuestTotal,
		"conns_private": connsPrivate,
		"conns_public":  connsPublic,
		"conns_guest":   connsGuest,
	}

	writeJSON(w, 200, map[string]any{"providers": enriched, "node_stats": nodeStats})
}

// ============================================================
// Static pages
// ============================================================

func handleAdminPage(w http.ResponseWriter, r *http.Request) {
	if !auth.Initialized() {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	// No server-side auth check — admin.html uses client-side auth
	// via authFetch() with Bearer token from localStorage.
	// This avoids redirect loops when cookies don't persist (e.g. behind tunnels).
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	http.ServeFile(w, r, "admin.html")
}

func handleSetupPage(w http.ResponseWriter, r *http.Request) {
	if auth.Initialized() {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	http.ServeFile(w, r, "setup.html")
}

func handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if !auth.Initialized() {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	http.ServeFile(w, r, "login.html")
}

// ============================================================
// Utility
// ============================================================

func clamp(v, min, max float64) float64 {
	if v < min { return min }
	if v > max { return max }
	return v
}

// ============================================================
// Sync URL handlers
// ============================================================

func handleSyncProviderURL(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := pm.GetRaw(id)
	if !ok {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}

	// Find matching preset
	var presetBaseURL string
	for _, preset := range presetProviders {
		if preset.ID == id {
			presetBaseURL = preset.BaseURL
			break
		}
	}

	if presetBaseURL == "" {
		writeJSON(w, 200, map[string]any{"changed": false, "message": "无匹配的预设平台，无法同步"})
		return
	}

	if p.BaseURL == presetBaseURL {
		writeJSON(w, 200, map[string]any{"changed": false, "message": "地址已是最新，无需更新"})
		return
	}

	oldURL := p.BaseURL
	p.BaseURL = presetBaseURL
	pm.Add(p)
	writeJSON(w, 200, map[string]any{
		"changed":  true,
		"message":  fmt.Sprintf("地址已从 %s 更新为 %s", oldURL, presetBaseURL),
		"old_url":  oldURL,
		"new_url":  presetBaseURL,
	})
}

func handleSyncAllURLs(w http.ResponseWriter, r *http.Request) {
	changed := 0
	allProviders := pm.GetAll()

	for _, p := range allProviders {
		if !p.Enabled {
			continue
		}
		var presetBaseURL string
		for _, preset := range presetProviders {
			if preset.ID == p.ID {
				presetBaseURL = preset.BaseURL
				break
			}
		}
		if presetBaseURL != "" && p.BaseURL != presetBaseURL {
			p.BaseURL = presetBaseURL
			pm.Add(p)
			changed++
		}
	}

	writeJSON(w, 200, map[string]any{"changed": changed, "total": len(allProviders)})
}

// ============================================================
// Status handler
// ============================================================

func handleStatus(w http.ResponseWriter, r *http.Request) {
	configured := pm.GetConfigured()
	withKey := 0
	for _, p := range configured {
		hasKey := (p.APIKey != "" && p.APIKey != "your-api-key-here") || len(p.APIKeys) > 0
		if hasKey {
			withKey++
		}
	}
	writeJSON(w, 200, map[string]any{
		"status":  "running",
		"version": AppVersion,
		"providers": map[string]any{
			"enabled":      withKey,
			"total":        len(configured),
			"preset_total": len(presetProviders),
		},
		"models": len(pm.AllModels()),
	})
}

// handleSMTPTest sends a test email using the configured SMTP settings.
func handleSMTPTest(w http.ResponseWriter, r *http.Request) {
	if !auth.IsSMTPConfigured() {
		writeError(w, 400, "SMTP not configured")
		return
	}
	s := auth.GetSMTP()
	adminEmail := auth.GetEmail()
	if adminEmail == "" {
		writeError(w, 400, "Admin email not set")
		return
	}

	// Build email message
	msg := []byte(fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: OpenModelPool Agent 测试邮件\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n这是一封来自 OpenModelPool Agent 的测试邮件。\r\n\r\n如果您收到此邮件，说明 SMTP 配置成功！", s.FromEmail, adminEmail))

	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	smtpAuth := smtp.PlainAuth("", s.Username, s.Password, s.Host)

	var err error
	if s.UseTLS && s.Port == 465 {
		// Implicit TLS (port 465)
		err = sendMailTLS(addr, smtpAuth, s.FromEmail, []string{adminEmail}, msg)
	} else {
		// STARTTLS or plain
		err = smtp.SendMail(addr, smtpAuth, s.FromEmail, []string{adminEmail}, msg)
	}

	if err != nil {
		writeJSON(w, 200, map[string]any{"success": false, "detail": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "message": "测试邮件已发送至 " + adminEmail})
}

// sendMailTLS sends email using implicit TLS (for port 465).
func sendMailTLS(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
	tlsConfig := &tls.Config{ServerName: strings.Split(addr, ":")[0]}
	conn, err := tls.Dial("tcp", addr, tlsConfig)
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, strings.Split(addr, ":")[0])
	if err != nil {
		return err
	}
	defer c.Close()
	if a != nil {
		if err = c.Auth(a); err != nil {
			return err
		}
	}
	if err = c.Mail(from); err != nil {
		return err
	}
	for _, addr := range to {
		if err = c.Rcpt(addr); err != nil {
			return err
		}
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	_, err = wc.Write(msg)
	if err != nil {
		return err
	}
	err = wc.Close()
	if err != nil {
		return err
	}
	return c.Quit()
}

// POST /api/auth/reset-with-code — reset password using an independent admin reset code.
// P0-2: Proxy API Key is NO LONGER used as the reset credential.
// Instead, a dedicated ResetCode is generated and stored in admin.json.
// The reset code can be generated via the "generate-reset-code" CLI command or
// from the admin panel when SMTP is unavailable.
func handleResetWithCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code        string `json:"code"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if body.Code == "" || body.NewPassword == "" {
		writeError(w, 400, "code and new_password required")
		return
	}
	if len(body.NewPassword) < 8 {
		writeError(w, 400, "password must be at least 8 characters")
		return
	}

	// P0-2: Validate against the independent ResetCode, NOT the Proxy API Key
	valid, err := auth.ValidateAndConsumeResetCode(body.Code)
	if err != nil || !valid {
		writeError(w, 401, "invalid or expired reset code")
		return
	}

	// Reset password
	hash, _ := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
	auth.mu.Lock()
	auth.data.Admin.PasswordHash = string(hash)
	auth.data.Reset = nil
	auth.mu.Unlock()
	auth.save()

	slog.Info("password reset via independent reset code")
	writeJSON(w, 200, map[string]any{
		"success": true,
		"message": "password reset successfully",
	})
}

// GET /api/config/export — export all configuration as JSON
func handleExportConfig(w http.ResponseWriter, r *http.Request) {
	smtpCfg := auth.GetSMTP()
	// Mask provider API keys in export
	maskedProviders := make([]map[string]any, 0)
	for _, p := range pm.GetAll() {
		sp := p.Safe()
		maskedProviders = append(maskedProviders, map[string]any{
			"id":          sp.ID,
			"name":        sp.Name,
			"type":        sp.Type,
			"base_url":    sp.BaseURL,
			"api_key":     sp.APIKey,
			"enabled":     sp.Enabled,
			"models":      sp.Models,
			"priority":    sp.Priority,
			"proxy":       sp.Proxy,
		})
	}
	export := map[string]any{
		"version":     "1.0",
		"exported_at": time.Now().Format(time.RFC3339),
		"providers":   maskedProviders,
		"config": map[string]any{
			"routing_mode":  cfg.Get("routing_mode", "priority"),
			"proxy_api_key": maskKey(cfg.Get("proxy_api_key", "")),
		},
		"smtp": map[string]any{
			"host":       smtpCfg.Host,
			"port":       smtpCfg.Port,
			"username":   smtpCfg.Username,
			"from_email": smtpCfg.FromEmail,
			"use_tls":    smtpCfg.UseTLS,
			// Don't export SMTP password for security
		},
		"admin": func() map[string]any {
			info := auth.AdminInfo()
			return map[string]any{
				"username": info["username"],
				"email":    info["email"],
			}
		}(),
	}
	writeJSON(w, 200, export)
}

// POST /api/config/import — import configuration from JSON
func handleImportConfig(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeError(w, 400, "failed to parse form data")
		return
	}

	file, _, err := r.FormFile("config")
	if err != nil {
		writeError(w, 400, "missing config file")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, 400, "failed to read file")
		return
	}

	var importData struct {
		Providers []Provider `json:"providers"`
		Config    struct {
			RoutingMode string `json:"routing_mode"`
			ProxyAPIKey string `json:"proxy_api_key"`
		} `json:"config"`
		SMTP struct {
			Host      string `json:"host"`
			Port      int    `json:"port"`
			Username  string `json:"username"`
			FromEmail string `json:"from_email"`
			UseTLS    bool   `json:"use_tls"`
		} `json:"smtp"`
		Admin struct {
			Email string `json:"email"`
		} `json:"admin"`
	}

	if err := json.Unmarshal(data, &importData); err != nil {
		writeError(w, 400, "invalid config format: "+err.Error())
		return
	}

	// Import providers
	if importData.Providers != nil {
		pm.mu.Lock()
		pm.providers = make(map[string]Provider)
		for _, p := range importData.Providers {
			if p.ID == "" {
				p.ID = strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(p.Name, " ", "-"), "_", "-"))
			}
			pm.providers[p.ID] = p
		}
		pm.save()
		pm.mu.Unlock()
	}

	// Import config
	updates := make(map[string]any)
	if importData.Config.RoutingMode != "" {
		updates["routing_mode"] = importData.Config.RoutingMode
	}
	if importData.Config.ProxyAPIKey != "" {
		updates["proxy_api_key"] = importData.Config.ProxyAPIKey
	}
	if len(updates) > 0 {
		cfg.SetMany(updates)
	}

	// Import SMTP (without password)
	if importData.SMTP.Host != "" {
		smtpCfg := auth.GetSMTP()
		smtpCfg.Host = importData.SMTP.Host
		smtpCfg.Port = importData.SMTP.Port
		smtpCfg.Username = importData.SMTP.Username
		smtpCfg.FromEmail = importData.SMTP.FromEmail
		smtpCfg.UseTLS = importData.SMTP.UseTLS
		auth.UpdateSMTP(smtpCfg)
	}

	// Import admin email
	if importData.Admin.Email != "" {
		auth.UpdateEmail(importData.Admin.Email)
	}

	writeJSON(w, 200, map[string]any{
		"success":         true,
		"message":         "config imported successfully",
		"providers_count": len(importData.Providers),
	})
}

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
	if err := readJSON(r, &key); err != nil {
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
	if err := readJSON(r, &updates); err != nil {
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
// ============================================================

// ============================================================
// Service Restart
// ============================================================

func handleRestart(w http.ResponseWriter, r *http.Request) {
	slog.Info("Restart requested via admin API")
	writeJSON(w, 200, map[string]any{"success": true, "message": "Service restarting..."})
	
	go func() {
		time.Sleep(500 * time.Millisecond)
		pid := os.Getpid()
		slog.Info("Initiating restart", "current_pid", pid)
		
		// Run restart script with current PID
		cmd := exec.Command("bash", "./restart.sh", fmt.Sprintf("%d", pid))
		cmd.Dir = "."
		cmd.Start()
		
		// Current process will be killed by the script
	}()
}

// handleRefreshToken accepts a refresh_token and returns a new access_token.
// S-3: JWT refresh token flow.
func handleRefreshToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if body.RefreshToken == "" {
		writeError(w, 400, "refresh_token is required")
		return
	}
	newAccessToken, err := auth.RefreshAccessToken(body.RefreshToken)
	if err != nil {
		writeJSON(w, 401, map[string]string{"error": "invalid or expired refresh token"})
		return
	}
	writeJSON(w, 200, map[string]string{
		"access_token": newAccessToken,
		"token_type":   "bearer",
	})
}

// ============================================================
// Collaborator Registration API (public, no auth required)
// ============================================================

// GET /api/collaborator/check-key?key=xxx — validate guest key for registration
func handleCollaboratorCheckKey(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		writeError(w, 400, "key parameter required")
		return
	}
	valid := auth.ValidateGuestKeyForRegistration(key)
	writeJSON(w, 200, map[string]any{"valid": valid})
}

// POST /api/collaborator/register — register collaborator account
func handleCollaboratorRegister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		GuestKey string `json:"guest_key"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if body.Username == "" || body.Password == "" || body.GuestKey == "" {
		writeError(w, 400, "username, password and guest_key are required")
		return
	}
	if !auth.ValidateGuestKeyForRegistration(body.GuestKey) {
		writeError(w, 400, "invalid or already used guest key")
		return
	}
	if err := auth.RegisterCollaborator(body.Username, body.Password, body.GuestKey); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	// Mark guest key as collaborator type
	if guestKeyStore != nil {
		guestKeyStore.SetShareType(body.GuestKey, "collaborator")
		guestKeyStore.MarkAsCollaborator(body.GuestKey)
	}
	// Create JWT token for auto-login
	accessToken, _ := auth.CreateToken(body.Username, true)
	writeJSON(w, 200, map[string]any{
		"success":      true,
		"access_token": accessToken,
		"role":         "collaborator",
		"username":     body.Username,
	})
}
