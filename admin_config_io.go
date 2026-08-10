package main

import (
	"encoding/json"
	"golang.org/x/crypto/bcrypt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

func handleResetWithCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code        string `json:"code"`
		NewPassword string `json:"new_password"`
	}
	if err := readJSON(w, r, &body); err != nil {
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
	if len(body.NewPassword) > 256 { // B9: max password length
		writeError(w, 400, "password too long")
		return
	}

	// P0-2: Validate against the independent ResetCode, NOT the Proxy API Key
	valid, err := auth.ValidateAndConsumeResetCode(body.Code)
	if err != nil || !valid {
		writeError(w, 401, "invalid or expired reset code")
		return
	}

	// Reset password
	hash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		slog.Error("failed to hash password", "error", err)
		writeError(w, 500, "internal error")
		return
	}
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
			"id":       sp.ID,
			"name":     sp.Name,
			"type":     sp.Type,
			"base_url": sp.BaseURL,
			"api_key":  sp.APIKey,
			"enabled":  sp.Enabled,
			"models":   sp.Models,
			"priority": sp.Priority,
			"proxy":    sp.Proxy,
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
		writeError(w, 400, "invalid config format")
		return
	}

	// Import providers (SEC-P2-18): validate every BaseURL with the same
	// scheme + SSRF guard as the create path, and MERGE (upsert) instead of
	// truncating the whole provider set. Masked values from an export keep the
	// existing live secrets.
	if importData.Providers != nil {
		// QA-minor: detect which import entries explicitly carry a
		// access_control.share_to_pool value. Unlike the masked string fields
		// above (which have a presence/"..." mask signal), ShareToPool is a
		// bool — absent and false are otherwise indistinguishable. The official
		// export omits share_to_pool, so a round-trip import must preserve the
		// existing setting; only an explicit value in the import file is
		// honored.
		rawProviders := rawProvidersWithShareToPool(data)

		pm.mu.Lock()
		if pm.providers == nil {
			pm.providers = make(map[string]Provider)
		}
		for i, p := range importData.Providers {
			if p.ID == "" {
				p.ID = strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(p.Name, " ", "-"), "_", "-"))
			}
			if err := validateProviderBaseURL(p.BaseURL); err != nil {
				pm.mu.Unlock()
				writeError(w, 400, "provider '"+p.ID+"': "+err.Error())
				return
			}
			if existing, ok := pm.providers[p.ID]; ok {
				if p.APIKey == "" || strings.Contains(p.APIKey, "...") {
					p.APIKey = existing.APIKey
				}
				if p.Proxy == "" || p.Proxy == "vmess://***" {
					p.Proxy = existing.Proxy
				}
				if len(p.APIKeys) == 0 {
					p.APIKeys = existing.APIKeys
				}
				// Preserve the existing share_to_pool unless the import file
				// explicitly sets it (see rawProvidersWithShareToPool).
				if len(rawProviders) <= i || !rawProviders[i] {
					p.AccessControl.ShareToPool = existing.AccessControl.ShareToPool
				}
			}
			pm.providers[p.ID] = p
		}
		pm.mu.Unlock()

		// QA blocking fix: pm.save() re-acquires pm.mu via RLock; Go's RWMutex
		// is NOT reentrant, so calling save() while holding the write lock
		// self-deadlocks and blocks every provider operation. Always persist
		// AFTER releasing the write lock.
		pm.save()
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

// rawProvidersWithShareToPool reports, per top-level "providers" entry in the
// raw import payload (in array order), whether that entry explicitly carries an
// access_control.share_to_pool value. Provider binds ShareToPool ONLY under
// "access_control" (types.go Provider.AccessControl json:"access_control"), so
// a top-level "share_to_pool" key would be silently ignored by unmarshal and is
// therefore NOT treated as explicit. Used to honor an explicit import value
// while preserving the existing setting on round-trips (the official export
// omits share_to_pool, so absent must mean "keep the current value", not "turn
// sharing off").
func rawProvidersWithShareToPool(data []byte) []bool {
	var holder struct {
		Providers []map[string]json.RawMessage `json:"providers"`
	}
	if err := json.Unmarshal(data, &holder); err != nil {
		return nil
	}
	out := make([]bool, len(holder.Providers))
	for i, raw := range holder.Providers {
		acRaw, ok := raw["access_control"]
		if !ok {
			continue
		}
		var ac map[string]json.RawMessage
		if json.Unmarshal(acRaw, &ac) == nil {
			if _, ok := ac["share_to_pool"]; ok {
				out[i] = true
			}
		}
	}
	return out
}