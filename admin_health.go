package main

import (
	"net/http"
	"strconv"
	"time"
)

// ============================================================
// Request Logs & Health
// ============================================================

func handleRequestLogs(w http.ResponseWriter, r *http.Request) {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 || limit > 500 {
		limit = 100
	}
	logs := tracker.GetRequestLog(limit)
	writeJSON(w, 200, map[string]any{"logs": logs, "count": len(logs)})
}

func handleHealthStatus(w http.ResponseWriter, r *http.Request) {
	var health []ProviderHealth
	if healthChecker != nil {
		health = healthChecker.GetHealth()
	}

	// Get today's usage stats
	todayStats := tracker.ProviderStats(1)

	// Get this month's usage stats (days since start of month)
	now := time.Now()
	daysInMonth := now.Day() // e.g., on Jan 15, this is 15
	monthlyStats := tracker.ProviderStats(daysInMonth)

	type EnrichedHealth struct {
		ProviderID        string                 `json:"provider_id"`
		ProviderName      string                 `json:"provider_name"`
		Type              string                 `json:"type"`
		Status            string                 `json:"status"`
		LatencyMS         float64                `json:"latency_ms"`
		ConsecutiveFails  int                    `json:"consecutive_fails"`
		FailureReason     string                 `json:"failure_reason,omitempty"`
		ModelCount        int                    `json:"model_count"`
		TotalModelCount   int                    `json:"total_model_count"`
		PrivateModelCount int                    `json:"private_model_count"`
		SharedModelCount  int                    `json:"shared_model_count"`
		TokenLimit        int64                  `json:"token_limit"`
		TokenUsed         int64                  `json:"token_used"`
		TodayRequests     int                    `json:"today_requests"`
		TodayTokens       int                    `json:"today_tokens"`
		KeyCount          int                    `json:"key_count"`
		PrivateKeyCount   int                    `json:"private_key_count"`
		SharedKeyCount    int                    `json:"shared_key_count"`
		FailedKeyCount    int                    `json:"failed_key_count"`
		Enabled           bool                   `json:"enabled"`
		RateLimitEnabled  bool                   `json:"rate_limit_enabled"`
		RateLimitPerMin   int                    `json:"rate_limit_per_min"`
		DailyRequestLimit int64                  `json:"daily_request_limit"`
		Priority          int                    `json:"priority"`
		IsShared          bool                   `json:"is_shared"`
		SuccessRate       *float64               `json:"success_rate"`
		Models            []ModelDef             `json:"models"`
		AccessControl     *ProviderAccessControl `json:"access_control"`
		// Quota fields: limits come from provider/key config; "used" tokens come
		// from per-key usage (multi-key) or the tracker (legacy single key).
		QuotaPrivateUsed  int64 `json:"quota_private_used"`
		QuotaPrivateTotal int64 `json:"quota_private_total"`
		QuotaPublicUsed   int64 `json:"quota_public_used"`
		QuotaPublicTotal  int64 `json:"quota_public_total"`
		QuotaGuestUsed    int64 `json:"quota_guest_used"`
		QuotaGuestTotal   int64 `json:"quota_guest_total"`
		// Daily/Monthly quota limits per pool
		QuotaPrivateDaily   int64 `json:"quota_private_daily"`
		QuotaPrivateMonthly int64 `json:"quota_private_monthly"`
		QuotaPublicDaily    int64 `json:"quota_public_daily"`
		QuotaPublicMonthly  int64 `json:"quota_public_monthly"`
		QuotaGuestDaily     int64 `json:"quota_guest_daily"`
		QuotaGuestMonthly   int64 `json:"quota_guest_monthly"`
		// Daily/Monthly usage per pool (from tracker)
		QuotaPrivateDailyUsed   int64 `json:"quota_private_daily_used"`
		QuotaPrivateMonthlyUsed int64 `json:"quota_private_monthly_used"`
		QuotaPublicDailyUsed    int64 `json:"quota_public_daily_used"`
		QuotaPublicMonthlyUsed  int64 `json:"quota_public_monthly_used"`
		QuotaGuestDailyUsed     int64 `json:"quota_guest_daily_used"`
		QuotaGuestMonthlyUsed   int64 `json:"quota_guest_monthly_used"`
		// Per-pool today stats derived from tracker.ProviderStats (split by access type).
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
		isWebSession := p.Type == "web_session"
		if !hasKey && !isWebSession {
			continue
		}

		h, hasHealth := healthMap[p.ID]

		// Get today's usage from stats
		todayReqs := 0
		todayTokens := 0
		todayPrivReqs, todayPubReqs, todayGuestReqs := 0, 0, 0
		todayPrivTokens, todayPubTokens, todayGuestTokens := 0, 0, 0
		if stats, ok := todayStats[p.ID]; ok {
			if count, ok := stats["request_count"].(int); ok {
				todayReqs = count
			}
			if tokens, ok := stats["total_tokens"].(int); ok {
				todayTokens = tokens
			}
			if v, ok := stats["private_reqs"].(int); ok {
				todayPrivReqs = v
			}
			if v, ok := stats["public_reqs"].(int); ok {
				todayPubReqs = v
			}
			if v, ok := stats["guest_reqs"].(int); ok {
				todayGuestReqs = v
			}
			if v, ok := stats["private_tokens"].(int); ok {
				todayPrivTokens = v
			}
			if v, ok := stats["public_tokens"].(int); ok {
				todayPubTokens = v
			}
			if v, ok := stats["guest_tokens"].(int); ok {
				todayGuestTokens = v
			}
		}

		// Get total usage
		totalUsed := tracker.TotalTokensByProvider()[p.ID]

		keyCount := 0
		for _, k := range p.APIKeys {
			if k.Enabled {
				keyCount++
			}
		}
		if keyCount == 0 && p.APIKey != "" && p.APIKey != "your-api-key-here" {
			keyCount = 1
		}

		// Count private vs shared keys
		privateKeyCount := 0
		sharedKeyCount := 0
		for _, k := range p.APIKeys {
			if !k.Enabled {
				continue
			}
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

		// Aggregate per-key quota by access type (private / shared → split into public + guest)
		var quotaPrivUsed, quotaPrivTotal int64
		var quotaPubUsed, quotaPubTotal int64
		var quotaGuestUsed, quotaGuestTotal int64

		// Guest pool percent for this provider (0-100, default 50)
		guestPct := p.AccessControl.GuestPoolPercent
		if guestPct <= 0 {
			guestPct = 50
		}
		if guestPct > 100 {
			guestPct = 100
		}
		publicPct := 100 - guestPct

		// Sum key-level used tokens for quota calculation
		for _, k := range p.APIKeys {
			if !k.Enabled {
				continue
			}
			switch k.AccessControl {
			case "private":
				quotaPrivUsed += k.Used
			case "shared":
				quotaPubUsed += k.Used * int64(publicPct) / 100
				quotaGuestUsed += k.Used * int64(guestPct) / 100
			}
		}
		// Legacy single key (no APIKeys) counts as private
		if len(p.APIKeys) == 0 && p.APIKey != "" && p.APIKey != "your-api-key-here" {
			quotaPrivUsed = totalUsed
		}

		// Use platform-level quota as the authoritative total for each pool
		// Take the minimum of all non-zero platform-level quotas as the effective limit
		if p.PrivateQuotaMonthly > 0 && p.PrivateQuotaTotal > 0 {
			if p.PrivateQuotaMonthly < p.PrivateQuotaTotal {
				quotaPrivTotal = p.PrivateQuotaMonthly
			} else {
				quotaPrivTotal = p.PrivateQuotaTotal
			}
		} else if p.PrivateQuotaMonthly > 0 {
			quotaPrivTotal = p.PrivateQuotaMonthly
		} else if p.PrivateQuotaTotal > 0 {
			quotaPrivTotal = p.PrivateQuotaTotal
		}
		// For private pool, if no platform-level quota set, fall back to summing key effective quotas
		if quotaPrivTotal == 0 {
			for _, k := range p.APIKeys {
				if !k.Enabled || k.AccessControl != "private" {
					continue
				}
				eq := k.Quota
				if k.QuotaMonthly > 0 && (eq == 0 || k.QuotaMonthly < eq) {
					eq = k.QuotaMonthly
				}
				if k.QuotaDaily > 0 && (eq == 0 || k.QuotaDaily < eq) {
					eq = k.QuotaDaily
				}
				if eq > 0 {
					quotaPrivTotal += eq
				}
			}
			// Legacy single key
			if len(p.APIKeys) == 0 && p.APIKey != "" && p.APIKey != "your-api-key-here" && p.TokenLimit > 0 {
				quotaPrivTotal = p.TokenLimit
			}
		}
		// Clamp used to total if total is set
		if quotaPrivTotal > 0 && quotaPrivUsed > quotaPrivTotal {
			quotaPrivUsed = quotaPrivTotal
		}

		// Shared pool: use platform-level quota
		var sharedTotal int64
		if p.SharedQuotaMonthly > 0 && p.SharedQuotaTotal > 0 {
			if p.SharedQuotaMonthly < p.SharedQuotaTotal {
				sharedTotal = p.SharedQuotaMonthly
			} else {
				sharedTotal = p.SharedQuotaTotal
			}
		} else if p.SharedQuotaMonthly > 0 {
			sharedTotal = p.SharedQuotaMonthly
		} else if p.SharedQuotaTotal > 0 {
			sharedTotal = p.SharedQuotaTotal
		}
		if sharedTotal == 0 {
			// Fall back to summing key effective quotas
			for _, k := range p.APIKeys {
				if !k.Enabled || k.AccessControl != "shared" {
					continue
				}
				eq := k.Quota
				if k.QuotaMonthly > 0 && (eq == 0 || k.QuotaMonthly < eq) {
					eq = k.QuotaMonthly
				}
				if k.QuotaDaily > 0 && (eq == 0 || k.QuotaDaily < eq) {
					eq = k.QuotaDaily
				}
				if eq > 0 {
					sharedTotal += eq
				}
			}
		}
		// Split shared total between public and guest pools
		if sharedTotal > 0 {
			quotaPubTotal = sharedTotal * int64(publicPct) / 100
			quotaGuestTotal = sharedTotal - quotaPubTotal
		}
		// Clamp used
		if quotaPubTotal > 0 && quotaPubUsed > quotaPubTotal {
			quotaPubUsed = quotaPubTotal
		}
		if quotaGuestTotal > 0 && quotaGuestUsed > quotaGuestTotal {
			quotaGuestUsed = quotaGuestTotal
		}

		// Compute daily/monthly usage from tracker, split by pool
		// Today's tokens for this provider
		dailyTotal := 0
		if stats, ok := todayStats[p.ID]; ok {
			if tokens, ok := stats["total_tokens"].(int); ok {
				dailyTotal = tokens
			}
		}
		// Monthly tokens for this provider
		monthlyTotal := 0
		if stats, ok := monthlyStats[p.ID]; ok {
			if tokens, ok := stats["total_tokens"].(int); ok {
				monthlyTotal = tokens
			}
		}

		// Split daily/monthly usage by pool based on key access_control ratios
		var dailyPrivUsed, dailyPubUsed, dailyGuestUsed int64
		var monthlyPrivUsed, monthlyPubUsed, monthlyGuestUsed int64

		if privateKeyCount+sharedKeyCount > 0 {
			privRatio := float64(privateKeyCount) / float64(privateKeyCount+sharedKeyCount)
			sharedRatio := float64(sharedKeyCount) / float64(privateKeyCount+sharedKeyCount)

			dailyPrivUsed = int64(float64(dailyTotal) * privRatio)
			dailySharedUsed := int64(float64(dailyTotal) * sharedRatio)
			dailyPubUsed = dailySharedUsed * int64(publicPct) / 100
			dailyGuestUsed = dailySharedUsed * int64(guestPct) / 100

			monthlyPrivUsed = int64(float64(monthlyTotal) * privRatio)
			monthlySharedUsed := int64(float64(monthlyTotal) * sharedRatio)
			monthlyPubUsed = monthlySharedUsed * int64(publicPct) / 100
			monthlyGuestUsed = monthlySharedUsed * int64(guestPct) / 100
		} else if len(p.APIKeys) == 0 && p.APIKey != "" && p.APIKey != "your-api-key-here" {
			// Legacy single key counts as private
			dailyPrivUsed = int64(dailyTotal)
			monthlyPrivUsed = int64(monthlyTotal)
		}

		// Real reliability metric: success rate over the current month (broadest
		// window the tracker keeps in memory). Only set when there is actual
		// traffic; otherwise leave nil ("no data") to avoid implying 0% for a
		// provider that has had zero requests.
		var poolSuccessRate *float64
		if ms, ok := monthlyStats[p.ID]; ok {
			if cnt, ok := ms["request_count"].(int); ok && cnt > 0 {
				if sr, ok := ms["success_rate"].(float64); ok {
					v := sr
					poolSuccessRate = &v
				}
			}
		}

		enriched = append(enriched, EnrichedHealth{
			ProviderID:   p.ID,
			ProviderName: p.Name,
			Type:         p.Type,
			Status: func() string {
				if hasHealth {
					return h.Status
				}
				return "pending"
			}(),
			LatencyMS: func() float64 {
				if hasHealth {
					return h.LatencyMS
				}
				return 0
			}(),
			ConsecutiveFails: func() int {
				if hasHealth {
					return h.ConsecutiveFails
				}
				return 0
			}(),
			FailureReason: func() string {
				if hasHealth {
					return h.FailureReason
				}
				return ""
			}(),
			ModelCount:         enabledModelCount,
			TotalModelCount:    len(p.Models),
			PrivateModelCount:  privateModelCount,
			SharedModelCount:   sharedModelCount,
			TokenLimit:         p.TokenLimit,
			TokenUsed:          totalUsed,
			TodayRequests:      todayReqs,
			TodayTokens:        todayTokens,
			TodayReqsPrivate:   todayPrivReqs,
			TodayTokensPrivate: todayPrivTokens,
			TodayReqsPublic:    todayPubReqs,
			TodayTokensPublic:  todayPubTokens,
			TodayReqsGuest:     todayGuestReqs,
			TodayTokensGuest:   todayGuestTokens,
			KeyCount:           keyCount,
			PrivateKeyCount:    privateKeyCount,
			SharedKeyCount:     sharedKeyCount,
			FailedKeyCount: func() int {
				if hasHealth {
					return h.FailedKeyCount
				}
				return 0
			}(),
			Enabled:           p.Enabled,
			RateLimitEnabled:  p.RateLimitEnabled,
			RateLimitPerMin:   p.RateLimitPerMin,
			DailyRequestLimit: p.DailyRequestLimit,
			Priority:          p.Priority,
			IsShared:          isShared,
			SuccessRate:       poolSuccessRate, // real: monthly success rate (nil = no traffic yet)
			Models:            p.Models,
			AccessControl:     &ac,
			ActiveConns:       GetProviderConns(p.ID),
			QuotaPrivateUsed:  quotaPrivUsed,
			QuotaPrivateTotal: quotaPrivTotal,
			QuotaPublicUsed:   quotaPubUsed,
			QuotaPublicTotal:  quotaPubTotal,
			QuotaGuestUsed:    quotaGuestUsed,
			QuotaGuestTotal:   quotaGuestTotal,
			// Daily/Monthly quotas: private from config, public/guest split from shared
			QuotaPrivateDaily:   p.PrivateTokensDaily,
			QuotaPrivateMonthly: p.PrivateQuotaMonthly,
			QuotaPublicDaily:    int64(float64(p.SharedTokensDaily) * float64(100-guestPct) / 100.0),
			QuotaPublicMonthly:  int64(float64(p.SharedQuotaMonthly) * float64(100-guestPct) / 100.0),
			QuotaGuestDaily:     int64(float64(p.SharedTokensDaily) * float64(guestPct) / 100.0),
			QuotaGuestMonthly:   int64(float64(p.SharedQuotaMonthly) * float64(guestPct) / 100.0),
			// Daily/Monthly usage
			QuotaPrivateDailyUsed:   dailyPrivUsed,
			QuotaPrivateMonthlyUsed: monthlyPrivUsed,
			QuotaPublicDailyUsed:    dailyPubUsed,
			QuotaPublicMonthlyUsed:  monthlyPubUsed,
			QuotaGuestDailyUsed:     dailyGuestUsed,
			QuotaGuestMonthlyUsed:   monthlyGuestUsed,
		})
	}

	// Aggregate node_stats from enriched providers
	var totalProviders, onlineProviders, totalKeys, privateKeyCount, sharedKeyCount, failedKeys int
	var totalModels, enabledModels, privateModels, sharedModels int
	var totalLatency float64
	var latencyCount, successCount int
	var successSum float64
	var todayReqsPrivate, todayTokensPrivate int
	var todayReqsPublic, todayTokensPublic int
	var todayReqsGuest, todayTokensGuest int
	var quotaPrivUsed, quotaPrivTotal, quotaPubUsed, quotaPubTotal, quotaGuestUsed, quotaGuestTotal int64
	var quotaPrivDaily, quotaPrivMonthly, quotaPubDaily, quotaPubMonthly, quotaGuestDaily, quotaGuestMonthly int64
	var quotaPrivDailyUsed, quotaPrivMonthlyUsed, quotaPubDailyUsed, quotaPubMonthlyUsed, quotaGuestDailyUsed, quotaGuestMonthlyUsed int64

	for _, ep := range enriched {
		totalProviders++
		// Skip all stats (except totalProviders) for disabled providers
		if !ep.Enabled {
			continue
		}
		if ep.Status == "healthy" || ep.Status == "degraded" {
			onlineProviders++
		}
		totalKeys += ep.KeyCount
		privateKeyCount += ep.PrivateKeyCount
		sharedKeyCount += ep.SharedKeyCount
		failedKeys += ep.FailedKeyCount
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
		quotaPrivDaily += ep.QuotaPrivateDaily
		quotaPrivMonthly += ep.QuotaPrivateMonthly
		quotaPubDaily += ep.QuotaPublicDaily
		quotaPubMonthly += ep.QuotaPublicMonthly
		quotaGuestDaily += ep.QuotaGuestDaily
		quotaGuestMonthly += ep.QuotaGuestMonthly
		quotaPrivDailyUsed += ep.QuotaPrivateDailyUsed
		quotaPrivMonthlyUsed += ep.QuotaPrivateMonthlyUsed
		quotaPubDailyUsed += ep.QuotaPublicDailyUsed
		quotaPubMonthlyUsed += ep.QuotaPublicMonthlyUsed
		quotaGuestDailyUsed += ep.QuotaGuestDailyUsed
		quotaGuestMonthlyUsed += ep.QuotaGuestMonthlyUsed
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
	// Real guest connection count from the connection tracker (access type "guest").
	connsGuest = GetGuestConns()

	// Compute weighted average guest_pool_percent (weighted by shared key quota)
	var totalSharedQuota int64
	var weightedGuestPct int64
	for _, ep := range enriched {
		// Find the provider to get its access_control
		for _, p2 := range configured {
			if p2.ID == ep.ProviderID {
				gp := p2.AccessControl.GuestPoolPercent
				if gp <= 0 {
					gp = 50
				}
				if gp > 100 {
					gp = 100
				}
				// Weight by this provider's shared quota total
				var pSharedQuota int64
				for _, k := range p2.APIKeys {
					if k.Enabled && k.AccessControl == "shared" && k.Quota > 0 {
						pSharedQuota += k.Quota
					}
				}
				totalSharedQuota += pSharedQuota
				weightedGuestPct += int64(gp) * pSharedQuota
				break
			}
		}
	}
	avgGuestPct := 50 // default
	if totalSharedQuota > 0 {
		avgGuestPct = int(weightedGuestPct / totalSharedQuota)
	}

	nodeStats := map[string]any{
		"provider_total":             totalProviders,
		"provider_online":            onlineProviders,
		"key_total":                  totalKeys,
		"private_key_count":          privateKeyCount,
		"shared_key_count":           sharedKeyCount,
		"failed_key_count":           failedKeys,
		"model_total":                totalModels,
		"model_enabled":              enabledModels,
		"private_models":             privateModels,
		"shared_models":              sharedModels,
		"avg_latency":                avgLatency,
		"success_rate":               avgSuccessRate,
		"today_reqs_private":         todayReqsPrivate,
		"today_tokens_private":       todayTokensPrivate,
		"today_reqs_public":          todayReqsPublic,
		"today_tokens_public":        todayTokensPublic,
		"today_reqs_guest":           todayReqsGuest,
		"today_tokens_guest":         todayTokensGuest,
		"quota_private_used":         quotaPrivUsed,
		"quota_private_total":        quotaPrivTotal,
		"quota_public_used":          quotaPubUsed,
		"quota_public_total":         quotaPubTotal,
		"quota_guest_used":           quotaGuestUsed,
		"quota_guest_total":          quotaGuestTotal,
		"quota_private_daily":        quotaPrivDaily,
		"quota_private_monthly":      quotaPrivMonthly,
		"quota_public_daily":         quotaPubDaily,
		"quota_public_monthly":       quotaPubMonthly,
		"quota_guest_daily":          quotaGuestDaily,
		"quota_guest_monthly":        quotaGuestMonthly,
		"quota_private_daily_used":   quotaPrivDailyUsed,
		"quota_private_monthly_used": quotaPrivMonthlyUsed,
		"quota_public_daily_used":    quotaPubDailyUsed,
		"quota_public_monthly_used":  quotaPubMonthlyUsed,
		"quota_guest_daily_used":     quotaGuestDailyUsed,
		"quota_guest_monthly_used":   quotaGuestMonthlyUsed,
		"guest_pool_percent":         avgGuestPct,
		"conns_private":              connsPrivate,
		"conns_public":               connsPublic,
		"conns_guest":                connsGuest,
		"node_id":                    cfg.Get("node_name", ""),
		"version":                    AppVersion,
	}

	writeJSON(w, 200, map[string]any{"providers": enriched, "node_stats": nodeStats})
}