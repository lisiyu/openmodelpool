package main

import (
	"crypto/tls"
	"encoding/json"
	"log/slog"
	"fmt"
	"net/smtp"
	"strings"
	"net/http"
	"strconv"
	"time"
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
	token := auth.CreateToken(body.Username, body.Remember)
	http.SetCookie(w, &http.Cookie{
		Name:     "admin_token",
		Path:     "/",
		Value:    token,
		HttpOnly: true,
		MaxAge:   86400,
		SameSite: http.SameSiteLaxMode,
	})
	if body.Remember {
		http.SetCookie(w, &http.Cookie{
			Name:     "admin_token",
		Path:     "/",
			Value:    token,
			HttpOnly: true,
			MaxAge:   7 * 86400,
			SameSite: http.SameSiteLaxMode,
		})
	}
	writeJSON(w, 200, map[string]string{"access_token": token, "token_type": "bearer"})
}

func handleVerifyAuth(w http.ResponseWriter, r *http.Request) {
	token := extractToken(r)
	username, _ := auth.VerifyToken(token)
	writeJSON(w, 200, map[string]any{"valid": true, "username": username})
}

func handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	if !auth.Initialized() {
		writeError(w, 400, "system not initialized")
		return
	}
	var body struct{ Email string `json:"email"` }
	readJSON(r, &body)
	// Always return success to prevent email enumeration
	writeJSON(w, 200, map[string]any{"success": true, "message": "if the email exists, a reset link has been sent"})
}

func handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	readJSON(r, &body)
	if err := auth.ResetPassword(body.Token, body.NewPassword); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "message": "password reset"})
}

func handleVerifyResetToken(w http.ResponseWriter, r *http.Request) {
	var body struct{ Token string `json:"token"` }
	readJSON(r, &body)
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

func handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	readJSON(r, &body)
	if err := auth.ChangePassword(body.OldPassword, body.NewPassword); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "message": "password changed"})
}

func handleUpdateEmail(w http.ResponseWriter, r *http.Request) {
	var body struct{ Email string `json:"email"` }
	readJSON(r, &body)
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
	if len(update) == 0 && body["proxy_api_key"] == "" {
		// Only proxy_api_key clear was sent, already handled
		writeJSON(w, 200, cfg.Masked())
		return
	}
	if len(update) == 0 {
		writeError(w, 400, "at least one config field required")
		return
	}
	cfg.SetMany(update)
	writeJSON(w, 200, cfg.Masked())
}

// ============================================================
// Provider handlers
// ============================================================

func handleListProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"providers": pm.GetAll()})
}

func handleGetPresets(w http.ResponseWriter, r *http.Request) {
	var presets []map[string]any
	for _, p := range presetProviders {
		presets = append(presets, map[string]any{
			"id": p.ID, "name": p.Name, "type": p.Type,
			"base_url": p.BaseURL, "description": p.Description,
			"icon": p.Icon, "default_models": p.Models,
		})
	}
	writeJSON(w, 200, map[string]any{"presets": presets})
}

func handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	var p Provider
	if err := readJSON(r, &p); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if p.ID == "" {
		writeError(w, 400, "provider ID required")
		return
	}
	// Validate VMess proxy link
	if strings.HasPrefix(p.Proxy, "vmess://") {
		if _, err := ParseVMessLink(p.Proxy); err != nil {
			writeError(w, 400, "Invalid VMess link: "+err.Error())
			return
		}
		// Start the proxy (keep original link in provider)
		if _, err := ResolveProxy(p.ID, p.Proxy); err != nil {
			slog.Warn("failed to start VMess proxy", "provider", p.ID, "error", err)
			writeError(w, 400, "VMess proxy failed: "+err.Error())
			return
		}
	}
	result := pm.Add(p)
	writeJSON(w, 200, map[string]any{"success": true, "data": result})
}

func handleGetProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := pm.Get(id)
	if !ok {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}
	writeJSON(w, 200, p)
}

func handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, ok := pm.GetRaw(id)
	if !ok {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}
	var updates map[string]any
	readJSON(r, &updates)
	b, _ := json.Marshal(existing)
	var merged Provider
	json.Unmarshal(b, &merged)
	// Apply updates via re-serialization
	b2, _ := json.Marshal(updates)
	json.Unmarshal(b2, &merged)
	merged.ID = id

	// Validate VMess proxy link if changed
	if merged.Proxy != "" && merged.Proxy != existing.Proxy {
		if strings.HasPrefix(merged.Proxy, "vmess://") {
			// Validate the link by parsing it
			if _, err := ParseVMessLink(merged.Proxy); err != nil {
				writeError(w, 400, "Invalid VMess link: "+err.Error())
				return
			}
			// Try to start the proxy
			if _, err := ResolveProxy(id, merged.Proxy); err != nil {
				slog.Warn("failed to start VMess proxy", "provider", id, "error", err)
				writeError(w, 400, "VMess proxy failed: "+err.Error())
				return
			}
			// Keep the original vmess:// link (proxyHTTPClient resolves on-the-fly)
		}
	}
	
	result := pm.Add(merged)
	writeJSON(w, 200, map[string]any{"success": true, "data": result})
}

func handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !pm.Delete(id) {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}
	writeJSON(w, 200, map[string]bool{"success": true})
}

func handleTestProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := pm.GetRaw(id)
	if !ok {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}
	writeJSON(w, 200, testConnection(p))
}

func handleGetProviderModels(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := pm.GetRaw(id)
	if !ok {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}
	models := fetchRemoteModels(p)
	writeJSON(w, 200, map[string]any{"models": models, "count": len(models)})
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
	stats := tracker.ProviderStats(30)
	totalReqs, totalTok, totalCost := 0, 0, 0.0
	for _, s := range stats {
		totalReqs += s["request_count"].(int)
		totalTok += s["total_tokens"].(int)
		totalCost += s["total_cost_usd"].(float64)
	}
	writeJSON(w, 200, map[string]any{
		"total_requests_30d": totalReqs,
		"total_tokens_30d":   totalTok,
		"total_cost_usd_30d": round4(totalCost),
		"providers_active":   len(stats),
		"total_records":      len(tracker.records),
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
	readJSON(r, &body)
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
	readJSON(r, &body)
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
	readJSON(r, &s)
	if s.Port == 0 {
		s.Port = 587
	}
	auth.UpdateSMTP(s)
	writeJSON(w, 200, map[string]any{"success": true, "message": "SMTP config saved"})
}

// ============================================================
// Logs
// ============================================================

func handleLogs(w http.ResponseWriter, r *http.Request) {
	// Return empty for now - would need a log buffer like Python version
	writeJSON(w, 200, map[string]any{"logs": []string{}})
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
	all := pm.GetAll()
	enabled := 0
	for _, p := range all {
		if p.Enabled {
			enabled++
		}
	}
	writeJSON(w, 200, map[string]any{
		"status":  "running",
		"version": "1.0.0",
		"providers": map[string]any{
			"enabled": enabled,
			"total":   len(all),
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
	msg := []byte(fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: ModelMux 测试邮件\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n这是一封来自 ModelMux 的测试邮件。\r\n\r\n如果您收到此邮件，说明 SMTP 配置成功！", s.FromEmail, adminEmail))

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
