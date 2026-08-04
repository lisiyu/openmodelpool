package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ============================================================
// Admin handlers
// ============================================================

func handleAdminInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, auth.AdminInfo())
}

// isValidID checks if a string is a valid identifier (alphanumeric, dash, underscore).
// B156: Used for provider ID validation.
func isValidID(id string) bool {
	for _, ch := range id {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_') {
			return false
		}
	}
	return true
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
	if err := readJSON(w, r, &body); err != nil {
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
	var body struct {
		Email string `json:"email"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	// B62: Validate email format
	if body.Email != "" && !strings.Contains(body.Email, "@") {
		writeError(w, 400, "invalid email format")
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
	if err := readJSON(w, r, &body); err != nil {
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
			// B60: Validate service_port is a valid port number
			if k == "service_port" {
				port, err := strconv.Atoi(v)
				if err != nil || port < 1 || port > 65535 {
					writeError(w, 400, "service_port must be a valid port number (1-65535)")
					return
				}
			}
			// B61: Validate public_url is a valid http/https URL
			if k == "public_url" && v != "" {
				u, err := url.Parse(v)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					writeError(w, 400, "public_url must be a valid http/https URL")
					return
				}
			}
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
		cachedSelfAddressesMu.Lock()
		cachedSelfAddresses = nil
		cachedSelfAddressesMu.Unlock()
	}
	if _, ok := update["service_port"]; ok {
		cachedSelfAddressesMu.Lock()
		cachedSelfAddresses = nil
		cachedSelfAddressesMu.Unlock()
	}
	cfg.SetMany(update)
	// §10A: re-read WAF configuration if any WAF keys were updated.
	reloadWAF()
	writeJSON(w, 200, cfg.Masked())
}

// ============================================================
// Gateway mark handlers
// ============================================================

// handleGetGateway returns whether this node is marked as a Gateway
// (network entry node). GET /api/gateway
func handleGetGateway(w http.ResponseWriter, r *http.Request) {
	isGateway := cfg.Get("is_gateway", "false") == "true"
	writeJSON(w, 200, map[string]any{"is_gateway": isGateway})
}

// handleSetGateway marks or unmarks this node as a Gateway.
// POST /api/gateway  body: {"is_gateway": bool}
func handleSetGateway(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IsGateway bool `json:"is_gateway"`
	}
	if err := readJSON(w, r, &body); err != nil {
		slog.Error("handleSetGateway: readJSON failed", "error", err)
		writeError(w, 400, "invalid JSON body: "+err.Error())
		return
	}
	if body.IsGateway {
		cfg.Set("is_gateway", "true")
	} else {
		cfg.Set("is_gateway", "false")
	}
	// Persist immediately so data/config.json reflects the mark without
	// waiting for the debounced background writer.
	cfg.saveSync()
	isGateway := cfg.Get("is_gateway", "false") == "true"
	slog.Info("gateway mark updated", "is_gateway", isGateway)
	writeJSON(w, 200, map[string]any{"success": true, "is_gateway": isGateway})
}