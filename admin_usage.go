package main

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// ============================================================
// Usage & Routing handlers
// ============================================================

func handleUsageSummary(w http.ResponseWriter, r *http.Request) {
	// SEC-B5-1: consumers only see their own usage; admin ("" owner) sees all.
	owner := getRequestOwner(r)
	stats30 := tracker.ProviderStatsOwned(30, owner)
	stats1 := tracker.ProviderStatsOwned(1, owner)
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
		"today_requests":     totalReqs1,
		"today_tokens":       totalTok1,
		"today_cost_usd":     round4(totalCost1),
		"total_requests_30d": totalReqs30,
		"total_tokens_30d":   totalTok30,
		"total_cost_usd_30d": round4(totalCost30),
		"providers_active":   len(stats30),
		"total_records":      tracker.CountOwned(owner),
	})
}

func handleUsageProviders(w http.ResponseWriter, r *http.Request) {
	days, err := strconv.Atoi(r.URL.Query().Get("days"))
	if err != nil || days <= 0 || days > 365 {
		days = 30
	}
	owner := getRequestOwner(r)
	writeJSON(w, 200, tracker.ProviderStatsOwned(days, owner))
}

func handleUsageRecords(w http.ResponseWriter, r *http.Request) {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit == 0 || limit > 500 {
		limit = 100
	}
	owner := getRequestOwner(r)
	tracker.mu.Lock()
	var recs []UsageRecord
	if owner == "" {
		recs = tracker.records
	} else {
		for _, rec := range tracker.records {
			if rec.Owner == owner {
				recs = append(recs, rec)
			}
		}
	}
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
	var body struct {
		Mode string `json:"mode"`
	}
	if err := readJSON(w, r, &body); err != nil {
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
	if err := readJSON(w, r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	weights := map[string]float64{
		"priority": clamp(body["priority"], 0, 1),
		"cost":     clamp(body["cost"], 0, 1),
		"latency":  clamp(body["latency"], 0, 1),
		"tokens":   clamp(body["tokens"], 0, 1),
	}
	b, err := json.Marshal(weights)
	if err != nil {
		writeError(w, 500, "failed to marshal weights")
		return
	}
	cfg.Set("routing_weights", string(b))
	writeJSON(w, 200, map[string]any{"success": true, "weights": weights})
}

func handleRoutingAdvice(w http.ResponseWriter, r *http.Request) {
	model := r.PathValue("model")
	advice := pm.RoutingAdvice(model)
	writeJSON(w, 200, map[string]any{"model": model, "candidates": advice, "count": len(advice)})
}